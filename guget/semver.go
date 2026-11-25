package main

import (
	"encoding/xml"
	"strconv"
	"strings"
	"unicode"
)

// SemVer is a NuGet package version with NuGet.Versioning.NuGetVersion
// parsing and ordering: one to four numeric parts (missing parts are zero),
// dot-separated release labels compared case-insensitively, and build
// metadata that never affects precedence.
//
// A string NuGet rejects ("abc", "1.2.3.4.5", "1.*") produces a SemVer whose
// IsValid reports false. Its Raw keeps the input so it can still be shown,
// and it never compares newer than, older than, or equal to anything.
type SemVer struct {
	Major      int
	Minor      int
	Patch      int
	Revision   int    // 4th segment for NuGet-style versions (e.g. 1.2.3.4)
	PreRelease string // release labels joined by '.', e.g. "beta.1"
	Build      string // build metadata after '+', ignored for precedence
	Raw        string // original text, without spaces
	invalid    bool
}

// ParseSemVer parses a single NuGet version (NuGetVersion.TryParse). Version
// ranges and floating versions are not versions; use ParseVersionRange or
// requestedVersion for project version expressions.
func ParseSemVer(s string) SemVer {
	if v, ok := parseNuGetVersion(s); ok {
		return v
	}
	return SemVer{Raw: s, invalid: true}
}

// IsValid reports whether the version parsed under NuGet's rules.
func (v SemVer) IsValid() bool { return !v.invalid }

// parseNuGetVersion ports NuGetVersion.TryParse
// (NuGet.Versioning/NuGetVersionFactory.cs).
func parseNuGetVersion(value string) (SemVer, bool) {
	versionString, labels, hasLabels, metadata, hasMetadata := parseVersionSections(strings.TrimSpace(value))
	if versionString == "" {
		return SemVer{}, false
	}
	parts, ok := parseNormalizedVersion(versionString)
	if !ok {
		return SemVer{}, false
	}
	if hasLabels {
		for _, label := range labels {
			if !isValidVersionPart(label, false) {
				return SemVer{}, false
			}
		}
	}
	if hasMetadata {
		for _, part := range strings.Split(metadata, ".") {
			if !isValidVersionPart(part, true) {
				return SemVer{}, false
			}
		}
	}
	return SemVer{
		Major:      parts[0],
		Minor:      parts[1],
		Patch:      parts[2],
		Revision:   parts[3],
		PreRelease: strings.Join(labels, "."),
		Build:      metadata,
		Raw:        strings.ReplaceAll(value, " ", ""),
	}, true
}

// parseVersionSections ports SemanticVersion.ParseSections: it splits on the
// first '-' (release labels) and the first '+' after it (metadata).
func parseVersionSections(value string) (version string, labels []string, hasLabels bool, metadata string, hasMetadata bool) {
	versionEnd, plus := -1, -1
	for i := 0; i < len(value); i++ {
		end := i == len(value)-1
		switch {
		case versionEnd < 0:
			if end || value[i] == '-' || value[i] == '+' {
				endPos := i
				if end {
					endPos++
				}
				version = value[:endPos]
				versionEnd = i
				if value[i] == '+' {
					plus = i
				}
			}
		case plus < 0:
			if end || value[i] == '+' {
				endPos := i
				if end {
					endPos++
				}
				labels = strings.Split(value[versionEnd+1:endPos], ".")
				hasLabels = true
				plus = i
			}
		case end:
			metadata = value[plus+1:]
			hasMetadata = true
		}
	}
	return version, labels, hasLabels, metadata, hasMetadata
}

// parseNormalizedVersion ports NuGetVersion.TryGetNormalizedVersion: up to
// four dot-separated non-negative int32 sections, whitespace around each
// number allowed, and nothing left over.
func parseNormalizedVersion(s string) ([4]int, bool) {
	var parts [4]int
	if strings.TrimSpace(s) == "" {
		return parts, false
	}
	str := []rune(s)
	pos := 0
	for i := range parts {
		next, n, ok := parseVersionSection(str, pos)
		if !ok {
			return parts, false
		}
		parts[i], pos = n, next
	}
	return parts, pos == len(str)
}

func parseVersionSection(str []rune, start int) (int, int, bool) {
	if start == len(str) {
		return start, 0, true
	}
	end := start
	for ; end < len(str); end++ {
		if !unicode.IsSpace(str[end]) {
			if str[end] >= '0' && str[end] <= '9' {
				break
			}
			return end, 0, false
		}
	}
	done, digitFound := false, false
	var number int64
	for ; end < len(str); end++ {
		c := str[end]
		if c >= '0' && c <= '9' {
			digitFound = true
			number = number*10 + int64(c-'0')
			if number > 2147483647 {
				return end, 0, false
			}
		} else if c == '.' {
			end++
			if end == len(str) {
				return end, 0, false
			}
			done = true
			break
		} else if unicode.IsSpace(c) {
			break
		} else {
			return end, 0, false
		}
	}
	if !digitFound {
		return end, 0, false
	}
	if end == len(str) {
		done = true
	}
	if !done {
		for ; end < len(str); end++ {
			if c := str[end]; !unicode.IsSpace(c) {
				if c != '.' {
					return end, 0, false
				}
				end++
				if end == len(str) {
					return end, 0, false
				}
				break
			}
		}
	}
	return end, int(number), true
}

