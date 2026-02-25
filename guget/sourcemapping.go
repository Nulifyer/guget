package main

import "strings"

// ─────────────────────────────────────────────
// XML types for <packageSourceMapping>
// ─────────────────────────────────────────────

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

// ─────────────────────────────────────────────
// PackageSourceMapping
// ─────────────────────────────────────────────

// PackageSourceMapping holds the accumulated <packageSourceMapping> rules
// from one or more nuget.config files. Entries maps a source key
// (case-preserved) to the list of lowercase patterns it serves.
type PackageSourceMapping struct {
	Entries map[string][]string // e.g. {"nuget.org": ["*"], "custom_github": ["redacted.*"]}
}

// IsConfigured returns true when at least one mapping entry exists.
// When false, all sources should be tried (legacy behaviour).
func (m *PackageSourceMapping) IsConfigured() bool {
	return m != nil && len(m.Entries) > 0
}

// SourcesForPackage returns the source keys that are allowed to serve
// the given packageID. Returns nil when the mapping is not configured
// (meaning "allow all sources").
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

// ─────────────────────────────────────────────
// Pattern matching
// ─────────────────────────────────────────────

// matchPattern checks whether packageID matches a single NuGet source-mapping
// pattern. Rules (all case-insensitive):
//   - "*"         → matches every package ID
//   - "Prefix.*"  → matches IDs starting with "prefix."
//   - "Exact.Name" → exact string match
func matchPattern(packageID, pattern string) bool {
	id := strings.ToLower(packageID)
	pat := strings.ToLower(pattern)

	if pat == "*" {
		return true
	}
	if strings.HasSuffix(pat, ".*") {
		prefix := pat[:len(pat)-1] // includes the trailing dot: "prefix."
		return strings.HasPrefix(id, prefix)
	}
	return id == pat
}

// ─────────────────────────────────────────────
// Service filtering
// ─────────────────────────────────────────────

// FilterServices returns the subset of services whose source name is
// allowed for packageID according to the mapping. If the mapping is not
// configured, or if filtering would leave zero services, all services
// are returned (graceful degradation).
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
