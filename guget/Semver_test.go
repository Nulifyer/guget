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

		// build metadata with +
		{"1.0.0+build.123", 1, 0, 0, "build.123"},

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
		"1.0.0+metadata",
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
		{"1.0.0+build", true}, // + also triggers pre-release
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

		// beta.2 vs beta.1 (lexicographic string compare)
		{"1.0.0-beta.2", "1.0.0-beta.1", true},
		{"1.0.0-beta.1", "1.0.0-beta.2", false},
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
		// 4th segment is ignored; only major.minor.patch matter
		{"1.2.3.4", "1.2.3.0", false}, // both parse to 1.2.3
		{"1.2.3.0", "1.2.3.4", false},
		{"2.0.0.0", "1.0.0.0", true},
		{"1.0.0.0", "1.0.0", false}, // same version
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
	// These versions should sort in descending order (newest first).
	versions := []string{
		"3.0.0",
		"3.0.0-rc.2",
		"3.0.0-rc.1",
		"3.0.0-beta.2",
		"3.0.0-beta.1",
		"3.0.0-alpha",
		"2.1.0",
		"2.0.0",
		"1.0.0",
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
}
