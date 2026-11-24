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
// project's sources and a mapping that remembers each project scope.
type workspaceSources struct {
	Sources        []NugetSource
	Mapping        *PackageSourceMapping // nil when no project uses package source mapping
	InputRevisions map[string][sha256.Size]byte
}

// resolveWorkspaceSources resolves the sources restore uses for each project:
// NuGet configuration from the project directory upward, then the project's
// RestoreSources overrides. Configuration errors make the view unusable.
func resolveWorkspaceSources(projects []*ParsedProject) (workspaceSources, error) {
	result := workspaceSources{InputRevisions: make(map[string][sha256.Size]byte)}
	settingsByDir := make(map[string]*nugetconfig.Settings)
	scopes := make(map[string]int)
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
		key := scope.signature()
		if _, seen := scopes[key]; seen {
			continue
		}
		scopes[key] = len(all)
		all = append(all, scope)
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
	mapping := &PackageSourceMapping{scopes: all}
	if mapping.IsConfigured() {
		result.Mapping = mapping
	}
	return result, nil
}

// sameRestoreProperties reports whether two parses of a project carry the
// same MSBuild source overrides, so its resolved sources cannot differ.
func sameRestoreProperties(a, b *ParsedProject) bool {
	if (a.RestoreSources == nil) != (b.RestoreSources == nil) || a.RestoreAdditionalProjectSources != b.RestoreAdditionalProjectSources {
		return false
	}
	return a.RestoreSources == nil || *a.RestoreSources == *b.RestoreSources
}

// PackageSourceMapping decides which sources may supply a package. A source
// is eligible when some project scope that uses it either has no package
// source mapping or maps the package to it. A nil mapping allows every source.
type PackageSourceMapping struct {
	scopes []sourceScope
}

type sourceScope struct {
	sources []NugetSource
	mapping *nugetconfig.Mapping
}

func (s sourceScope) includes(name, url string) bool {
	for _, source := range s.sources {
		if source.Name == name && source.URL == url {
			return true
		}
	}
	return false
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

// Allows reports whether the source with this name and URL may supply packageID.
func (m *PackageSourceMapping) Allows(name, url, packageID string) bool {
	if !m.IsConfigured() {
		return true
	}
	for _, scope := range m.scopes {
		if !scope.includes(name, url) {
			continue
		}
		if scope.mapping == nil || containsFold(scope.mapping.SourcesFor(packageID), name) {
			return true
		}
	}
	return false
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

func (m *PackageSourceMapping) signature() string {
	if !m.IsConfigured() {
		return ""
	}
	var parts []string
	for _, scope := range m.scopes {
		parts = append(parts, scope.signature())
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
