package main

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

type workspaceSnapshot struct {
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
	snapshot, err := scanWorkspace(projectDir)
	if err != nil {
		return nil, err
	}

	for _, src := range snapshot.Sources {
		svc, err := NewNugetServiceContext(ctx, src)
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

	return snapshot, nil
}

// scanWorkspace reads project and NuGet configuration without contacting any
// package source. Headless inspection commands use this path so listing local
// package declarations never depends on network availability.
func scanWorkspace(projectDir string) (*workspaceSnapshot, error) {
	fullProjectPath, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("getting absolute project directory: %w", err)
	}

	logInfo("Scanning workspace: %s", fullProjectPath)

	projectFiles, err := FindProjectFiles(fullProjectPath)
	if err != nil {
		return nil, fmt.Errorf("finding projects: %w", err)
	}
	logInfo("Found %d project(s)", len(projectFiles))

	var parsedProjects []*ParsedProject
	for _, file := range projectFiles {
		project, err := ParseCsproj(file)
		if err != nil {
			logWarn("Skipping unparseable project %s: %v", file, err)
			continue
		}
		parsedProjects = append(parsedProjects, project)
	}

	if len(parsedProjects) == 0 {
		return nil, fmt.Errorf("no parseable .csproj, .fsproj, or .vbproj files found in: %s", fullProjectPath)
	}

	propsProjects := collectPropsProjects(parsedProjects)
	logInfo("Found %d .props file(s) with packages", len(propsProjects))
	unsupported := findUnsupportedPackageArtifacts(fullProjectPath)
	for _, artifact := range unsupported {
		logWarn("Read-only package artifact %s: %s", artifact.Path, artifact.Reason)
	}

	detected := DetectSources(fullProjectPath)
	sources := detected.Sources
	sourceMapping := detected.Mapping
	logInfo("Detected %d NuGet source(s)", len(sources))
	if sourceMapping.IsConfigured() {
		logInfo("Package source mapping configured with %d source(s)", len(sourceMapping.Entries))
	}

	return &workspaceSnapshot{
		ProjectDir:     fullProjectPath,
		ParsedProjects: parsedProjects,
		PropsProjects:  propsProjects,
		Sources:        sources,
		SourceMapping:  sourceMapping,
		Unsupported:    unsupported,
	}, nil
}

func findUnsupportedPackageArtifacts(root string) []unsupportedPackageArtifact {
	var artifacts []unsupportedPackageArtifact
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != root && shouldSkipProjectDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.EqualFold(entry.Name(), "packages.config"):
			artifacts = append(artifacts, unsupportedPackageArtifact{Path: path, Reason: "packages.config mutation is not supported"})
		case strings.EqualFold(entry.Name(), "dotnet-tools.json") && strings.EqualFold(filepath.Base(filepath.Dir(path)), ".config"):
			artifacts = append(artifacts, unsupportedPackageArtifact{Path: path, Reason: ".NET tool manifests are outside project package management"})
		}
		return nil
	})
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts
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

	var b strings.Builder
	for _, part := range parts {
		b.WriteString(part)
		b.WriteByte('\n')
	}

	if mapping != nil {
		keys := make([]string, 0, len(mapping.Entries))
		for key := range mapping.Entries {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			patterns := append([]string(nil), mapping.Entries[key]...)
			sort.Strings(patterns)
			b.WriteString(strings.ToLower(key))
			b.WriteByte('=')
			b.WriteString(strings.Join(patterns, ","))
			b.WriteByte('\n')
		}
	}

	return b.String()
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

func fetchPackageMetadataAsync(send func(tea.Msg), generation int, nugetServices []*NugetService, sourceMapping *PackageSourceMapping, packageNames []string) {
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
						info, lastErr = svc.SearchExact(name)
						if lastErr == nil {
							sourceName = svc.SourceName()
							break
						}
						logDebug("Source [%s] failed for %s: %v", svc.SourceName(), name, lastErr)
					}

					if info != nil && !strings.EqualFold(sourceName, "nuget.org") && nugetOrgSvc != nil && serviceIncluded(eligibleServices, nugetOrgSvc) {
						if nugetInfo, err := nugetOrgSvc.SearchExact(name); err == nil {
							info.NugetOrgURL = "https://www.nuget.org/packages/" + nugetInfo.ID
							enrichFromNugetOrg(info, nugetInfo)
						}
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
			jobs <- name
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
