package main

import (
	"sort"
	"strings"
)

type packageSourceMappingXML struct {
	Sources []mappedSourceXML `xml:"packageSource"`
	Clear   []struct{}        `xml:"clear"`
}

type mappedSourceXML struct {
	Key      string             `xml:"key,attr"`
	Patterns []mappedPatternXML `xml:"package"`
}

type mappedPatternXML struct {
	Pattern string `xml:"pattern,attr"`
}

type PackageSourceMapping struct {
	Entries map[string][]string // source key → lowercase patterns
}

func (m *PackageSourceMapping) IsConfigured() bool {
	return m != nil && len(m.Entries) > 0
}

func (m *PackageSourceMapping) SourcesForPackage(packageID string) []string {
	if !m.IsConfigured() {
		return nil
	}
	bestSpecificity := -1
	var matched []string
	for sourceKey, patterns := range m.Entries {
		sourceSpecificity := -1
		for _, p := range patterns {
			if specificity := patternSpecificity(packageID, p); specificity > sourceSpecificity {
				sourceSpecificity = specificity
			}
		}
		switch {
		case sourceSpecificity < 0:
		case sourceSpecificity > bestSpecificity:
			bestSpecificity = sourceSpecificity
			matched = []string{sourceKey}
		case sourceSpecificity == bestSpecificity:
			matched = append(matched, sourceKey)
		}
	}
	sort.Strings(matched)
	return matched
}

// patternSpecificity applies NuGet's strongest-pattern rule. Exact package IDs
// outrank prefixes, longer prefixes outrank shorter prefixes, and "*" is the
// least specific match. A negative result means no match.
func patternSpecificity(packageID, pattern string) int {
	if !matchPattern(packageID, pattern) {
		return -1
	}
	pattern = strings.ToLower(pattern)
	if pattern == "*" {
		return 0
	}
	if strings.HasSuffix(pattern, ".*") {
		return len(pattern) - 1
	}
	return 1_000_000 + len(pattern)
}

// matchPattern: "*" matches all, "Prefix.*" matches prefix, otherwise exact. Case-insensitive.
func matchPattern(packageID, pattern string) bool {
	id := strings.ToLower(packageID)
	pat := strings.ToLower(pattern)

	if pat == "*" {
		return true
	}
	if strings.HasSuffix(pat, ".*") {
		prefix := pat[:len(pat)-1]
		return strings.HasPrefix(id, prefix)
	}
	return id == pat
}

// FilterServices returns services allowed for packageID by the mapping. When a
// mapping is configured, no match or an unavailable mapped source is an empty
// result: silently broadening to other feeds would violate the trust boundary.
func FilterServices(services []*NugetService, mapping *PackageSourceMapping, packageID string) []*NugetService {
	if !mapping.IsConfigured() {
		return services
	}
	allowed := mapping.SourcesForPackage(packageID)
	if len(allowed) == 0 {
		logWarn("Package %q matches no package source mapping pattern", packageID)
		return nil
	}
	allowedSet := NewSet[string]()
	for _, k := range allowed {
		allowedSet.Add(strings.ToLower(k))
	}
	var filtered []*NugetService
	for _, svc := range services {
		if allowedSet.Contains(strings.ToLower(svc.SourceName())) {
			filtered = append(filtered, svc)
		}
	}
	if len(filtered) == 0 {
		logWarn("Package %q maps to sources %v but none are available", packageID, allowed)
	}
	return filtered
}
