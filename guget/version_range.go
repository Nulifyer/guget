package main

import (
	"strings"
)

// This file ports NuGet.Versioning's VersionRange and FloatRange
// (VersionRangeFactory.cs, VersionRange.cs, VersionRangeBase.cs, FloatRange.cs)
// so a PackageReference version expression means what it means to restore.

// FloatBehavior ports NuGetVersionFloatBehavior.
type FloatBehavior int

const (
	FloatNone FloatBehavior = iota
	FloatPrerelease
	FloatRevision
	FloatPatch
	FloatMinor
	FloatMajor
	FloatAbsoluteLatest
	FloatPrereleaseRevision
	FloatPrereleasePatch
	FloatPrereleaseMinor
	FloatPrereleaseMajor
)

// FloatRange is the floating part of a version expression such as "1.*" or
// "2.0.0-beta*".
type FloatRange struct {
	Behavior      FloatBehavior
	Min           SemVer
	ReleasePrefix string
}

func newFloatRange(behavior FloatBehavior, min SemVer, prefix string, hasPrefix bool) FloatRange {
	if !hasPrefix && min.IsPreRelease() {
		prefix = min.PreRelease
	}
	return FloatRange{Behavior: behavior, Min: min, ReleasePrefix: prefix}
}

// IncludePrerelease reports whether the float admits prerelease versions.
func (f FloatRange) IncludePrerelease() bool {
	switch f.Behavior {
	case FloatAbsoluteLatest, FloatPrerelease, FloatPrereleaseMajor, FloatPrereleaseMinor, FloatPrereleasePatch, FloatPrereleaseRevision:
		return true
	}
	return false
}

