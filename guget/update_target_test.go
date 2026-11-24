package main

import (
	"strings"
	"testing"
)

// testVersion builds a listed version: "2.0.0" supports everything, a "@"
// suffix lists frameworks ("2.0.0@net8.0,net48"), and "!" marks it unlisted.
func testVersion(spec string) PackageVersion {
	unlisted := strings.HasSuffix(spec, "!")
	spec = strings.TrimSuffix(spec, "!")
	version, frameworks, _ := strings.Cut(spec, "@")
	v := PackageVersion{SemVer: ParseSemVer(version), Unlisted: unlisted}
	if frameworks != "" {
		for _, framework := range strings.Split(frameworks, ",") {
			v.Frameworks = append(v.Frameworks, parsePackageFramework(framework))
		}
	}
	return v
}

func testPackage(specs ...string) *PackageInfo {
	info := &PackageInfo{ID: "Example"}
	for _, spec := range specs {
		info.Versions = append(info.Versions, testVersion(spec))
	}
	sortVersionsDesc(info.Versions)
	return info
}

func testRefs(expressions ...string) []PackageReference {
	var refs []PackageReference
	for _, expression := range expressions {
		refs = append(refs, PackageReference{Name: "Example", Version: requestedVersion(expression), Requested: expression})
	}
	return refs
}

func versionText(v *PackageVersion) string {
	if v == nil {
		return "-"
	}
	return v.SemVer.String()
}