// isValidVersionPart ports SemanticVersion.IsValidPart: non-empty,
// [0-9A-Za-z-] only, and no leading zero on an all-digit part unless allowed.
func isValidVersionPart(s string, allowLeadingZeros bool) bool {
	if s == "" {
		return false
	}
	if !allowLeadingZeros && len(s) > 1 && s[0] == '0' && strings.Trim(s[1:], "0123456789") == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-') {
			return false
		}
	}
	return true
}

// Compare orders versions as NuGet's VersionComparer.VersionRelease does:
// numeric parts, then stable after prerelease, then release labels; build
// metadata is ignored. Invalid versions sort below valid ones (by Raw) so a
// sort stays total; use IsNewerThan and Equal for decisions.
func (v SemVer) Compare(other SemVer) int {
	if v.invalid || other.invalid {
		switch {
		case v.invalid && other.invalid:
			return strings.Compare(v.Raw, other.Raw)
		case v.invalid:
			return -1
		default:
			return 1
		}
	}
	for _, pair := range [][2]int{{v.Major, other.Major}, {v.Minor, other.Minor}, {v.Patch, other.Patch}, {v.Revision, other.Revision}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.PreRelease == "" && other.PreRelease == "":
		return 0
	case v.PreRelease == "":
		return 1
	case other.PreRelease == "":
		return -1
	}
	return comparePreRelease(v.PreRelease, other.PreRelease)
}

// IsNewerThan returns true if v is strictly newer than other under NuGet
// ordering. It is false when either version is invalid.
func (v SemVer) IsNewerThan(other SemVer) bool {
	return v.IsValid() && other.IsValid() && v.Compare(other) > 0
}

// Equal reports NuGet version equality: 1.0 == 1.0.0 == 1.0.0.0, labels
// compare case-insensitively, metadata is ignored. Invalid versions are never
// equal to anything.
func (v SemVer) Equal(other SemVer) bool {
	return v.IsValid() && other.IsValid() && v.Compare(other) == 0
}

// comparePreRelease ports VersionComparer.CompareReleaseLabels: labels left to
// right, int32 labels numerically and before alphanumeric ones, alphanumeric
// labels ordinal ignoring case, and more labels sort higher.
func comparePreRelease(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < max(len(ap), len(bp)); i++ {
		if i >= len(ap) {
			return -1
		}
		if i >= len(bp) {
			return 1
		}
		if result := compareReleaseLabel(ap[i], bp[i]); result != 0 {
			return result
		}
	}
	return 0
}

func compareReleaseLabel(a, b string) int {
	an, aErr := strconv.ParseInt(a, 10, 32)
	bn, bErr := strconv.ParseInt(b, 10, 32)
	switch {
	case aErr == nil && bErr == nil:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return 0
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}
	return strings.Compare(strings.ToUpper(a), strings.ToUpper(b))
}

func (v SemVer) IsPreRelease() bool { return v.IsValid() && v.PreRelease != "" }

// String returns the version as written, without build metadata. An invalid
// version returns its input unchanged.
func (v SemVer) String() string {
	if v.Build != "" {
		if cut := len(v.Raw) - len(v.Build) - 1; cut > 0 && cut < len(v.Raw) {
			return v.Raw[:cut]
		}
	}
	return v.Raw
}

// Normalized returns NuGet's normalized form (ToNormalizedString):
// Major.Minor.Patch, the revision only when non-zero, then release labels.
// An invalid version returns its input unchanged.
func (v SemVer) Normalized() string {
	if v.invalid {
		return v.Raw
	}
	return string(v.appendNormalized(make([]byte, 0, 24)))
}

// appendNormalized appends Normalized without fmt, which row assessment
// calls for every reference on each refresh.
func (v SemVer) appendNormalized(b []byte) []byte {
	b = strconv.AppendInt(b, int64(v.Major), 10)
	b = append(b, '.')
	b = strconv.AppendInt(b, int64(v.Minor), 10)
	b = append(b, '.')
	b = strconv.AppendInt(b, int64(v.Patch), 10)
	if v.Revision > 0 {
		b = append(b, '.')
		b = strconv.AppendInt(b, int64(v.Revision), 10)
	}
	if v.PreRelease != "" {
		b = append(b, '-')
		b = append(b, v.PreRelease...)
	}
	return b
}

// versionKey identifies a version for maps and sets: versions NuGet treats as
// equal share a key, and invalid versions never share one with a valid version.
func (v SemVer) versionKey() string {
	if v.invalid {
		return "\x00" + v.Raw
	}
	b := v.appendNormalized(make([]byte, 0, 24))
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		} else if c >= 0x80 {
			return strings.ToLower(string(b))
		}
	}
	return string(b)
}

func (s *SemVer) UnmarshalXMLAttr(attr xml.Attr) error {
	*s = ParseSemVer(attr.Value)
	return nil
}
