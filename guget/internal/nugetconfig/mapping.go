package nugetconfig

import (
	"sort"
	"strings"
)

// Mapping is NuGet package source mapping: which source names may supply a
// package ID.
type Mapping struct {
	entries []mappingEntry
}

type mappingEntry struct {
	source   string
	patterns []string // trimmed, lowercased
}

func (m *Mapping) add(source string, patterns []string) {
	entry := mappingEntry{source: source}
	for _, pattern := range patterns {
		entry.patterns = append(entry.patterns, strings.ToLower(strings.TrimSpace(pattern)))
	}
	m.entries = append(m.entries, entry)
}

func (m *Mapping) configured() bool {
	return m != nil && len(m.entries) > 0
}

// SourcesFor returns the source names allowed to supply packageID, sorted.
// An exact ID match wins; otherwise the longest matching prefix pattern wins.
// A '*' anywhere ends a prefix pattern, so "Contoso*" and "Contoso.*" are both
// prefixes. An empty result means no source may supply the package.
func (m *Mapping) SourcesFor(packageID string) []string {
	if !m.configured() {
		return nil
	}
	id := strings.ToLower(strings.TrimSpace(packageID))
	best := -1
	var matched []string
	for _, entry := range m.entries {
		strength := -1
		for _, pattern := range entry.patterns {
			strength = max(strength, patternStrength(id, pattern))
		}
		switch {
		case strength < 0:
		case strength > best:
			best, matched = strength, []string{entry.source}
		case strength == best:
			matched = append(matched, entry.source)
		}
	}
	sort.Strings(matched)
	return matched
}

// Patterns returns the patterns mapped to a source name, as configured.
func (m *Mapping) Patterns(source string) []string {
	if m == nil {
		return nil
	}
	for _, entry := range m.entries {
		if strings.EqualFold(entry.source, source) {
			return append([]string(nil), entry.patterns...)
		}
	}
	return nil
}

// patternStrength mirrors NuGet's search tree: a pattern that ends exactly at
// the package ID is an exact match; otherwise longer prefixes are stronger.
func patternStrength(id, pattern string) int {
	const exact = 1 << 30
	prefix, isPrefix := pattern, false
	if star := strings.IndexByte(pattern, '*'); star >= 0 {
		prefix, isPrefix = pattern[:star], true
	}
	switch {
	case prefix == id:
		return exact
	case isPrefix && strings.HasPrefix(id, prefix):
		return len(prefix)
	default:
		return -1
	}
}

// Sources returns the mapped source names in configuration order.
func (m *Mapping) Sources() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.entries))
	for _, entry := range m.entries {
		names = append(names, entry.source)
	}
	return names
}
