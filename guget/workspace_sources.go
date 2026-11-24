package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nulifyer/guget/internal/nugetconfig"
)

// NugetSource is an enabled package source with its resolved credentials.
type NugetSource = nugetconfig.Source

// workspaceSources is the NuGet source view of a workspace. Restore resolves
// configuration per project, so the workspace holds the union of every
// project's sources and each project's own source scope.
type workspaceSources struct {
	Sources        []NugetSource
	Mapping        *PackageSourceMapping // each project's sources and mapping
	InputRevisions map[string][sha256.Size]byte
}

// resolveWorkspaceSources resolves the sources restore uses for each project:
// NuGet configuration from the project directory upward, then the project's
// RestoreSources overrides. Configuration errors make the view unusable.
func resolveWorkspaceSources(projects []*ParsedProject) (workspaceSources, error) {
	result := workspaceSources{InputRevisions: make(map[string][sha256.Size]byte)}
	settingsByDir := make(map[string]*nugetconfig.Settings)
	scopes := make(map[string]int)
	projectScopes := make(map[string]int)
	importers := make(map[string][]int)
	var all []sourceScope
	var errs []error
	reported := make(map[string]bool)
	report := func(err error) {
		if !reported[err.Error()] {
			reported[err.Error()] = true
			errs = append(errs, err)
		}
	}

	for _, project := range projects {
		projectPath, err := filepath.Abs(project.FilePath)
		if err != nil {
			report(fmt.Errorf("resolve project path %s: %w", project.FilePath, err))
			continue
		}
		dir := filepath.Dir(projectPath)
		settings, known := settingsByDir[dir]
		if !known {
			loaded, err := nugetconfig.Load(dir, nugetconfig.Environment{})
			for _, file := range loaded.Files {
				if previous, seen := result.InputRevisions[file.Path]; seen && previous != file.Digest {
					return workspaceSources{}, fmt.Errorf("%w: source configuration %s", errWorkspaceInputsChanged, file.Path)
				}
				result.InputRevisions[file.Path] = file.Digest
			}
			if err != nil {
				report(err)
				loaded = nil
			} else {
				for _, warning := range loaded.Warnings {
					if !reported[warning] {
						reported[warning] = true
						logWarn("NuGet configuration: %s", warning)
					}
				}
			}
			settings, settingsByDir[dir] = loaded, loaded
		}
		if settings == nil {
			continue
		}
		sources, err := settings.ProjectSources(dir, nugetconfig.RestoreProperties{
			Sources:           project.RestoreSources,
			AdditionalSources: project.RestoreAdditionalProjectSources,
		})
		if err != nil {
			report(fmt.Errorf("%s: %w", projectPath, err))
			continue
		}
		scope := sourceScope{sources: sources, mapping: settings.Mapping}
		scope.key = scope.signature()
		index, seen := scopes[scope.key]
		if !seen {
			index = len(all)
			scopes[scope.key] = index
			all = append(all, scope)
		}
		projectScopes[filepath.Clean(projectPath)] = index
		for _, consumed := range consumedFiles(project) {
			if !slices.Contains(importers[consumed], index) {
				importers[consumed] = append(importers[consumed], index)
			}
		}
		if seen {
			continue
		}
		for _, source := range sources {
			logRegisterSecret(source.Password)
			if !slices.Contains(result.Sources, source) {
				result.Sources = append(result.Sources, source)
			}
		}
	}
	if len(errs) > 0 {
		return result, errors.Join(errs...)
	}
	if len(all) > 1 {
		logInfo("Projects use %d different NuGet source configurations", len(all))
	}
	result.Mapping = &PackageSourceMapping{scopes: all, projects: projectScopes, importers: importers}
	return result, nil
}

// consumedFiles lists the files besides the project file whose declarations
// the project uses: its imports and its package version owners.
func consumedFiles(project *ParsedProject) []string {
	var files []string
	for path := range project.InputRevisions {
		files = append(files, filepath.Clean(path))
	}
	for _, path := range project.PackageSources {
		files = append(files, filepath.Clean(path))
	}
	slices.Sort(files)
	return slices.Compact(files)
}

