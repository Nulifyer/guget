package main

import (
	"encoding/xml"
	"strconv"
	"strings"
)

type SemVer struct {
	Major      int
	Minor      int
	Patch      int
	Revision   int    // 4th segment for NuGet-style versions (e.g. 1.2.3.4)
	PreRelease string // e.g. "beta.1", "rc.2"
	Build      string // build metadata after '+', ignored for precedence
	Raw        string
}

func ParseSemVer(s string) SemVer {
	raw := s
	build := ""
	pre := ""

	// Extract build metadata (after '+') first
	if idx := strings.IndexByte(s, '+'); idx != -1 {
		build = s[idx+1:]
		s = s[:idx]
	}
	// Extract pre-release (after '-')
	if idx := strings.IndexByte(s, '-'); idx != -1 {
		pre = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ".")
	intAt := func(i int) int {
		if i >= len(parts) {
			return 0
		}
		n, _ := strconv.Atoi(parts[i])
		return n
	}
	return SemVer{
		Major:      intAt(0),
		Minor:      intAt(1),
		Patch:      intAt(2),
		Revision:   intAt(3),
		PreRelease: pre,
		Build:      build,
		Raw:        raw,
	}
}

// IsNewerThan returns true if v is strictly newer than other.
// Follows SemVer 2.0.0 precedence rules. Build metadata is ignored.
func (v SemVer) IsNewerThan(other SemVer) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor > other.Minor
	}
	if v.Patch != other.Patch {
		return v.Patch > other.Patch
	}
	if v.Revision != other.Revision {
		return v.Revision > other.Revision
	}
	// Stable > pre-release
	if v.PreRelease == "" && other.PreRelease != "" {
		return true
	}
	if v.PreRelease != "" && other.PreRelease == "" {
		return false
	}
	return comparePreRelease(v.PreRelease, other.PreRelease) > 0
}

// comparePreRelease compares two pre-release strings per SemVer 2.0.0 §11:
// identifiers are compared left-to-right; numeric ids as integers,
// alphanumeric ids lexically; numeric < alphanumeric; fewer fields < more.
func comparePreRelease(a, b string) int {
	if a == b {
		return 0
	}
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	n := len(ap)
	if len(bp) < n {
		n = len(bp)
	}
	for i := 0; i < n; i++ {
		ai, aErr := strconv.Atoi(ap[i])
		bi, bErr := strconv.Atoi(bp[i])
		switch {
		case aErr == nil && bErr == nil: // both numeric
			if ai != bi {
				if ai > bi {
					return 1
				}
				return -1
			}
		case aErr == nil: // a numeric, b alpha → numeric < alpha
			return -1
		case bErr == nil: // a alpha, b numeric → alpha > numeric
			return 1
		default: // both alphanumeric
			if ap[i] != bp[i] {
				if ap[i] > bp[i] {
					return 1
				}
				return -1
			}
		}
	}
	// All compared identifiers equal — more fields = higher precedence
	if len(ap) > len(bp) {
		return 1
	}
	if len(ap) < len(bp) {
		return -1
	}
	return 0
}

func (v SemVer) IsPreRelease() bool { return v.PreRelease != "" }
func (v SemVer) String() string {
	if v.Build != "" {
		return v.Raw[:len(v.Raw)-len(v.Build)-1]
	}
	return v.Raw
}

func (s *SemVer) UnmarshalXMLAttr(attr xml.Attr) error {
	*s = ParseSemVer(attr.Value)
	return nil
}