// Satisfies ports FloatRange.Satisfies.
func (f FloatRange) Satisfies(v SemVer) bool {
	if !v.IsValid() {
		return false
	}
	labelMatches := !v.IsPreRelease() || hasPrefixFold(v.PreRelease, f.ReleasePrefix)
	m := f.Min
	switch f.Behavior {
	case FloatAbsoluteLatest:
		return true
	case FloatMajor:
		return !v.IsPreRelease()
	case FloatPrereleaseRevision:
		return m.Major == v.Major && m.Minor == v.Minor && m.Patch == v.Patch && labelMatches
	case FloatPrereleasePatch:
		return m.Major == v.Major && m.Minor == v.Minor && labelMatches
	case FloatPrereleaseMinor:
		return m.Major == v.Major && labelMatches
	case FloatPrereleaseMajor:
		return labelMatches
	case FloatPrerelease:
		return m.Major == v.Major && m.Minor == v.Minor && m.Patch == v.Patch && m.Revision == v.Revision && labelMatches
	case FloatRevision:
		return m.Major == v.Major && m.Minor == v.Minor && m.Patch == v.Patch && !v.IsPreRelease()
	case FloatPatch:
		return m.Major == v.Major && m.Minor == v.Minor && !v.IsPreRelease()
	case FloatMinor:
		return m.Major == v.Major && !v.IsPreRelease()
	}
	return false
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// parseFloatRange ports FloatRange.TryParse.
func parseFloatRange(s string) (FloatRange, bool) {
	if strings.TrimSpace(s) == "" {
		return FloatRange{}, false
	}
	firstStar := strings.IndexByte(s, '*')
	lastStar := strings.LastIndexByte(s, '*')
	hasPlus := strings.IndexByte(s, '+') >= 0
	switch {
	case len(s) == 1 && firstStar == 0:
		return newFloatRange(FloatMajor, SemVer{Raw: "0.0"}, "", false), true
	case s == "*-*":
		return newFloatRange(FloatAbsoluteLatest, ParseSemVer("0.0.0-0"), "", true), true
	case firstStar != lastStar && lastStar != -1 && !hasPlus:
		// Two stars are allowed only as "<stable>*-<prefix>*".
		dash := strings.IndexByte(s, '-')
		if dash == -1 || lastStar != len(s)-1 || firstStar != dash-1 {
			return FloatRange{}, false
		}
		stable := s[:dash-1] + "0"
		behavior := FloatNone
		switch strings.Count(stable, ".") + 1 {
		case 1:
			behavior = FloatPrereleaseMajor
		case 2:
			behavior = FloatPrereleaseMinor
		case 3:
			behavior = FloatPrereleasePatch
		case 4:
			behavior = FloatPrereleaseRevision
		}
		prefix := s[dash+1 : len(s)-1]
		release := prefix
		if prefix == "" || strings.HasSuffix(prefix, ".") {
			release += "0" // "1.0.*-*": an empty label is not a valid version
		}
		min, ok := parseNuGetVersion(stable + "-" + release)
		if !ok {
			return FloatRange{}, false
		}
		return newFloatRange(behavior, min, prefix, true), true
	case lastStar == len(s)-1 && !hasPlus:
		// A single star, last in the string.
		behavior := FloatNone
		actual := s[:len(s)-1]
		prefix, hasPrefix := "", false
		if strings.IndexByte(s, '-') == -1 {
			actual += "0"
			switch strings.Count(actual, ".") + 1 {
			case 2:
				behavior = FloatMinor
			case 3:
				behavior = FloatPatch
			case 4:
				behavior = FloatRevision
			}
		} else {
			behavior = FloatPrerelease
			if strings.IndexByte(s, '-') == strings.LastIndexByte(s, '-') {
				prefix, hasPrefix = actual[strings.LastIndexByte(s, '-')+1:], true
				if prefix == "" || strings.HasSuffix(actual, ".") {
					actual += "0" // "1.0.0-*": an empty label is not a valid version
				} else if strings.HasSuffix(actual, "-") {
					actual += "-"
				}
			}
		}
		min, ok := parseNuGetVersion(actual)
		if !ok {
			return FloatRange{}, false
		}
		return newFloatRange(behavior, min, prefix, hasPrefix), true
	default:
		min, ok := parseNuGetVersion(s)
		if !ok {
			return FloatRange{}, false
		}
		return newFloatRange(FloatNone, min, "", false), true
	}
}

// VersionRange is a parsed NuGet version expression. A bare version "1.0.0"
// means ">= 1.0.0"; "[1.0.0]" pins exactly; "(1.0.0,)" excludes its lower
// bound; "(,2.0.0]" has none; "1.*" and "*" float.
type VersionRange struct {
	Min, Max               SemVer
	HasMin, HasMax         bool
	IncludeMin, IncludeMax bool
	Float                  FloatRange
	HasFloat               bool
	Original               string
}

// ParseVersionRange ports VersionRange.TryParse with floating allowed.
func ParseVersionRange(value string) (VersionRange, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return VersionRange{}, false
	}
	if trimmed == "*" {
		f, _ := parseFloatRange(trimmed)
		return VersionRange{Min: ParseSemVer("0.0.0"), HasMin: true, IncludeMin: true, IncludeMax: true, Float: f, HasFloat: true, Original: value}, true
	}
	r := VersionRange{Original: value}
	var minText, maxText string
	if trimmed[0] == '(' || trimmed[0] == '[' {
		r.IncludeMin = trimmed[0] == '['
		switch trimmed[len(trimmed)-1] {
		case ']':
			r.IncludeMax = true
		case ')':
		default:
			return VersionRange{}, false
		}
		if len(trimmed) < 2 {
			return VersionRange{}, false
		}
		parts := strings.Split(trimmed[1:len(trimmed)-1], ",")
		if len(parts) > 2 {
			return VersionRange{}, false
		}
		allEmpty := true
		for _, part := range parts {
			if part != "" {
				allEmpty = false
			}
		}
		if allEmpty {
			return VersionRange{}, false
		}
		// (1.0.0] and [1.0.0), (1.0.0) are invalid.
		if len(parts) == 1 && !(r.IncludeMin && r.IncludeMax) {
			return VersionRange{}, false
		}
		minText, maxText = parts[0], parts[0]
		if len(parts) == 2 {
			maxText = parts[1]
		}
	} else {
		r.IncludeMin = true
		minText = trimmed
	}
	if strings.TrimSpace(minText) != "" {
		if strings.Contains(minText, "*") {
			f, ok := parseFloatRange(minText)
			if !ok {
				return VersionRange{}, false
			}
			r.Float, r.HasFloat = f, true
			r.Min = f.Min
		} else {
			v, ok := parseNuGetVersion(minText)
			if !ok {
				return VersionRange{}, false
			}
			r.Min = v
		}
		r.HasMin = true
	}
	if strings.TrimSpace(maxText) != "" {
		v, ok := parseNuGetVersion(maxText) // the upper bound cannot float
		if !ok {
			return VersionRange{}, false
		}
		r.Max, r.HasMax = v, true
	}
	if r.HasMin && r.HasMax {
		switch c := r.Min.Compare(r.Max); {
		case c > 0:
			return VersionRange{}, false
		case c == 0 && r.IncludeMin != r.IncludeMax:
			return VersionRange{}, false
		}
	}
	return r, true
}

// IsFloating reports whether the expression floats ("1.*", "*", "1.0.0-*").
func (r VersionRange) IsFloating() bool { return r.HasFloat && r.Float.Behavior != FloatNone }

