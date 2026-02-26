package main

import (
	"testing"
)

func TestParseSemVer_Standard(t *testing.T) {
	tests := []struct {
		input              string
		major, minor, patch int
		pre                string
	}{
		// basic 3-part
		{"1.2.3", 1, 2, 3, ""},
		{"0.0.0", 0, 0, 0, ""},
		{"10.20.30", 10, 20, 30, ""},

		// 2-part and 1-part
		{"1.2", 1, 2, 0, ""},
		{"5", 5, 0, 0, ""},

		// 4-part (NuGet style) — 4th segment is ignored
		{"1.2.3.4", 1, 2, 3, ""},
		{"6.0.0.0", 6, 0, 0, ""},

		// pre-release with hyphen
		{"1.0.0-alpha", 1, 0, 0, "alpha"},
		{"1.0.0-beta", 1, 0, 0, "beta"},
		{"1.0.0-beta.1", 1, 0, 0, "beta.1"},
		{"1.0.0-beta.2", 1, 0, 0, "beta.2"},
		{"1.0.0-rc.1", 1, 0, 0, "rc.1"},
		{"2.1.0-preview.3", 2, 1, 0, "preview.3"},

		// build metadata with + (not pre-release per SemVer 2.0.0)
		{"1.0.0+build.123", 1, 0, 0, ""},

		// combined pre-release + build metadata
		{"1.0.0-alpha+build", 1, 0, 0, "alpha"},
		{"1.0.0-beta.1+sha.abc123", 1, 0, 0, "beta.1"},

		// hyphen pre-release with dots
		{"3.0.0-beta.24301.2", 3, 0, 0, "beta.24301.2"},

		// empty string
		{"", 0, 0, 0, ""},

		// large version numbers
		{"100.200.300", 100, 200, 300, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := ParseSemVer(tt.input)
			if v.Major != tt.major {
				t.Errorf("Major: got %d, want %d", v.Major, tt.major)
			}
			if v.Minor != tt.minor {
				t.Errorf("Minor: got %d, want %d", v.Minor, tt.minor)
			}
			if v.Patch != tt.patch {
				t.Errorf("Patch: got %d, want %d", v.Patch, tt.patch)
			}
			if v.PreRelease != tt.pre {
				t.Errorf("PreRelease: got %q, want %q", v.PreRelease, tt.pre)
			}
			if v.Raw != tt.input {
				t.Errorf("Raw: got %q, want %q", v.Raw, tt.input)
			}
		})
	}
}

func TestParseSemVer_PreservesRaw(t *testing.T) {
	inputs := []string{
		"1.2.3-beta.1",
		"6.0.0.0",
		"0.1",
	}
	for _, s := range inputs {
		v := ParseSemVer(s)
		if v.Raw != s {
			t.Errorf("ParseSemVer(%q).Raw = %q, want %q", s, v.Raw, s)
		}
		if v.String() != s {
			t.Errorf("ParseSemVer(%q).String() = %q, want %q", s, v.String(), s)
		}
	}
}

func TestString_OmitsBuildMetadata(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.0.0", "1.0.0"},
		{"1.0.0-beta.1", "1.0.0-beta.1"},
		{"1.0.0+metadata", "1.0.0"},
		{"1.0.0-alpha+build", "1.0.0-alpha"},
		{"1.15.0+02753db24d9685e54db06739eb63183d86eb5b62", "1.15.0"},
		{"2.0.0-beta.1+sha.abc123", "2.0.0-beta.1"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := ParseSemVer(tt.input)
			if v.Raw != tt.input {
				t.Errorf("Raw: got %q, want %q", v.Raw, tt.input)
			}
			if v.String() != tt.want {
				t.Errorf("String(): got %q, want %q", v.String(), tt.want)
			}
		})
	}
}

