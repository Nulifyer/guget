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
	PreRelease string // e.g. "beta.1", "rc.2"
	Raw        string
}

func ParseSemVer(s string) SemVer {
	raw := s
	pre := ""
	if idx := strings.IndexAny(s, "-+"); idx != -1 {
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
		PreRelease: pre,
		Raw:        raw,
	}
}

// IsNewerThan returns true if v is strictly newer than other.
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
	// Stable > pre-release
	if v.PreRelease == "" && other.PreRelease != "" {
		return true
	}
	if v.PreRelease != "" && other.PreRelease == "" {
		return false
	}
	return v.PreRelease > other.PreRelease
}

func (v SemVer) IsPreRelease() bool { return v.PreRelease != "" }
func (v SemVer) String() string     { return v.Raw }

func (s *SemVer) UnmarshalXMLAttr(attr xml.Attr) error {
	*s = ParseSemVer(attr.Value)
	return nil
}