// IsMinInclusive reports whether the lower bound exists and is included.
func (r VersionRange) IsMinInclusive() bool { return r.HasMin && r.IncludeMin }

// IsMaxInclusive reports whether the upper bound exists and is included.
func (r VersionRange) IsMaxInclusive() bool { return r.HasMax && r.IncludeMax }

// LowerBound returns the range's minimum version, if it has one. For a float
// this is the float's minimum (1.0.0 for "1.*"), as NuGet's MinVersion is.
func (r VersionRange) LowerBound() (SemVer, bool) { return r.Min, r.HasMin }

// LowerBoundIsPreRelease reports NuGet's MinVersion.IsPrerelease, which
// `dotnet package update` uses to decide whether to consider prereleases.
func (r VersionRange) LowerBoundIsPreRelease() bool { return r.HasMin && r.Min.IsPreRelease() }

// Satisfies ports VersionRangeBase.Satisfies (metadata ignored).
func (r VersionRange) Satisfies(v SemVer) bool {
	if !v.IsValid() {
		return false
	}
	if r.HasMin {
		c := r.Min.Compare(v)
		if c > 0 || (c == 0 && !r.IncludeMin) {
			return false
		}
	}
	if r.HasMax {
		c := r.Max.Compare(v)
		if c < 0 || (c == 0 && !r.IncludeMax) {
			return false
		}
	}
	return true
}

func (r VersionRange) hasPrereleaseBounds() bool {
	return (r.HasMin && r.Min.IsPreRelease()) || (r.HasMax && r.Max.IsPreRelease())
}

// FindBestMatch ports VersionRange.FindBestMatch: the version restore selects
// from the given candidates (lowest in range, or the highest in a float).
func (r VersionRange) FindBestMatch(versions []SemVer) (SemVer, bool) {
	var best SemVer
	found := false
	for _, v := range versions {
		if r.isBetter(best, found, v) {
			best, found = v, true
		}
	}
	return best, found
}

func (r VersionRange) isBetter(current SemVer, hasCurrent bool, considering SemVer) bool {
	if !considering.IsValid() {
		return false
	}
	if !r.hasPrereleaseBounds() && considering.IsPreRelease() && !(r.HasFloat && r.Float.IncludePrerelease()) {
		return false
	}
	if !r.Satisfies(considering) {
		return false
	}
	if !hasCurrent {
		return true
	}
	if r.IsFloating() {
		curIn := r.Float.Satisfies(current)
		conIn := r.Float.Satisfies(considering)
		switch {
		case curIn && !conIn:
			return false
		case conIn && !curIn:
			return true
		case curIn && conIn:
			return current.Compare(considering) < 0
		}
		curLow := current.Compare(r.Float.Min) < 0
		conLow := considering.Compare(r.Float.Min) < 0
		switch {
		case curLow && !conLow:
			return true
		case !curLow && conLow:
			return false
		case !curLow && !conLow:
			return current.Compare(considering) > 0
		default:
			return current.Compare(considering) < 0
		}
	}
	return current.Compare(considering) > 0
}

// requestedVersion returns the version guget treats as current for a
// PackageReference expression. It is known only when restore's choice follows
// from the expression alone once that version exists: a bare version, an
// exact pin, or a non-floating range with an inclusive lower bound. For
// floating, exclusive, or unbounded expressions the result is invalid and its
// Raw is the expression; resolveRequestedVersion can pick it from a feed.
func requestedVersion(expression string) SemVer {
	r, ok := ParseVersionRange(expression)
	if !ok || r.IsFloating() || !r.IsMinInclusive() {
		return SemVer{Raw: strings.TrimSpace(expression), invalid: true}
	}
	return r.Min
}

// resolveRequestedVersion returns the version restore would select for an
// expression from the given candidates, if any satisfies it.
func resolveRequestedVersion(expression string, versions []PackageVersion) (SemVer, bool) {
	r, ok := ParseVersionRange(expression)
	if !ok {
		return SemVer{}, false
	}
	candidates := make([]SemVer, 0, len(versions))
	for _, v := range versions {
		candidates = append(candidates, v.SemVer)
	}
	return r.FindBestMatch(candidates)
}

// requestNamesVersion reports whether a requested expression is exactly v:
// a bare version or an exact [v] pin equal to v under NuGet comparison.
func requestNamesVersion(requested string, v SemVer) bool {
	s := strings.TrimSpace(requested)
	if len(s) > 2 && s[0] == '[' && s[len(s)-1] == ']' && !strings.Contains(s, ",") {
		s = s[1 : len(s)-1]
	}
	return ParseSemVer(s).Equal(v)
}