func TestIsPreRelease(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1.0.0", false},
		{"2.3.4", false},
		{"1.0.0-alpha", true},
		{"1.0.0-beta.1", true},
		{"1.0.0-rc.1", true},
		{"1.0.0-preview.3", true},
		{"1.0.0+build", false}, // build metadata is NOT pre-release per SemVer 2.0.0
		{"1.0.0-alpha+build", true}, // pre-release with build metadata is still pre-release
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := ParseSemVer(tt.input)
			if got := v.IsPreRelease(); got != tt.want {
				t.Errorf("IsPreRelease() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsNewerThan_MajorMinorPatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// major wins
		{"2.0.0", "1.0.0", true},
		{"1.0.0", "2.0.0", false},

		// minor wins when major equal
		{"1.2.0", "1.1.0", true},
		{"1.1.0", "1.2.0", false},

		// patch wins when major.minor equal
		{"1.0.2", "1.0.1", true},
		{"1.0.1", "1.0.2", false},

		// equal versions
		{"1.2.3", "1.2.3", false},
		{"0.0.0", "0.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			a := ParseSemVer(tt.a)
			b := ParseSemVer(tt.b)
			if got := a.IsNewerThan(b); got != tt.want {
				t.Errorf("%s.IsNewerThan(%s) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestIsNewerThan_StableBeatsPreRelease(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// stable > pre-release at same version
		{"1.0.0", "1.0.0-alpha", true},
		{"1.0.0", "1.0.0-beta", true},
		{"1.0.0", "1.0.0-rc.1", true},

		// pre-release < stable at same version
		{"1.0.0-alpha", "1.0.0", false},
		{"1.0.0-rc.1", "1.0.0", false},

		// higher version pre-release > lower version stable
		{"2.0.0-alpha", "1.9.9", true},

		// lower version stable < higher version pre-release
		{"1.9.9", "2.0.0-alpha", false},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			a := ParseSemVer(tt.a)
			b := ParseSemVer(tt.b)
			if got := a.IsNewerThan(b); got != tt.want {
				t.Errorf("%s.IsNewerThan(%s) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestIsNewerThan_PreReleaseOrdering(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// lexicographic: rc > beta > alpha
		{"1.0.0-rc.1", "1.0.0-beta.1", true},
		{"1.0.0-beta", "1.0.0-alpha", true},
		{"1.0.0-alpha", "1.0.0-beta", false},

		// same pre-release tag
		{"1.0.0-beta.1", "1.0.0-beta.1", false},

		// preview vs rc
		{"1.0.0-rc.1", "1.0.0-preview.1", true},
		{"1.0.0-preview.1", "1.0.0-rc.1", false},

		// beta.2 vs beta.1 (numeric identifier compare per SemVer 2.0.0)
		{"1.0.0-beta.2", "1.0.0-beta.1", true},
		{"1.0.0-beta.1", "1.0.0-beta.2", false},

		// numeric identifiers: 11 > 2 (not lexicographic)
		{"1.0.0-beta.11", "1.0.0-beta.2", true},
		{"1.0.0-beta.2", "1.0.0-beta.11", false},

		// more identifiers > fewer when all preceding match
		{"1.0.0-alpha.1", "1.0.0-alpha", true},
		{"1.0.0-alpha", "1.0.0-alpha.1", false},

		// numeric identifiers have lower precedence than alphanumeric
		{"1.0.0-alpha", "1.0.0-1", true},
		{"1.0.0-1", "1.0.0-alpha", false},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			a := ParseSemVer(tt.a)
			b := ParseSemVer(tt.b)
			if got := a.IsNewerThan(b); got != tt.want {
				t.Errorf("%s.IsNewerThan(%s) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestIsNewerThan_FourPartVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// revision (4th segment) participates in comparison
		{"1.2.3.4", "1.2.3.0", true},  // revision 4 > 0
		{"1.2.3.0", "1.2.3.4", false}, // revision 0 < 4
		{"2.0.0.0", "1.0.0.0", true},
		{"1.0.0.0", "1.0.0", false},   // same version (revision defaults to 0)
		{"1.2.3.5", "1.2.3.4", true},  // revision 5 > 4
		{"1.2.3.4", "1.2.3.4", false}, // equal
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			a := ParseSemVer(tt.a)
			b := ParseSemVer(tt.b)
			if got := a.IsNewerThan(b); got != tt.want {
				t.Errorf("%s.IsNewerThan(%s) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestParseSemVer_NuGetRealWorld(t *testing.T) {
	tests := []struct {
		input              string
		major, minor, patch int
		pre                string
	}{
		// Common NuGet packages
		{"8.0.0", 8, 0, 0, ""},
		{"6.0.0-preview.7.21377.19", 6, 0, 0, "preview.7.21377.19"},
		{"5.0.0-rc.2.20475.5", 5, 0, 0, "rc.2.20475.5"},
		{"4.7.2", 4, 7, 2, ""},
		{"13.0.3", 13, 0, 3, ""},

		// Azure SDK style
		{"12.19.0-beta.1", 12, 19, 0, "beta.1"},

		// PolySharp-style SemVer 2.0.0 with build metadata (not pre-release)
		{"1.15.0+02753db24d9685e54db06739eb63183d86eb5b62", 1, 15, 0, ""},

		// NuGet service API type string — not a bare version, but parser doesn't crash
		{"SearchQueryService/3.0.0-beta", 0, 0, 0, "beta"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := ParseSemVer(tt.input)
			if v.Major != tt.major || v.Minor != tt.minor || v.Patch != tt.patch {
				t.Errorf("got %d.%d.%d, want %d.%d.%d",
					v.Major, v.Minor, v.Patch,
					tt.major, tt.minor, tt.patch)
			}
			if v.PreRelease != tt.pre {
				t.Errorf("PreRelease: got %q, want %q", v.PreRelease, tt.pre)
			}
		})
	}
}

func TestIsNewerThan_SortOrder(t *testing.T) {
	// Descending order (newest first). Each entry must be strictly newer
	// than the one below it. Exercises every precedence rule:
	//   major > minor > patch > revision > pre-release
	//   stable > pre-release
	//   pre-release: left-to-right, numeric as int, alpha lexically,
	//                numeric < alpha, more ids > fewer ids
	//   build metadata ignored entirely
	versions := []string{
		// --- major ---
		"4.0.0",
		"3.0.0",

		// --- stable vs pre-release at same version ---
		"3.0.0-rc.2",
		"3.0.0-rc.1",

		// --- numeric pre-release: 11 > 9 > 2 > 1 ---
		"3.0.0-beta.11",
		"3.0.0-beta.9",
		"3.0.0-beta.2",
		"3.0.0-beta.1",

		// --- more identifiers > fewer when prefix matches ---
		"3.0.0-beta",
		"3.0.0-alpha.1",
		"3.0.0-alpha",

		// --- alpha > numeric identifiers ---
		"3.0.0-1",
		"3.0.0-0",

		// --- minor ---
		"2.2.0",
		"2.1.0",

		// --- revision (4-part NuGet) ---
		"2.0.0.10",
		"2.0.0.2",
		"2.0.0.1",

		// --- patch (2.0.0.0 == 2.0.0, tested in equals below) ---
		"2.0.0",
		"1.0.1",
		"1.0.0",

		// --- pre-release with dotted numeric segments ---
		"1.0.0-rc.2.20475.5",
		"1.0.0-rc.2.20475.4",
		"1.0.0-rc.1",
		"1.0.0-preview.7.21377.19",
		"1.0.0-preview.7.21377.18",
		"1.0.0-preview.7.100.1",
		"1.0.0-preview.6",
		"1.0.0-beta.24301.2",
		"1.0.0-beta.24301.1",
		"1.0.0-beta.2",
		"1.0.0-beta.1",
		"1.0.0-beta",
		"1.0.0-alpha.1",
		"1.0.0-alpha",

		// --- numeric only pre-release ---
		"1.0.0-100",
		"1.0.0-2",
		"1.0.0-1",

		// --- bottom ---
		"0.0.1",
		"0.0.0",
	}

	for i := 0; i < len(versions)-1; i++ {
		a := ParseSemVer(versions[i])
		b := ParseSemVer(versions[i+1])
		if !a.IsNewerThan(b) {
			t.Errorf("expected %s > %s", versions[i], versions[i+1])
		}
		if b.IsNewerThan(a) {
			t.Errorf("expected %s < %s", versions[i+1], versions[i])
		}
	}

	// Build metadata MUST be ignored for precedence (SemVer 2.0.0 §10).
	// Every pair here should be considered equal (neither is newer).
	equal := [][2]string{
		// no build vs build
		{"1.0.0", "1.0.0+build"},
		{"1.0.0", "1.0.0+sha.abc123"},
		// different build strings
		{"1.0.0+build1", "1.0.0+build2"},
		{"1.0.0+aaaa", "1.0.0+zzzz"},
		// pre-release with different builds
		{"2.0.0-beta.1+sha.aaa", "2.0.0-beta.1+sha.zzz"},
		{"1.0.0-alpha+build", "1.0.0-alpha+other"},
		// long hash-style build metadata (PolySharp)
		{"1.15.0+02753db24d9685e54db06739eb63183d86eb5b62", "1.15.0+ffffffffffffffffffffffffffffffffffffffff"},
		{"1.15.0", "1.15.0+02753db24d9685e54db06739eb63183d86eb5b62"},
		// revision with build
		{"2.0.0.1+build", "2.0.0.1+other"},
		// 4-part with trailing zero == 3-part
		{"2.0.0.0", "2.0.0"},
		{"1.2.3.0", "1.2.3"},
	}
	for _, pair := range equal {
		a := ParseSemVer(pair[0])
		b := ParseSemVer(pair[1])
		if a.IsNewerThan(b) {
			t.Errorf("expected %s == %s (build metadata ignored), but got >", pair[0], pair[1])
		}
		if b.IsNewerThan(a) {
			t.Errorf("expected %s == %s (build metadata ignored), but got <", pair[0], pair[1])
		}
	}

	// Versions with build metadata must still sort correctly by their
	// core version + pre-release, ignoring the build suffix.
	withBuilds := []string{
		"3.0.0+build.99",
		"3.0.0-rc.1+build.1",
		"3.0.0-beta.11+sha.abc",
		"3.0.0-beta.2+sha.def",
		"3.0.0-alpha+build",
		"2.0.0.5+meta",
		"2.0.0.1+meta",
		"2.0.0+build",
		"1.0.0+build.456",
		"1.0.0-beta.1+sha.xyz",
		"1.0.0-alpha+build.1",
	}
	for i := 0; i < len(withBuilds)-1; i++ {
		a := ParseSemVer(withBuilds[i])
		b := ParseSemVer(withBuilds[i+1])
		if !a.IsNewerThan(b) {
			t.Errorf("expected %s > %s (build metadata present)", withBuilds[i], withBuilds[i+1])
		}
		if b.IsNewerThan(a) {
			t.Errorf("expected %s < %s (build metadata present)", withBuilds[i+1], withBuilds[i])
		}
	}
}

func TestParseSemVer_BuildMetadata(t *testing.T) {
	tests := []struct {
		input string
		pre   string
		build string
	}{
		{"1.0.0+build.123", "", "build.123"},
		{"1.0.0-alpha+build", "alpha", "build"},
		{"1.0.0-beta.1+sha.abc123", "beta.1", "sha.abc123"},
		{"1.15.0+02753db24d9685e54db06739eb63183d86eb5b62", "", "02753db24d9685e54db06739eb63183d86eb5b62"},
		{"1.0.0", "", ""},
		{"1.0.0-rc.1", "rc.1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := ParseSemVer(tt.input)
			if v.PreRelease != tt.pre {
				t.Errorf("PreRelease: got %q, want %q", v.PreRelease, tt.pre)
			}
			if v.Build != tt.build {
				t.Errorf("Build: got %q, want %q", v.Build, tt.build)
			}
		})
	}
}

func TestParseSemVer_Revision(t *testing.T) {
	tests := []struct {
		input    string
		revision int
	}{
		{"1.2.3.4", 4},
		{"6.0.0.0", 0},
		{"1.2.3", 0},
		{"1.0", 0},
		{"10.20.30.40", 40},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			v := ParseSemVer(tt.input)
			if v.Revision != tt.revision {
				t.Errorf("Revision: got %d, want %d", v.Revision, tt.revision)
			}
		})
	}
}

func TestIsNewerThan_BuildMetadataIgnored(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// build metadata should not affect precedence
		{"1.0.0+build1", "1.0.0+build2", false},
		{"1.0.0+build2", "1.0.0+build1", false},
		{"1.0.0+build", "1.0.0", false},
		{"1.0.0", "1.0.0+build", false},
		// pre-release still works with build metadata present
		{"1.0.0", "1.0.0-alpha+build", true},
		{"1.0.0-alpha+build", "1.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			a := ParseSemVer(tt.a)
			b := ParseSemVer(tt.b)
			if got := a.IsNewerThan(b); got != tt.want {
				t.Errorf("%s.IsNewerThan(%s) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
