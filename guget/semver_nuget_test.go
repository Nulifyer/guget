package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func mustVersion(t *testing.T, where, s string) SemVer {
	t.Helper()
	v := ParseSemVer(s)
	if !v.IsValid() {
		t.Fatalf("%s: NuGet parses %q but guget rejected it", where, s)
	}
	return v
}

func mustRange(t *testing.T, where, s string) VersionRange {
	t.Helper()
	r, ok := ParseVersionRange(s)
	if !ok {
		t.Fatalf("%s: NuGet parses range %q but guget rejected it", where, s)
	}
	return r
}

func versionParts(v SemVer) string {
	return fmt.Sprintf("%d.%d.%d.%d", v.Major, v.Minor, v.Patch, v.Revision)
}

func sameBounds(a, b VersionRange) bool {
	return a.HasMin == b.HasMin && a.HasMax == b.HasMax && a.IsMinInclusive() == b.IsMinInclusive() &&
		a.IsMaxInclusive() == b.IsMaxInclusive() && (!a.HasMin || a.Min.Equal(b.Min)) && (!a.HasMax || a.Max.Equal(b.Max))
}

// TestVersioningMatchesNuGetTable interprets nugetVersioningTable, the
// [InlineData] rows of NuGet.Versioning.Test.
func TestVersioningMatchesNuGetTable(t *testing.T) {
	for _, tc := range nugetVersioningTable {
		where := tc.source + ":" + strconv.Itoa(tc.line)
		a := tc.args
		switch tc.kind {
		case "equal":
			x, y := mustVersion(t, where, a[0]), mustVersion(t, where, a[1])
			if !x.Equal(y) || !y.Equal(x) || x.Compare(y) != 0 || x.versionKey() != y.versionKey() {
				t.Errorf("%s: %q and %q should be equal", where, a[0], a[1])
			}
		case "notequal":
			x, y := mustVersion(t, where, a[0]), mustVersion(t, where, a[1])
			if x.Equal(y) || x.versionKey() == y.versionKey() {
				t.Errorf("%s: %q and %q should differ", where, a[0], a[1])
			}
		case "less":
			x, y := mustVersion(t, where, a[0]), mustVersion(t, where, a[1])
			if x.Compare(y) >= 0 || !y.IsNewerThan(x) || x.IsNewerThan(y) {
				t.Errorf("%s: %q should sort before %q", where, a[0], a[1])
			}
		case "valid":
			mustVersion(t, where, a[0])
		case "invalid":
			if v := ParseSemVer(a[0]); v.IsValid() || v.String() != a[0] {
				t.Errorf("%s: %q should be invalid and keep its text, got %+v", where, a[0], v)
			}
		case "parts":
			v := mustVersion(t, where, a[0])
			want := strings.Join(append(strings.Split(a[1], "."), "0", "0", "0")[:4], ".")
			if got := versionParts(v); got != want {
				t.Errorf("%s: %q parts %s, want %s", where, a[0], got, want)
			}
			if len(a) > 2 && v.PreRelease != a[2] {
				t.Errorf("%s: %q release %q, want %q", where, a[0], v.PreRelease, a[2])
			}
			if len(a) == 2 && v.PreRelease != "" {
				t.Errorf("%s: %q release %q, want none", where, a[0], v.PreRelease)
			}
			if len(a) > 3 && v.Build != a[3] {
				t.Errorf("%s: %q metadata %q, want %q", where, a[0], v.Build, a[3])
			}
		case "numbers":
			v := mustVersion(t, where, a[0])
			if got, want := versionParts(v), strings.Join(a[1:], "."); got != want {
				t.Errorf("%s: %q parts %s, want %s", where, a[0], got, want)
			}
		case "normalized":
			if got := mustVersion(t, where, a[0]).Normalized(); got != a[1] {
				t.Errorf("%s: %q normalizes to %q, want %q", where, a[0], got, a[1])
			}
		case "length":
			if got := mustVersion(t, where, a[0]).Normalized(); got != "2.0.0" {
				t.Errorf("%s: %q normalizes to %q", where, a[0], got)
			}
		case "release":
			if got := mustVersion(t, where, a[1]).PreRelease; got != a[0] {
				t.Errorf("%s: %q release %q, want %q", where, a[1], got, a[0])
			}
		case "metadata":
			if got := mustVersion(t, where, a[1]).Build; got != a[0] {
				t.Errorf("%s: %q metadata %q, want %q", where, a[1], got, a[0])
			}
		case "prerelease":
			if got := mustVersion(t, where, a[1]).IsPreRelease(); strconv.FormatBool(got) != a[0] {
				t.Errorf("%s: %q IsPreRelease %v", where, a[1], got)
			}
		case "rangemin":
			if got := mustRange(t, where, a[0]).Min.Normalized(); got != a[1] {
				t.Errorf("%s: %q MinVersion %q, want %q", where, a[0], got, a[1])
			}
		case "floatsatisfies":
			if !mustRange(t, where, "1.0.0-*").Satisfies(mustVersion(t, where, a[0])) {
				t.Errorf("%s: 1.0.0-* should admit %q", where, a[0])
			}
		case "satisfies":
			if !mustRange(t, where, a[0]).Satisfies(mustVersion(t, where, a[1])) {
				t.Errorf("%s: %q should admit %q", where, a[0], a[1])
			}
		case "notsatisfies":
			if mustRange(t, where, a[0]).Satisfies(mustVersion(t, where, a[1])) {
				t.Errorf("%s: %q should not admit %q", where, a[0], a[1])
			}
		case "floatlabels":
			r := mustRange(t, where, a[0])
			if !r.HasFloat || r.Float.Min.PreRelease != a[1] || r.Float.ReleasePrefix != a[2] {
				t.Errorf("%s: %q float %+v, want release %q prefix %q", where, a[0], r.Float, a[1], a[2])
			}
		case "nofloat":
			r := mustRange(t, where, a[0])
			best, ok := r.FindBestMatch([]SemVer{ParseSemVer("1.0.0"), ParseSemVer("1.1.0")})
			if !ok || best.Normalized() != "1.0.0" {
				t.Errorf("%s: %q best match %v %v, want 1.0.0", where, a[0], best, ok)
			}
		case "rangeinvalid":
			if _, ok := ParseVersionRange(a[0]); ok {
				t.Errorf("%s: %q should not parse as a range", where, a[0])
			}
		case "rangevalid":
			mustRange(t, where, a[0])
		case "rangeequiv":
			if r, legacy := mustRange(t, where, a[0]), mustRange(t, where, a[1]); !sameBounds(r, legacy) {
				t.Errorf("%s: %q bounds %+v, want those of %q", where, a[0], r, a[1])
			}
		case "bestmatch":
			var versions []SemVer
			for _, v := range strings.Split(a[0], ";") {
				versions = append(versions, mustVersion(t, where, v))
			}
			best, ok := mustRange(t, where, a[1]).FindBestMatch(versions)
			want := a[2]
			if (want == "<null>") == ok || (ok && best.Normalized() != want) {
				t.Errorf("%s: %q over %s picked %v (%v), want %s", where, a[1], a[0], best, ok, want)
			}
		case "isbetter":
			r := mustRange(t, where, a[0])
			if got := r.isBetter(SemVer{}, false, mustVersion(t, where, a[1])); strconv.FormatBool(got) != a[2] {
				t.Errorf("%s: %q IsBetter(null, %q) = %v", where, a[0], a[1], got)
			}
		case "rangeparts":
			r := mustRange(t, where, a[0])
			hasMin, hasMax := a[1] != "<null>", a[3] != "<null>"
			ok := r.HasMin == hasMin && r.HasMax == hasMax &&
				r.IsMinInclusive() == (hasMin && a[2] == "true") && r.IsMaxInclusive() == (hasMax && a[4] == "true")
			if ok && hasMin {
				ok = r.Min.Equal(mustVersion(t, where, a[1]))
			}
			if ok && hasMax {
				ok = r.Max.Equal(mustVersion(t, where, a[3]))
			}
			if !ok {
				t.Errorf("%s: %q parsed as %+v, want %v", where, a[0], r, a[1:])
			}
		default:
			t.Fatalf("%s: unknown kind %q", where, tc.kind)
		}
	}
}

