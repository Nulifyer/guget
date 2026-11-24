package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	ui "github.com/nulifyer/guget/internal/ui"
)

type workspaceSnapshot struct {
	FileRevisions  map[string]watchedFileState
	InputRevisions map[string][sha256.Size]byte
	ProjectDir     string
	ParsedProjects []*ParsedProject
	PropsProjects  []*ParsedProject
	Sources        []NugetSource
	SourceMapping  *PackageSourceMapping
	NugetServices  []*NugetService
	Unsupported    []unsupportedPackageArtifact
}

type unsupportedPackageArtifact struct {
	Path   string
	Reason string
}

func loadWorkspace(projectDir string) (*workspaceSnapshot, error) {
	return loadWorkspaceContext(context.Background(), projectDir)
}

func loadWorkspaceContext(ctx context.Context, projectDir string) (*workspaceSnapshot, error) {
	return loadWorkspaceReusingServices(ctx, projectDir, workspaceScanOptions{}, nil)
}

// Saves only change package declarations. Reuse initialized feeds when their
// complete configuration is unchanged, avoiding network discovery per keypress.
// Manual and NuGet.Config reloads pass nil so credentials and failed feeds refresh.
func loadWorkspaceReusingServices(ctx context.Context, projectDir string, opts workspaceScanOptions, previous *workspaceSnapshot) (*workspaceSnapshot, error) {
	snapshot, err := scanWorkspaceWith(projectDir, opts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if previous != nil && slices.Equal(previous.Sources, snapshot.Sources) {
		snapshot.NugetServices = previous.NugetServices
		return snapshot, nil
	}

	for _, src := range snapshot.Sources {
		svc, err := newDeferredNugetService(src, snapshot.Sources)
		if err != nil {
			logWarn("Failed to initialise NuGet source [%s]: %v", src.Name, err)
			continue
		}
		snapshot.NugetServices = append(snapshot.NugetServices, svc)
	}
	if len(snapshot.NugetServices) == 0 {
		logWarn("No metadata-capable NuGet sources are available; package inspection remains available")
	}
	DeduplicateADOUpstreams(snapshot.NugetServices)
	// Validate the local disk view before publication. Sources initialize only
	// when metadata/search work needs them, after projects are already visible.
	if err := validateWorkspaceSnapshot(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

// scanWorkspace reads project and NuGet configuration without contacting any
// package source. Headless inspection commands use this path so listing local
// package declarations never depends on network availability.
func scanWorkspace(projectDir string) (*workspaceSnapshot, error) {
	return scanWorkspaceWith(projectDir, workspaceScanOptions{})
}

func scanWorkspaceWith(projectDir string, opts workspaceScanOptions) (*workspaceSnapshot, error) {
	return scanWorkspaceWithParser(projectDir, opts, ParseCsproj)
}

// Validate a whole disk view, including external owners discovered during parsing.
// Generations order requests; these revisions detect changes within one request.
// Each attempt lists the workspace once. The before and after revisions re-read
// the files that walk found, so a slow filesystem pays for one directory walk.
func scanWorkspaceWithParser(projectDir string, opts workspaceScanOptions, parse func(string) (*ParsedProject, error)) (*workspaceSnapshot, error) {
	fullProjectPath, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("getting absolute project directory: %w", err)
	}
	inputs := workspaceInputPaths(fullProjectPath, &workspaceSnapshot{})
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		tree, err := walkWorkspace(fullProjectPath, opts)
		if err != nil {
			return nil, fmt.Errorf("finding projects: %w", err)
		}
		before, err := readWatchedFileStates(tree.WatchedFiles, inputs)
		if err != nil {
			return nil, fmt.Errorf("reading workspace input revisions: %w", err)
		}
		snapshot, err := scanWorkspaceOnce(fullProjectPath, tree, parse, before)
		if errors.Is(err, errWorkspaceInputsChanged) {
			continue
		}
		if err != nil {
			return nil, err
		}
		inputs = mergeWorkspaceInputPaths(inputs, workspaceInputPaths(fullProjectPath, snapshot))
		after, err := readWatchedFileStates(tree.WatchedFiles, inputs)
		if err != nil {
			return nil, fmt.Errorf("validating workspace input revisions: %w", err)
		}
		if sameWorkspaceInputRevisions(before, after) {
			snapshot.FileRevisions = after
			return snapshot, nil
		}
	}
	return nil, fmt.Errorf("workspace inputs changed during %d consecutive scans; wait for saves to finish and reload", maxAttempts)
}

func validateWorkspaceSnapshot(snapshot *workspaceSnapshot) error {
	if snapshot.FileRevisions == nil {
		return nil
	}
	known := make([]string, 0, len(snapshot.FileRevisions))
	for path := range snapshot.FileRevisions {
		known = append(known, path)
	}
	current, err := readWatchedFileStates(known, workspaceInputPaths(snapshot.ProjectDir, snapshot))
	if err != nil {
		return fmt.Errorf("revalidating workspace before publication: %w", err)
	}
	if !sameWorkspaceInputRevisions(snapshot.FileRevisions, current) {
		return fmt.Errorf("workspace inputs changed while loading package sources; reload to use the current files")
	}
	return nil
}

func mergeWorkspaceInputPaths(first, second []string) []string {
	paths := append(append([]string(nil), first...), second...)
	slices.Sort(paths)
	return slices.Compact(paths)
}

func sameWorkspaceInputRevisions(before, after map[string]watchedFileState) bool {
	if len(before) != len(after) {
		return false
	}
	for path, prior := range before {
		current, ok := after[path]
		if !ok || current.Hash != prior.Hash || current.Owner != prior.Owner {
			return false
		}
	}
	return true
}

var errWorkspaceInputsChanged = errors.New("workspace inputs changed while scanning")

func scanWorkspaceOnce(fullProjectPath string, tree *workspaceTree, parse func(string) (*ParsedProject, error), revisions map[string]watchedFileState) (*workspaceSnapshot, error) {
	logInfo("Scanning workspace: %s", fullProjectPath)

	projectFiles := tree.ProjectFiles
	logInfo("Found %d project(s)", len(projectFiles))
	if tree.GitIgnored > 0 {
		logInfo("Skipped %d path(s) excluded by .gitignore; use --%s to search them", tree.GitIgnored, includeIgnoredFlag)
	}

	var parsedProjects []*ParsedProject
	for _, file := range projectFiles {
		project, err := parse(file)
		if err != nil {
			return nil, fmt.Errorf("incomplete workspace discovery: project %s could not be parsed: %w", file, err)
		}
		if err := validateProjectInputRevisions(project, revisions); err != nil {
			return nil, err
		}
		parsedProjects = append(parsedProjects, project)
	}

	if len(parsedProjects) == 0 {
		if tree.GitIgnored > 0 {
			return nil, fmt.Errorf("no parseable .csproj, .fsproj, or .vbproj files found in: %s (%d path(s) excluded by .gitignore were not searched; use --%s to search them)", fullProjectPath, tree.GitIgnored, includeIgnoredFlag)
		}
		return nil, fmt.Errorf("no parseable .csproj, .fsproj, or .vbproj files found in: %s", fullProjectPath)
	}

	propsProjects := collectPropsProjects(parsedProjects)
	for _, project := range propsProjects {
		if err := validateProjectInputRevisions(project, revisions); err != nil {
			return nil, err
		}
	}
	logInfo("Found %d .props file(s) with packages", len(propsProjects))
	unsupported := tree.Unsupported
	for _, artifact := range unsupported {
		logWarn("Read-only package artifact %s: %s", artifact.Path, artifact.Reason)
	}

	detected, err := resolveWorkspaceSources(parsedProjects)
	for path, actual := range detected.InputRevisions {
		if expected, known := revisions[path]; known && expected.Hash != actual {
			return nil, fmt.Errorf("%w: source configuration %s consumed a different revision", errWorkspaceInputsChanged, path)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("incomplete source configuration: %w", err)
	}
	sources := detected.Sources
	sourceMapping := detected.Mapping
	logInfo("Detected %d NuGet source(s)", len(sources))
	if sourceMapping.IsConfigured() {
		logInfo("Package source mapping is configured")
	}

	return &workspaceSnapshot{
		ProjectDir:     fullProjectPath,
		InputRevisions: detected.InputRevisions,
		ParsedProjects: parsedProjects,
		PropsProjects:  propsProjects,
		Sources:        sources,
		SourceMapping:  sourceMapping,
		Unsupported:    unsupported,
	}, nil
}

// Compare the exact bytes consumed and current bytes with the baseline. A later
// restore of the original contents cannot disguise a different parsed revision.
func validateProjectInputRevisions(project *ParsedProject, baseline map[string]watchedFileState) error {
	for path, actual := range project.InputRevisions {
		if expected, known := baseline[path]; known && expected.Hash != actual {
			return fmt.Errorf("%w: %s consumed a different revision", errWorkspaceInputsChanged, path)
		}
	}
	for _, path := range projectInputPaths(project) {
		expected, known := baseline[path]
		if !known {
			continue
		} // Newly discovered external inputs require another attempt.
		current, exists, err := readWatchedFileState(path)
		if err != nil {
			return fmt.Errorf("validate project input %s: %w", path, err)
		}
		if !exists || current.Hash != expected.Hash || current.Owner != expected.Owner {
			return fmt.Errorf("%w: %s", errWorkspaceInputsChanged, path)
		}
	}
	return nil
}

func collectPropsProjects(parsedProjects []*ParsedProject) []*ParsedProject {
	propsSet := make(map[string]bool)
	for _, p := range parsedProjects {
		for _, source := range p.PackageSources {
			if strings.HasSuffix(strings.ToLower(source), ".props") {
				absSource, err := filepath.Abs(source)
				if err == nil {
					propsSet[absSource] = true
				}
			}
		}
	}

	propsPaths := make([]string, 0, len(propsSet))
	for propsPath := range propsSet {
		propsPaths = append(propsPaths, propsPath)
	}
	sort.Strings(propsPaths)

	var propsProjects []*ParsedProject
	for _, propsPath := range propsPaths {
		pp, err := ParsePropsAsProject(propsPath)
		if err != nil {
			logWarn("Failed to parse props file %s as project: %v", propsPath, err)
			continue
		}
		if pp.Packages.Len() > 0 {
			propsProjects = append(propsProjects, pp)
		}
	}
	return propsProjects
}

func distinctPackageNames(parsedProjects []*ParsedProject, propsProjects []*ParsedProject) []string {
	set := NewSet[string]()
	for _, project := range parsedProjects {
		for pkg := range project.Packages {
			set.Add(pkg.Name)
		}
	}
	for _, project := range propsProjects {
		for pkg := range project.Packages {
			set.Add(pkg.Name)
		}
	}

	names := set.ToSlice()
	sort.Strings(names)
	return names
}

func workspaceSourceSignature(sources []NugetSource, mapping *PackageSourceMapping) string {
	var parts []string
	for _, source := range sources {
		parts = append(parts, strings.ToLower(source.Name)+"="+strings.TrimRight(strings.ToLower(source.URL), "/"))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n") + "\n" + mapping.signature()
}

func planPackageReload(snapshot *workspaceSnapshot, current map[string]nugetResult, invalidateAll bool) (map[string]nugetResult, []string) {
	names := distinctPackageNames(snapshot.ParsedProjects, snapshot.PropsProjects)
	next := make(map[string]nugetResult, len(names))
	toFetch := make([]string, 0, len(names))

	for _, name := range names {
		if !invalidateAll {
			if res, ok := current[name]; ok && res.pkg != nil && res.err == nil {
				next[name] = res
				continue
			}
		}
		toFetch = append(toFetch, name)
	}

	return next, toFetch
}

func fetchPackageMetadataAsync(ctx context.Context, send func(ui.Event), generation int, nugetServices []*NugetService, sourceMapping *PackageSourceMapping, packageNames []string) {
	if send == nil || len(packageNames) == 0 {
		return
	}

	go func() {
		var nugetOrgSvc *NugetService
		for _, svc := range nugetServices {
			if strings.EqualFold(svc.SourceName(), "nuget.org") {
				nugetOrgSvc = svc
				break
			}
		}
		workerCount := 8
		if len(packageNames) < workerCount {
			workerCount = len(packageNames)
		}
		jobs := make(chan string)
		var wg sync.WaitGroup
		for range workerCount {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for name := range jobs {
					if ctx.Err() != nil {
						return
					}
					var info *PackageInfo
					var sourceName string
					var lastErr error
					eligibleServices := FilterServices(nugetServices, sourceMapping, name)
					if len(eligibleServices) == 0 {
						if len(nugetServices) == 0 {
							lastErr = fmt.Errorf("no metadata-capable NuGet source is available for %q", name)
						} else {
							lastErr = fmt.Errorf("package source mapping allows no available source for %q", name)
						}
					}
					for _, svc := range eligibleServices {
						info, lastErr = svc.SearchExactContext(ctx, name)
						if lastErr == nil {
							sourceName = svc.SourceName()
							break
						}
						logDebug("Source [%s] failed for %s: %v", svc.SourceName(), name, lastErr)
					}

					if info != nil && !strings.EqualFold(sourceName, "nuget.org") && nugetOrgSvc != nil && serviceIncluded(eligibleServices, nugetOrgSvc) {
						if nugetInfo, err := nugetOrgSvc.SearchExactContext(ctx, name); err == nil {
							info.NugetOrgURL = "https://www.nuget.org/packages/" + nugetInfo.ID
							enrichFromNugetOrg(info, nugetInfo)
						}
					}

					if ctx.Err() != nil {
						return
					}
					send(packageReadyMsg{
						generation: generation,
						name:       name,
						result:     nugetResult{pkg: info, source: sourceName, err: lastErr},
					})
				}
			}()
		}
		for _, name := range packageNames {
			select {
			case jobs <- name:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return
			}
		}
		close(jobs)
		wg.Wait()
	}()
}

func serviceIncluded(services []*NugetService, target *NugetService) bool {
	for _, service := range services {
		if service == target {
			return true
		}
	}
	return false
}
