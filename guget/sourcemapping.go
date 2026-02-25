package main

import "strings"

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
	var matched []string
	for sourceKey, patterns := range m.Entries {
		for _, p := range patterns {
			if matchPattern(packageID, p) {
				matched = append(matched, sourceKey)
				break
			}
		}
	}
	return matched
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

// FilterServices returns services allowed for packageID by the mapping.
// Falls back to all services if mapping is unconfigured or filtering yields nothing.
func FilterServices(services []*NugetService, mapping *PackageSourceMapping, packageID string) []*NugetService {
	if !mapping.IsConfigured() {
		return services
	}
	allowed := mapping.SourcesForPackage(packageID)
	if len(allowed) == 0 {
		logDebug("Package %q matches no source mapping patterns; trying all sources", packageID)
		return services
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
		logDebug("Package %q mapped to sources %v but none are available; trying all sources", packageID, allowed)
		return services
	}
	return filtered
}