func TestParseSemVerRejectsWhatNuGetRejects(t *testing.T) {
	for _, s := range []string{"abc", "1.2.3.4.5", "1.*", "*", "[1.0,2.0)", "1.0.0-", "1.0.0-01", "v1.0", ""} {
		v := ParseSemVer(s)
		if v.IsValid() {
			t.Errorf("ParseSemVer(%q) accepted %+v", s, v)
		}
		if v.String() != s || v.Normalized() != s {
			t.Errorf("ParseSemVer(%q) should keep the text for display, got %q", s, v.String())
		}
		if v.IsNewerThan(ParseSemVer("0.0.0")) || ParseSemVer("0.0.0").IsNewerThan(v) || v.Equal(v) || v.IsPreRelease() {
			t.Errorf("ParseSemVer(%q) must not compare to anything", s)
		}
	}
}

func TestSemVerEqualityIsNuGetEquality(t *testing.T) {
	for _, pair := range [][2]string{{"1.0", "1.0.0"}, {"1.0.0", "1.0.0.0"}, {"1.0.0-BETA", "1.0.0-beta"}, {"1.0.0+a", "1.0.0+b"}, {"01.0", "1.0.0"}} {
		a, b := ParseSemVer(pair[0]), ParseSemVer(pair[1])
		if !a.Equal(b) || a.versionKey() != b.versionKey() {
			t.Errorf("%q and %q are the same NuGet version", pair[0], pair[1])
		}
	}
	if ParseSemVer("1.0.0.1").Equal(ParseSemVer("1.0.0")) {
		t.Error("a non-zero revision is part of the version")
	}
}