func TestAssessPackageChoosesTheUpdateTarget(t *testing.T) {
	// The issue #14 feed: a stable line, previews of the next major, and a
	// newer major that only supports a newer framework.
	issue := []string{"10.6.0", "10.7.0", "11.0.0-preview.6", "11.0.0-preview.7"}
	cases := []struct {
		name       string
		versions   []string
		refs       []string
		frameworks string
		policy     UpdateTarget
		target     string // update target, "-" for none
		candidate  string
		compat     FrameworkCompatibility
	}{
		{"default stays stable for a stable reference", issue, []string{"10.6.0"}, "net8.0", UpdateTargetCompatibleStable, "10.7.0", "10.7.0", FrameworkCompatible},
		{"default follows a prerelease reference", issue, []string{"11.0.0-preview.6"}, "net8.0", UpdateTargetCompatibleStable, "11.0.0-preview.7", "11.0.0-preview.7", FrameworkCompatible},
		{"compatible counts prereleases", issue, []string{"10.6.0"}, "net8.0", UpdateTargetCompatible, "11.0.0-preview.7", "11.0.0-preview.7", FrameworkCompatible},
		{"stable keeps the prerelease rule", issue, []string{"11.0.0-preview.6"}, "net8.0", UpdateTargetStable, "11.0.0-preview.7", "11.0.0-preview.7", FrameworkCompatible},
		{"stable ignores prereleases for a stable reference", issue, []string{"10.6.0"}, "net8.0", UpdateTargetStable, "10.7.0", "10.7.0", FrameworkCompatible},
		{"a prerelease lower bound counts", issue, []string{"[11.0.0-preview.1,)"}, "net8.0", UpdateTargetCompatibleStable, "11.0.0-preview.7", "11.0.0-preview.7", FrameworkCompatible},
		{"a floating prerelease range restores the newest", issue, []string{"*-*"}, "net8.0", UpdateTargetCompatibleStable, "-", "11.0.0-preview.7", FrameworkCompatible},
		{"up to date", issue, []string{"10.7.0"}, "net8.0", UpdateTargetCompatibleStable, "-", "10.7.0", FrameworkCompatible},
		{"never downgrade a newer prerelease", []string{"1.0.0", "2.0.0-beta"}, []string{"2.0.0-beta"}, "net8.0", UpdateTargetStable, "-", "2.0.0-beta", FrameworkCompatible},
		{"never downgrade below the installed version", []string{"1.0.0", "2.0.0@net9.0"}, []string{"3.0.0"}, "net8.0", UpdateTargetCompatibleStable, "-", "1.0.0", FrameworkCompatible},
		{"incompatible newer version is skipped", []string{"1.0.0", "1.1.0", "2.0.0@net9.0"}, []string{"1.0.0"}, "net8.0", UpdateTargetCompatibleStable, "1.1.0", "1.1.0", FrameworkCompatible},
		{"stable takes an incompatible version", []string{"1.0.0", "2.0.0@net9.0"}, []string{"1.0.0"}, "net8.0", UpdateTargetStable, "2.0.0", "2.0.0", FrameworkIncompatible},
		{"nothing compatible means no target", []string{"1.0.0@net9.0", "2.0.0@net9.0"}, []string{"1.0.0"}, "net8.0", UpdateTargetCompatibleStable, "-", "-", FrameworkIncompatible},
		{"fallback counts as compatible", []string{"1.0.0", "2.0.0@net48"}, []string{"1.0.0"}, "net8.0", UpdateTargetCompatibleStable, "2.0.0", "2.0.0", FrameworkFallback},
		{"unlisted versions are never chosen", []string{"1.0.0", "2.0.0!"}, []string{"1.0.0"}, "net8.0", UpdateTargetCompatibleStable, "-", "1.0.0", FrameworkCompatible},
		{"unparseable project frameworks are skipped", []string{"1.0.0", "2.0.0@net9.0"}, []string{"1.0.0"}, "$(Unknown)", UpdateTargetCompatibleStable, "2.0.0", "2.0.0", FrameworkCompatible},
		{"diverged versions converge without lowering", []string{"1.0.0", "2.0.0", "3.0.0"}, []string{"1.0.0", "3.0.0"}, "net8.0", UpdateTargetCompatibleStable, "3.0.0", "3.0.0", FrameworkCompatible},
		{"diverged versions never lower the newest", []string{"1.0.0", "2.0.0"}, []string{"1.0.0", "3.0.0"}, "net8.0", UpdateTargetCompatibleStable, "-", "2.0.0", FrameworkCompatible},
		{"diverged versions converge on the newest prerelease", []string{"1.0.0", "2.0.0", "3.0.0-beta"}, []string{"1.0.0", "3.0.0-beta"}, "net8.0", UpdateTargetCompatibleStable, "3.0.0-beta", "3.0.0-beta", FrameworkCompatible},
		{"diverged prerelease rule follows the newest reference", []string{"1.0.0", "2.0.0", "3.0.0-beta", "3.0.0-rc"}, []string{"1.0.0", "3.0.0-beta"}, "net8.0", UpdateTargetCompatibleStable, "3.0.0-rc", "3.0.0-rc", FrameworkCompatible},
		{"no reference: adding takes the candidate", issue, nil, "net8.0", UpdateTargetCompatibleStable, "10.7.0", "10.7.0", FrameworkCompatible},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			a := assessPackage(testPackage(test.versions...), testRefs(test.refs...), targetsOf(test.frameworks), test.policy)
			if got := versionText(a.Target); got != test.target {
				t.Fatalf("target = %s, want %s", got, test.target)
			}
			if got := versionText(a.Candidate); got != test.candidate {
				t.Fatalf("candidate = %s, want %s", got, test.candidate)
			}
			if a.Candidate != nil && a.CandidateCompatibility != test.compat {
				t.Fatalf("compatibility = %v, want %v", a.CandidateCompatibility, test.compat)
			}
		})
	}
}

func TestAssessPackageReportsLatestVersions(t *testing.T) {
	a := assessPackage(testPackage("1.0.0", "2.0.0", "3.0.0-beta", "4.0.0!"), testRefs("1.0.0"), targetsOf("net8.0"), UpdateTargetCompatibleStable)
	if versionText(a.LatestStable) != "2.0.0" || versionText(a.LatestVersion) != "3.0.0-beta" {
		t.Fatalf("latest stable %s, latest %s", versionText(a.LatestStable), versionText(a.LatestVersion))
	}
	if a.Prerelease || a.Policy != UpdateTargetCompatibleStable {
		t.Fatalf("assessment = %+v", a)
	}
}

