package nugetconfig

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// RestoreProperties are a project's MSBuild source overrides.
type RestoreProperties struct {
	Sources           *string // RestoreSources; nil when the project does not set it
	AdditionalSources string  // RestoreAdditionalProjectSources
}

// ProjectSources returns the sources restore uses for a project in projectDir.
// RestoreSources replaces the configured sources, and "Clear" empties them.
// RestoreAdditionalProjectSources are appended in ordinal order. A property
// source receives a configured source's name and credentials only when an
// enabled configured source has exactly the same location.
func (s *Settings) ProjectSources(projectDir string, properties RestoreProperties) ([]Source, error) {
	sources := append([]Source(nil), s.Sources...)
	// MSBuild passes an empty property as unset, so configured sources remain.
	if entries := splitSourceList(valueOf(properties.Sources)); len(entries) > 0 {
		sources = nil
		for _, entry := range entries {
			if strings.EqualFold(entry, "clear") {
				if len(entries) > 1 {
					return nil, fmt.Errorf("RestoreSources cannot combine Clear with other sources")
				}
				return s.withAdditional(nil, projectDir, properties.AdditionalSources), nil
			}
			sources = append(sources, s.sourceAt(resolvePropertySource(entry, projectDir)))
		}
	}
	return s.withAdditional(sources, projectDir, properties.AdditionalSources), nil
}

// withAdditional appends additional sources in ordinal order of their
// property values, as restore does.
func (s *Settings) withAdditional(sources []Source, projectDir, additional string) []Source {
	entries := splitSourceList(additional)
	sort.Strings(entries)
	for _, entry := range entries {
		location := resolvePropertySource(entry, projectDir)
		duplicate := false
		for _, source := range sources {
			duplicate = duplicate || strings.EqualFold(source.URL, location)
		}
		if !duplicate {
			sources = append(sources, s.sourceAt(location))
		}
	}
	return sources
}

func (s *Settings) sourceAt(location string) Source {
	for _, source := range s.Sources {
		if source.URL == location {
			return source
		}
	}
	return Source{Name: location, URL: location}
}

func valueOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func splitSourceList(value string) []string {
	var entries []string
	for _, entry := range strings.Split(value, ";") {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// resolvePropertySource repairs HTTP scheme slashes that MSBuild collapses in
// values without any "//", converts file URIs to paths, then resolves local
// paths against the project directory.
func resolvePropertySource(entry, projectDir string) string {
	if !strings.Contains(entry, "//") {
		lower := strings.ToLower(entry)
		for _, scheme := range []string{"http:", "https:"} {
			if strings.HasPrefix(lower, scheme+"/") {
				entry = entry[:len(scheme)] + "//" + entry[len(scheme)+1:]
				break
			}
		}
	}
	if isHTTP(entry) {
		return entry
	}
	if strings.HasPrefix(strings.ToLower(entry), "file:") {
		path := strings.TrimLeft(entry[len("file:"):], "/")
		if runtime.GOOS != "windows" {
			path = "/" + path
		}
		entry = filepath.FromSlash(path)
	}
	return resolveLocalPath(entry, projectDir)
}