func TestPreReleaseOrderingIgnoresCase(t *testing.T) {
	order := []string{"1.0.0-1", "1.0.0-2", "1.0.0-10", "1.0.0-Alpha", "1.0.0-alpha.1", "1.0.0-BETA", "1.0.0-beta.2", "1.0.0-beta.10", "1.0.0-RC", "1.0.0"}
	for i := 1; i < len(order); i++ {
		if !ParseSemVer(order[i]).IsNewerThan(ParseSemVer(order[i-1])) {
			t.Errorf("%s should be newer than %s", order[i], order[i-1])
		}
	}
	// Numeric labels beyond int32 compare as text, as NuGet's int.TryParse does.
	if !ParseSemVer("1.0.0-alpha.zzz").IsNewerThan(ParseSemVer("1.0.0-alpha.9999999999")) {
		t.Error("alpha.zzz sorts after alpha.9999999999")
	}
}

func TestVersionRangeExpressions(t *testing.T) {
	cases := []struct {
		expression       string
		floating         bool
		hasMin           bool
		min              string
		minInclusive     bool
		hasMax           bool
		max              string
		minPrerelease    bool
		requestedVersion string // "" when guget cannot know the resolved version
	}{
		{"1.0.0", false, true, "1.0.0", true, false, "", false, "1.0.0"},
		{"1.0", false, true, "1.0.0", true, false, "", false, "1.0"},
		{"[1.0.0]", false, true, "1.0.0", true, true, "1.0.0", false, "1.0.0"},
		{"[1.0.0, 2.0.0)", false, true, "1.0.0", true, true, "2.0.0", false, "1.0.0"},
		{"(1.0.0,)", false, true, "1.0.0", false, false, "", false, ""},
		{"(,2.0.0]", false, false, "", false, true, "2.0.0", false, ""},
		{"1.*", true, true, "1.0.0", true, false, "", false, ""},
		{"*", true, true, "0.0.0", true, false, "", false, ""},
		{"1.0.0-*", true, true, "1.0.0-0", true, false, "", true, ""},
		{"2.0.0-beta.1", false, true, "2.0.0-beta.1", true, false, "", true, "2.0.0-beta.1"},
		{"[2.0.0-rc.1, 3.0.0)", false, true, "2.0.0-rc.1", true, true, "3.0.0", true, "2.0.0-rc.1"},
	}
	for _, tc := range cases {
		r, ok := ParseVersionRange(tc.expression)
		if !ok {
			t.Errorf("%q did not parse", tc.expression)
			continue
		}
		min, hasMin := r.LowerBound()
		if r.IsFloating() != tc.floating || hasMin != tc.hasMin || r.IsMinInclusive() != tc.minInclusive || r.HasMax != tc.hasMax || r.LowerBoundIsPreRelease() != tc.minPrerelease {
			t.Errorf("%q parsed as %+v", tc.expression, r)
		}
		if hasMin && min.Normalized() != tc.min {
			t.Errorf("%q lower bound %s, want %s", tc.expression, min.Normalized(), tc.min)
		}
		if tc.hasMax && r.Max.Normalized() != tc.max {
			t.Errorf("%q upper bound %s, want %s", tc.expression, r.Max.Normalized(), tc.max)
		}
		v := requestedVersion(tc.expression)
		if tc.requestedVersion == "" {
			if v.IsValid() || v.String() != tc.expression {
				t.Errorf("requestedVersion(%q) = %+v, want unknown showing the expression", tc.expression, v)
			}
		} else if !v.IsValid() || v.String() != tc.requestedVersion {
			t.Errorf("requestedVersion(%q) = %+v, want %s", tc.expression, v, tc.requestedVersion)
		}
	}
	for _, bad := range []string{"", "abc", "(1.0.0)", "[2.0,1.0]", "1.2.3.4.5", "[1.0.0, 1.1.*)"} {
		if _, ok := ParseVersionRange(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
		if v := requestedVersion(bad); v.IsValid() {
			t.Errorf("requestedVersion(%q) = %+v, want unknown", bad, v)
		}
	}
}

func TestResolveRequestedVersionUsesRestoreChoice(t *testing.T) {
	var versions []PackageVersion
	for _, v := range []string{"2.0.0", "1.5.0", "1.2.0-beta", "1.0.0"} {
		versions = append(versions, PackageVersion{SemVer: ParseSemVer(v)})
	}
	for expression, want := range map[string]string{"1.*": "1.5.0", "*": "2.0.0", "(1.0.0,)": "1.5.0", "(,2.0.0]": "1.0.0", "[3.0.0,)": ""} {
		got, ok := resolveRequestedVersion(expression, versions)
		if (want == "") == ok || (ok && got.String() != want) {
			t.Errorf("resolve %q = %v %v, want %q", expression, got, ok, want)
		}
	}
}

func TestRequestNamesVersion(t *testing.T) {
	target := ParseSemVer("1.0.0")
	for requested, want := range map[string]bool{"1.0.0": true, "1.0": true, "[1.0]": true, "[1.0.0]": true, "1.0.0.0": true, "1.0.1": false, "[1.0,2.0)": false, "1.*": false, "$(V)": false} {
		if got := requestNamesVersion(requested, target); got != want {
			t.Errorf("requestNamesVersion(%q, 1.0.0) = %v, want %v", requested, got, want)
		}
	}
}

func TestPackageReferenceRequestedRange(t *testing.T) {
	ref := PackageReference{Name: "A", Version: requestedVersion("[1.0.0-beta,)"), Requested: "[1.0.0-beta,)"}
	r, ok := ref.RequestedRange()
	if !ok || !r.LowerBoundIsPreRelease() || r.Min.Normalized() != "1.0.0-beta" {
		t.Fatalf("RequestedRange = %+v %v", r, ok)
	}
	bare := PackageReference{Name: "A", Version: ParseSemVer("2.0.0")}
	if r, ok := bare.RequestedRange(); !ok || !r.Min.Equal(ParseSemVer("2.0")) || r.LowerBoundIsPreRelease() {
		t.Fatalf("RequestedRange without Requested = %+v %v", r, ok)
	}
}