// sameRestoreProperties reports whether two parses of a project carry the
// same MSBuild source overrides, so its resolved sources cannot differ.
func sameRestoreProperties(a, b *ParsedProject) bool {
	if (a.RestoreSources == nil) != (b.RestoreSources == nil) || a.RestoreAdditionalProjectSources != b.RestoreAdditionalProjectSources {
		return false
	}
	return a.RestoreSources == nil || *a.RestoreSources == *b.RestoreSources
}

// PackageSourceMapping records the NuGet source scope of every project:
// the sources restore uses for it and its package source mapping. Restore
// resolves each project on its own, so a source may supply a package to a
// project only when that project's scope includes the source and, if the
// scope has a mapping, maps the package to it. A nil value allows every
// source for every project.
type PackageSourceMapping struct {
	scopes []sourceScope
	// projects maps each project file to the index of its scope.
	projects map[string]int
	// importers maps a file that projects import or take package versions
	// from to the scopes of those projects.
	importers map[string][]int
}

type sourceScope struct {
	sources []NugetSource
	mapping *nugetconfig.Mapping
	key     string // signature, computed once
}

func (s sourceScope) includes(name, url string) bool {
	for _, source := range s.sources {
		if source.Name == name && source.URL == url {
			return true
		}
	}
	return false
}

// allows reports whether the scope may get packageID from the source.
func (s sourceScope) allows(name, url, packageID string) bool {
	return s.includes(name, url) && (s.mapping == nil || containsFold(s.mapping.SourcesFor(packageID), name))
}

func (s sourceScope) signature() string {
	var b strings.Builder
	for _, source := range s.sources {
		// Hash credentials so signatures never hold secrets.
		fmt.Fprintf(&b, "%s\x00%s\x00%x\n", source.Name, source.URL, sha256.Sum256([]byte(source.Username+"\x00"+source.Password)))
	}
	for _, name := range s.mapping.Sources() {
		fmt.Fprintf(&b, "map %s=%s\n", strings.ToLower(name), strings.Join(s.mapping.Patterns(name), ","))
	}
	return b.String()
}

// IsConfigured reports whether any project uses package source mapping.
func (m *PackageSourceMapping) IsConfigured() bool {
	if m == nil {
		return false
	}
	for _, scope := range m.scopes {
		if scope.mapping != nil {
			return true
		}
	}
	return false
}

// Allows reports whether some project's scope may get packageID from the
// source with this name and URL. It is the workspace view, for listing what
// any project could use; decisions for a project use AllowsFor.
func (m *PackageSourceMapping) Allows(name, url, packageID string) bool {
	if !m.IsConfigured() {
		return true
	}
	for _, scope := range m.scopes {
		if scope.allows(name, url, packageID) {
			return true
		}
	}
	return false
}

// AllowsFor reports whether every project that uses path may get packageID
// from the source: a project's own scope, or for an imported file the scope
// of each importing project. A path no project uses gets the workspace view.
func (m *PackageSourceMapping) AllowsFor(path, name, url, packageID string) bool {
	scopes := m.scopesOf(path)
	if len(scopes) == 0 {
		return m.Allows(name, url, packageID)
	}
	for _, index := range scopes {
		if !m.scopes[index].allows(name, url, packageID) {
			return false
		}
	}
	return true
}

// scopesOf returns the scope indexes of the projects that use path, or nil
// for a path no project uses.
func (m *PackageSourceMapping) scopesOf(path string) []int {
	if m == nil {
		return nil
	}
	path = filepath.Clean(path)
	if index, ok := m.projects[path]; ok {
		return []int{index}
	}
	return m.importers[path]
}

// workspaceScope is the scope index that stands for the workspace view.
const workspaceScope = -1

// scopesForPaths returns the distinct, sorted scope indexes of the projects
// that use the paths, or only the workspace scope when none is known.
func (m *PackageSourceMapping) scopesForPaths(paths ...string) []int {
	var scopes []int
	for _, path := range paths {
		for _, index := range m.scopesOf(path) {
			if !slices.Contains(scopes, index) {
				scopes = append(scopes, index)
			}
		}
	}
	if len(scopes) == 0 {
		return []int{workspaceScope}
	}
	slices.Sort(scopes)
	return scopes
}