func TestAssessPackageResolvesFloatingAndRestoredVersions(t *testing.T) {
	info := testPackage("1.0.0", "1.5.0", "2.0.0")
	// "1.*" restores 1.5.0, which is what an update compares with.
	if a := assessPackage(info, testRefs("1.*"), nil, UpdateTargetCompatibleStable); versionText(a.Target) != "2.0.0" || len(a.Installed) != 1 || a.Installed[0].String() != "1.5.0" {
		t.Fatalf("target %s installed %v", versionText(a.Target), a.Installed)
	}
	// A restored version newer than the lower bound is the comparison point.
	restored := PackageReference{Name: "Example", Requested: "1.0.0", Version: ParseSemVer("2.0.0")}
	if a := assessPackage(info, []PackageReference{restored}, nil, UpdateTargetCompatibleStable); a.Target != nil {
		t.Fatalf("restored 2.0.0 is up to date; target %s", versionText(a.Target))
	}
	// No lower bound: any listed version is an update.
	if a := assessPackage(info, testRefs("(,3.0.0)"), nil, UpdateTargetCompatibleStable); versionText(a.Target) != "2.0.0" {
		t.Fatalf("target %s", versionText(a.Target))
	}
}

func TestAssessPackageMatchesInstalledVersionsByNuGetEquality(t *testing.T) {
	info := testPackage("1.0.0", "1.0.1", "2.0.0")
	info.Versions[2].Vulnerabilities = []PackageVulnerability{{AdvisoryURL: "https://advisory.example/1", Severity: 2}}
	info.Versions[1].Deprecation = &PackageDeprecation{Reasons: []string{"Legacy"}, AlternatePackageID: "Other"}
	info.Versions[0].Deprecation = &PackageDeprecation{Reasons: []string{"CriticalBugs"}}

	// "1.0" equals 1.0.0: its advisory applies, 2.0.0's deprecation does not.
	a := assessPackage(info, testRefs("1.0"), nil, UpdateTargetCompatibleStable)
	if !a.Vulnerable() || len(a.Advisories) != 1 || a.Advisories[0].SeverityLabel() != "high" {
		t.Fatalf("advisories = %+v", a.Advisories)
	}
	if a.Deprecated() {
		t.Fatalf("1.0.0 is not deprecated: %+v", a.Deprecations)
	}
	a = assessPackage(info, testRefs("1.0.1"), nil, UpdateTargetCompatibleStable)
	if a.Vulnerable() || !a.Deprecated() || a.Deprecations[0].AlternatePackageID != "Other" {
		t.Fatalf("assessment = %+v", a)
	}
	// Diverged: both installed versions count.
	a = assessPackage(info, testRefs("1.0.0", "1.0.1"), nil, UpdateTargetCompatibleStable)
	if !a.Vulnerable() || !a.Deprecated() || len(a.Installed) != 2 {
		t.Fatalf("assessment = %+v", a)
	}
}

func TestAssessPackageWithoutMetadataHasNoTarget(t *testing.T) {
	a := assessPackage(nil, testRefs("1.0.0"), nil, "bogus")
	if a.Target != nil || a.Candidate != nil || a.Policy != UpdateTargetCompatibleStable {
		t.Fatalf("assessment = %+v", a)
	}
}

func TestParseUpdateTarget(t *testing.T) {
	for _, value := range []string{"compatible-stable", "Compatible", " stable "} {
		if _, ok := parseUpdateTarget(value); !ok {
			t.Fatalf("%q rejected", value)
		}
	}
	if _, ok := parseUpdateTarget("latest"); ok {
		t.Fatal("unknown policy accepted")
	}
	if updateTargetOrDefault("") != UpdateTargetCompatibleStable {
		t.Fatal("empty policy is not the default")
	}
}