// eligibleServices returns the services the scope at index may use for
// packageID. The workspace scope uses the workspace view.
func (m *PackageSourceMapping) eligibleServices(services []*NugetService, index int, packageID string) []*NugetService {
	if m == nil || index < 0 || index >= len(m.scopes) {
		return FilterServices(services, m, packageID)
	}
	scope := m.scopes[index]
	var eligible []*NugetService
	for _, service := range services {
		if scope.allows(service.SourceName(), service.SourceURL(), packageID) {
			eligible = append(eligible, service)
		}
	}
	if len(eligible) == 0 && scope.mapping != nil && len(services) > 0 {
		logWarn("Package %q matches no available package source mapping", packageID)
	}
	return eligible
}

// scopeMapped reports whether the scope at index uses package source mapping.
func (m *PackageSourceMapping) scopeMapped(index int) bool {
	if m == nil || index < 0 || index >= len(m.scopes) {
		return m.IsConfigured()
	}
	return m.scopes[index].mapping != nil
}

// packageScopes is the set of scopes whose projects reference a package.
type packageScopes struct {
	indexes []int
	// key identifies the scopes' configurations across workspace loads, so
	// cached metadata is reused only for the same set of scopes.
	key string
}

// packageScopesFor returns a lookup of the scopes that reference each
// package: those of the projects that reference it; for a package only a
// .props file declares, those of the projects importing that file; otherwise
// every scope. The lookup is safe for concurrent use.
func (m *PackageSourceMapping) packageScopesFor(projects, props []*ParsedProject) func(packageID string) packageScopes {
	if m == nil || len(m.scopes) == 0 {
		return func(string) packageScopes { return packageScopes{indexes: []int{workspaceScope}} }
	}
	collect := func(sources []*ParsedProject) map[string][]string {
		paths := make(map[string][]string)
		for _, project := range sources {
			for ref := range project.Packages {
				name := strings.ToLower(ref.Name)
				paths[name] = append(paths[name], project.FilePath)
			}
		}
		return paths
	}
	referenced, declared := collect(projects), collect(props)
	every := make([]int, len(m.scopes))
	for i := range every {
		every[i] = i
	}
	return func(packageID string) packageScopes {
		name := strings.ToLower(packageID)
		paths := referenced[name]
		if len(paths) == 0 {
			paths = declared[name]
		}
		indexes := m.scopesForPaths(paths...)
		if indexes[0] == workspaceScope {
			indexes = every
		}
		keys := make([]string, len(indexes))
		for i, index := range indexes {
			keys[i] = m.scopes[index].key
		}
		slices.Sort(keys)
		return packageScopes{indexes: indexes, key: strings.Join(keys, "\x01")}
	}
}

// Patterns returns the package patterns mapped to a source in any scope.
func (m *PackageSourceMapping) Patterns(source NugetSource) []string {
	if m == nil {
		return nil
	}
	var patterns []string
	for _, scope := range m.scopes {
		if scope.includes(source.Name, source.URL) {
			patterns = append(patterns, scope.mapping.Patterns(source.Name)...)
		}
	}
	slices.Sort(patterns)
	return slices.Compact(patterns)
}

// signature identifies the set of scopes, so a change in any project's
// sources or mapping invalidates metadata fetched for the old scopes.
func (m *PackageSourceMapping) signature() string {
	if m == nil || len(m.scopes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.scopes))
	for _, scope := range m.scopes {
		parts = append(parts, scope.key)
	}
	slices.Sort(parts)
	return strings.Join(parts, "\x01")
}

// FilterServices returns services allowed for packageID by the mapping. When a
// mapping is configured, no match or an unavailable mapped source is an empty
// result: silently broadening to other feeds would violate the trust boundary.
func FilterServices(services []*NugetService, mapping *PackageSourceMapping, packageID string) []*NugetService {
	if !mapping.IsConfigured() {
		return services
	}
	var filtered []*NugetService
	for _, service := range services {
		if mapping.Allows(service.SourceName(), service.SourceURL(), packageID) {
			filtered = append(filtered, service)
		}
	}
	if len(filtered) == 0 {
		logWarn("Package %q matches no available package source mapping", packageID)
	}
	return filtered
}
