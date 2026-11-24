package main

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// dotNetFrameworkName mirrors NuGetFramework.DotNetFrameworkName for tests.
func dotNetFrameworkName(f TargetFramework) string {
	if !f.isSpecificFramework() {
		return f.Framework + ",Version=v0.0"
	}
	version, profile := f.Version, f.Profile
	if sameFramework(f, fwDotNet) { // FullNameReplacements: dotnet → dotnet5.0
		version = fwDotNet50.Version
	}
	s := f.Framework + ",Version=v" + versionDisplay(version)
	if profile != "" {
		s += ",Profile=" + profile
	}
	return s
}

func dotNetPlatformName(f TargetFramework) string {
	if f.Platform == "" {
		return ""
	}
	return f.Platform + ",Version=" + versionDisplay(f.PlatformVersion)
}

func TestFrameworkCompatibilityMatchesNuGetTable(t *testing.T) {
	for _, tc := range nugetCompatibilityTable {
		project, pkg := ParseTargetFramework(tc.project), ParseTargetFramework(tc.pkg)
		if got := project.IsCompatibleWith(pkg); got != tc.want {
			t.Errorf("%s:%d IsCompatible(%q, %q) = %v, want %v (parsed %+v / %+v)", tc.source, tc.line, tc.project, tc.pkg, got, tc.want, project, pkg)
		}
	}
}

func TestFrameworkParseMatchesNuGetTable(t *testing.T) {
	for _, tc := range nugetFrameworkParseTable {
		f := ParseTargetFramework(tc.input)
		switch tc.kind {
		case "moniker":
			if got := dotNetFrameworkName(f); !strings.EqualFold(got, tc.want) {
				t.Errorf("NuGetFrameworkParseTests.cs:%d Parse(%q) = %q, want %q", tc.line, tc.input, got, tc.want)
			}
			if got := dotNetPlatformName(f); got != tc.platform {
				t.Errorf("NuGetFrameworkParseTests.cs:%d Parse(%q) platform = %q, want %q", tc.line, tc.input, got, tc.platform)
			}
		case "unsupported":
			if !f.IsUnsupported() {
				t.Errorf("NuGetFrameworkParseTests.cs:%d Parse(%q) = %+v, want Unsupported", tc.line, tc.input, f)
			}
		case "same":
			if other := ParseTargetFramework(tc.want); !sameFramework(f, other) {
				t.Errorf("NuGetFrameworkParseTests.cs:%d Parse(%q) = %+v, want same as %q (%+v)", tc.line, tc.input, f, tc.want, other)
			}
		case "profile":
			if f.Profile != tc.want {
				t.Errorf("NuGetFrameworkParseTests.cs:%d Parse(%q).Profile = %q, want %q", tc.line, tc.input, f.Profile, tc.want)
			}
		}
	}
}

// Facts from NuGet.Frameworks.Test/CompatibilityTests.cs and
// CompatibilityPCLTests.cs that are not [InlineData] rows.
func TestFrameworkCompatibilityNuGetFacts(t *testing.T) {
	cases := []struct {
		project, pkg string
		want         bool
	}{
		{"UAP10.0", "win", true},                               // Compatibility_UAPWinNonTPM
		{"dnx451", "dnxcore50", false},                         // Compatibility_DnxNoCompat
		{"dnxcore50", "dnx451", false},                         //
		{"dnxcore50", "aspnetcore50", true},                    // Compatibility_DnxAsp
		{"aspnetcore50", "dnxcore50", true},                    //
		{"uap", "dotnet", true},                                // Compatibility_InferredNetPlatform
		{"dotnet", "uap", false},                               //
		{"uap", "netstandard", true},                           // Compatibility_InferredNetStandard
		{"netstandard", "uap", false},                          //
		{"win9", "netcore45", true},                            // Compatibility_InferredIndirect
		{"netcore45", "win9", false},                           //
		{"net45", "net40", true},                               // Compatibility_Basic
		{"sl3", "sl3-wp", false},                               // IsCompatibleReturnsFalseForSlAndWindowsPhoneFrameworks
		{"sl3-wp", "sl3", false},                               //
		{"sl3-wp", "sl4-wp71", false},                          // IsCompatibleWindowsPhoneVersions
		{"sl4-wp71", "sl3-wp", true},                           //
		{"wp8", "sl3-wp", true},                                //
		{"wp8", "sl4-wp71", true},                              //
		{"sl3-wp", "wp8", false},                               //
		{"sl4-wp71", "wp8", false},                             //
		{"wp81", "wp8", true},                                  //
		{"wpa81", "wp81", false},                               //
		{"net40", "net40-client", true},                        // NetFrameworkCompatibilityIsCompatibleReturns
		{"net40-client", "net40", true},                        //
		{"net20", "net40", false},                              // LowerFrameworkVersionsAreNotCompatible...
		{"net40", "net20", true},                               //
		{"net40", "portable-win8+net45", false},                // CompatibilityPCL_NetNeg
		{"portable-win8+net45", "net40", false},                //
		{"net45", "portable-win8+net45", true},                 // CompatibilityPCL_Net
		{"portable-win8+net45", "net45", false},                //
		{"net45", "portable-net45", true},                      // CompatibilityPCL_WithOneTfm
		{"portable-net45", "net45", false},                     //
		{"net45", "portable", false},                           // CompatibilityPCL_ZeroTfmIntoNonPclProject
		{"portable", "net45", false},                           //
		{"portable", "portable", true},                         // CompatibilityPCL_ZeroTfm
		{"portable-net451+win81", "portable-win8+net45", true}, // CompatibilityPCL_Basic
		{"portable-win8+net45", "portable-net451+win81", false},
		{"portable-net45+win8", "portable-win8+net45", true}, // CompatibilityPCL_Same
	}
	for _, tc := range cases {
		if got := ParseTargetFramework(tc.project).IsCompatibleWith(ParseTargetFramework(tc.pkg)); got != tc.want {
			t.Errorf("IsCompatible(%q, %q) = %v, want %v", tc.project, tc.pkg, got, tc.want)
		}
	}
}

// The pairs guget's research probe answered wrongly before the NuGet port,
// plus the netstandard, .NET 5+ and platform rules they exercise.
func TestFrameworkCompatibilityResearchPairs(t *testing.T) {
	cases := []struct {
		project, pkg string
		want         FrameworkCompatibility
	}{
		// .NET Framework short names: net48 is 4.8, net472 is 4.7.2.
		{"net48", "netstandard2.0", FrameworkCompatible},
		{"net461", "netstandard2.0", FrameworkCompatible},
		{"net47", "netstandard2.0", FrameworkCompatible},
		{"net48", "net472", FrameworkCompatible},
		{"net472", "net48", FrameworkIncompatible},
		{"net451", "net46", FrameworkIncompatible},
		{"net46", "net451", FrameworkCompatible},
		{"net40", "netstandard1.0", FrameworkIncompatible},
		{"net403", "net40", FrameworkCompatible},
		{"net35", "net20", FrameworkCompatible},
		{"net11", "net20", FrameworkIncompatible},
		// DefaultFrameworkMappings netstandard support.
		{"net45", "netstandard1.1", FrameworkCompatible},
		{"net45", "netstandard1.2", FrameworkIncompatible},
		{"net45", "netstandard1.3", FrameworkIncompatible},
		{"net451", "netstandard1.2", FrameworkCompatible},
		{"net46", "netstandard1.3", FrameworkCompatible},
		{"net46", "netstandard1.4", FrameworkIncompatible},
		{"net481", "netstandard2.0", FrameworkCompatible},
		{"net481", "netstandard2.1", FrameworkIncompatible},
		{"net462", "netstandard2.1", FrameworkIncompatible},
		{"netcoreapp1.0", "netstandard1.6", FrameworkCompatible},
		{"netcoreapp1.0", "netstandard2.0", FrameworkIncompatible},
		{"netcoreapp1.1", "netstandard1.7", FrameworkCompatible},
		{"netcoreapp2.1", "netstandard2.0", FrameworkCompatible},
		{"netcoreapp2.1", "netstandard2.1", FrameworkIncompatible},
		{"netcoreapp3.0", "netstandard2.1", FrameworkCompatible},
		{"monoandroid90", "netstandard2.1", FrameworkCompatible},
		{"xamarinios10", "netstandard2.1", FrameworkCompatible},
		{"uap10.0.16299", "netstandard2.0", FrameworkCompatible},
		{"uap10.0", "netstandard2.0", FrameworkIncompatible},
		// .NET 5+ is .NETCoreApp >= 5.
		{"net8.0", "netcoreapp3.1", FrameworkCompatible},
		{"net8.0", "net6.0", FrameworkCompatible},
		{"net6.0", "net8.0", FrameworkIncompatible},
		{"net8.0", "netstandard2.1", FrameworkCompatible},
		// AssetTargetFallback: .NET Framework packages restore with NU1701.
		{"net8.0", "net472", FrameworkFallback},
		{"net8.0", "net45", FrameworkFallback},
		{"net8.0", "net481", FrameworkFallback},
		{"netcoreapp2.0", "net461", FrameworkFallback},
		{"netstandard2.0", "net48", FrameworkFallback},
		{"netstandard1.6", "net461", FrameworkIncompatible},
		{"netcoreapp1.1", "net461", FrameworkIncompatible},
		{"net8.0-windows", "net472", FrameworkFallback},
		{"net48", "net8.0", FrameworkIncompatible},
		// Platform TFMs.
		{"net8.0", "net8.0-windows7.0", FrameworkIncompatible},
		{"net8.0", "net8.0-windows", FrameworkIncompatible},
		{"net8.0-windows", "net8.0", FrameworkCompatible},
		{"net8.0-windows10.0.19041", "net8.0-windows7.0", FrameworkCompatible},
		{"net8.0-windows7.0", "net8.0-windows10.0.19041", FrameworkIncompatible},
		{"net8.0-windows10.0.19041", "net6.0-windows10.0.17763", FrameworkCompatible},
		{"net8.0-windows", "net8.0-android", FrameworkIncompatible},
		{"net8.0-android", "monoandroid12.0", FrameworkCompatible},
		{"net8.0-android34.0", "net8.0-android", FrameworkCompatible},
		{"net8.0-android33.0", "net8.0-android34.0", FrameworkIncompatible},
		// The SDK defaults a project's Windows platform version to 7.0; other
		// platforms take a workload default guget cannot see.
		{"net8.0-windows", "net8.0-windows7.0", FrameworkCompatible},
		{"net8.0-windows", "net8.0-windows10.0.19041", FrameworkIncompatible},
		{"net8.0-android", "net8.0-android34.0", FrameworkCompatible},
		{"net8.0-android", "net8.0-ios17.0", FrameworkIncompatible},
		{"net8.0-ios", "xamarinios10", FrameworkIncompatible},
		{"net8.0-ios17.0", "net7.0-ios16.1", FrameworkCompatible},
		{"net8.0-maccatalyst", "netstandard2.0", FrameworkCompatible},
		{"net8.0-tvos", "net8.0", FrameworkCompatible},
		{"net8.0-browser", "net8.0", FrameworkCompatible},
		{"net8.0-browser", "net8.0-windows", FrameworkIncompatible},
		{"net5.0-android", "monoandroid10", FrameworkIncompatible},
		{"net10.0-windows10.0.26100.1", "net10.0-windows10.0.22000.0", FrameworkIncompatible},
		// Profiles and PCLs.
		{"net40-client", "net35", FrameworkCompatible},
		{"net45", "portable-net45+win8+wpa81+wp8", FrameworkCompatible},
		{"netstandard1.0", "portable-net45+win8+wpa81+wp8", FrameworkIncompatible},
		{"net8.0", "portable-net45+win8+wpa81+wp8", FrameworkFallback},
		// Special frameworks.
		{"net8.0", "any", FrameworkCompatible},
		{"net8.0", "unknownfx1.0", FrameworkIncompatible},
	}
	for _, tc := range cases {
		got := ParseTargetFramework(tc.project).CompatibilityWith([]TargetFramework{ParseTargetFramework(tc.pkg)})
		if got != tc.want {
			t.Errorf("%s using %s: %v, want %v", tc.project, tc.pkg, got, tc.want)
		}
	}
}

// AssetTargetFallback applies only when no package framework is compatible
// (NuGet.Frameworks.Test/AssetTargetFallbackTests.cs behaviour).
func TestCompatibilityWithPrefersDirectAssets(t *testing.T) {
	project := ParseTargetFramework("net8.0")
	frameworks := []TargetFramework{ParseTargetFramework("net472"), ParseTargetFramework("netstandard2.0")}
	if got := project.CompatibilityWith(frameworks); got != FrameworkCompatible {
		t.Fatalf("got %v, want compatible through netstandard2.0", got)
	}
	if got := project.CompatibilityWith(nil); got != FrameworkIncompatible {
		t.Fatalf("no frameworks: got %v", got)
	}
}

func TestParseTargetFrameworkShortNames(t *testing.T) {
	cases := []struct {
		input           string
		framework       string
		version         fwVersion
		profile         string
		platform        string
		platformVersion fwVersion
	}{
		{"net48", fwNet, fwVersion{4, 8}, "", "", fwVersion{}},
		{"net472", fwNet, fwVersion{4, 7, 2}, "", "", fwVersion{}},
		{"net403", fwNet, fwVersion{4, 0, 3}, "", "", fwVersion{}},
		{"net35", fwNet, fwVersion{3, 5}, "", "", fwVersion{}},
		{"net20", fwNet, fwVersion{2, 0}, "", "", fwVersion{}},
		{"net11", fwNet, fwVersion{1, 1}, "", "", fwVersion{}},
		{"NET48", fwNet, fwVersion{4, 8}, "", "", fwVersion{}},
		{" net8.0 ", fwNetCoreApp, fwVersion{8}, "", "", fwVersion{}},
		{"net40-client", fwNet, fwVersion{4}, "Client", "", fwVersion{}},
		{".NETFramework4.6.2", fwNet, fwVersion{4, 6, 2}, "", "", fwVersion{}},
		{".NETFramework,Version=v4.7.2", fwNet, fwVersion{4, 7, 2}, "", "", fwVersion{}},
		{".NETStandard2.0", fwNetStandard, fwVersion{2}, "", "", fwVersion{}},
		{".NETCoreApp3.1", fwNetCoreApp, fwVersion{3, 1}, "", "", fwVersion{}},
		{"net5", fwNetCoreApp, fwVersion{5}, "", "", fwVersion{}},
		{"net8.0-windows", fwNetCoreApp, fwVersion{8}, "", "windows", fwVersion{}},
		{"net8.0-windows10.0.19041", fwNetCoreApp, fwVersion{8}, "", "windows", fwVersion{10, 0, 19041}},
		{"net8.0-android34.0", fwNetCoreApp, fwVersion{8}, "", "android", fwVersion{34}},
		{"net9.0-ios18.0", fwNetCoreApp, fwVersion{9}, "", "ios", fwVersion{18}},
		{"net8.0-maccatalyst", fwNetCoreApp, fwVersion{8}, "", "maccatalyst", fwVersion{}},
		{"net8.0-tvos17.0", fwNetCoreApp, fwVersion{8}, "", "tvos", fwVersion{17}},
		{"net8.0-browser", fwNetCoreApp, fwVersion{8}, "", "browser", fwVersion{}},
		{"netcoreapp3.1-windows", fwNetCoreApp, fwVersion{3, 1}, "windows", "", fwVersion{}},
		{"monoandroid12.0", fwMonoAndroid, fwVersion{12}, "", "", fwVersion{}},
		{"xamarinios10", fwXamarinIOs, fwVersion{1, 0}, "", "", fwVersion{}},
		{"Xamarin.iOS1.0", fwXamarinIOs, fwVersion{1, 0}, "", "", fwVersion{}},
		{"MonoAndroid1.0", fwMonoAndroid, fwVersion{1, 0}, "", "", fwVersion{}},
		{"uap10.0.16299", fwUAP, fwVersion{10, 0, 16299}, "", "", fwVersion{}},
		{"tizen40", fwTizen, fwVersion{4}, "", "", fwVersion{}},
		{"sl5", fwSilverlight, fwVersion{5}, "", "", fwVersion{}},
		{"wp8", fwWindowsPhone, fwVersion{8}, "", "", fwVersion{}},
		{"portable-net45+win8+wpa81+wp8", fwPortable, fwVersion{}, "Profile259", "", fwVersion{}},
		{".NETPortable0.0-Profile259", fwPortable, fwVersion{}, "Profile259", "", fwVersion{}},
	}
	for _, tc := range cases {
		f := ParseTargetFramework(tc.input)
		if f.Framework != tc.framework || f.Version != tc.version || f.Profile != tc.profile || f.Platform != tc.platform || f.PlatformVersion != tc.platformVersion {
			t.Errorf("ParseTargetFramework(%q) = %+v", tc.input, f)
		}
		if f.Raw != tc.input || f.String() != tc.input {
			t.Errorf("ParseTargetFramework(%q) lost its text: Raw %q", tc.input, f.Raw)
		}
	}
	for _, input := range []string{"$(TargetFrameworksForLibraries)", "", "foo", "net8.0-windows1.0.0.0.0", "portable-net45-client+win8"} {
		if f := ParseTargetFramework(input); !f.IsUnsupported() {
			t.Errorf("ParseTargetFramework(%q) = %+v, want Unsupported", input, f)
		}
	}
}

func TestParsePackageFrameworkKeepsDisplayForm(t *testing.T) {
	for raw, want := range map[string]string{".NETFramework4.6.2": "net462", ".NETStandard2.0": "netstandard2.0", "net8.0-windows7.0": "net8.0-windows7.0", "": "any"} {
		f := parsePackageFramework(raw)
		if f.String() != want {
			t.Errorf("parsePackageFramework(%q).String() = %q, want %q", raw, f.String(), want)
		}
	}
	if !parsePackageFramework("").isAny() {
		t.Fatal("an empty dependency group framework is NuGet's any framework")
	}
}

func packageVersionFor(version string, frameworks ...string) PackageVersion {
	v := PackageVersion{SemVer: ParseSemVer(version)}
	for _, f := range frameworks {
		v.Frameworks = append(v.Frameworks, parsePackageFramework(f))
	}
	return v
}

func targetsOf(frameworks ...string) Set[TargetFramework] {
	targets := NewSet[TargetFramework]()
	for _, f := range frameworks {
		targets.Add(ParseTargetFramework(f))
	}
	return targets
}

func TestCompatibilityForTargets(t *testing.T) {
	cases := []struct {
		name       string
		version    PackageVersion
		targets    Set[TargetFramework]
		want       FrameworkCompatibility
		compatible bool
	}{
		{"no declared frameworks", packageVersionFor("1.0.0"), targetsOf("net48"), FrameworkCompatible, true},
		{"no targets", packageVersionFor("1.0.0", "net8.0"), targetsOf(), FrameworkCompatible, true},
		{"every target compatible", packageVersionFor("1.0.0", ".NETStandard2.0"), targetsOf("net48", "net8.0"), FrameworkCompatible, true},
		{"one target incompatible", packageVersionFor("1.0.0", "net8.0"), targetsOf("net48", "net8.0"), FrameworkIncompatible, false},
		{"weakest target wins", packageVersionFor("1.0.0", ".NETFramework4.7.2"), targetsOf("net48", "net8.0"), FrameworkFallback, true},
		{"unknown targets are skipped", packageVersionFor("1.0.0", "net8.0"), targetsOf("net8.0", "$(TargetFrameworksForLibraries)"), FrameworkCompatible, true},
		{"only unknown targets", packageVersionFor("1.0.0", "net8.0"), targetsOf("$(TargetFrameworks)"), FrameworkCompatible, true},
		{"platform package needs the platform", packageVersionFor("1.0.0", "net8.0-windows7.0"), targetsOf("net8.0"), FrameworkIncompatible, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.version.compatibilityFor(tc.targets); got != tc.want {
				t.Fatalf("compatibilityFor = %v, want %v", got, tc.want)
			}
			if got := versionCompatible(tc.version, tc.targets); got != tc.compatible {
				t.Fatalf("versionCompatible = %v, want %v", got, tc.compatible)
			}
		})
	}
}

// The picker, its default cursor and the Available column must reach the
// same answer for every combination, including unknown project targets.
func TestPickerCursorAndAvailableAgree(t *testing.T) {
	versions := []PackageVersion{
		packageVersionFor("4.0.0-beta", "net8.0"),
		packageVersionFor("3.0.0", "net9.0"),
		packageVersionFor("2.1.0", ".NETFramework4.7.2"),
		packageVersionFor("2.0.0", ".NETStandard2.0"),
		packageVersionFor("1.0.0"),
	}
	for _, targets := range []Set[TargetFramework]{
		targetsOf("net8.0"), targetsOf("net8.0", "$(Unknown)"), targetsOf("$(Unknown)"),
		targetsOf("net48"), targetsOf("net8.0-windows"), targetsOf("netstandard1.6"), targetsOf(),
	} {
		info := &PackageInfo{Versions: versions}
		latest := info.LatestStableForFramework(targets)
		cursor := defaultVersionCursor(versions, targets, nil, UpdateTargetCompatibleStable)
		if latest == nil {
			t.Fatalf("%v: no compatible version", targets.ToSlice())
		}
		if !versions[cursor].SemVer.Equal(latest.SemVer) {
			t.Errorf("%v: cursor on %s, Available shows %s", targets.ToSlice(), versions[cursor].SemVer, latest.SemVer)
		}
		if !versionCompatible(versions[cursor], targets) {
			t.Errorf("%v: picker marks the default %s incompatible", targets.ToSlice(), versions[cursor].SemVer)
		}
	}
}

func TestFrameworkCompatibilityIsSafeForConcurrentUse(t *testing.T) {
	done := make(chan bool)
	for i := 0; i < 8; i++ {
		go func(i int) {
			project := ParseTargetFramework("net" + strconv.Itoa(5+i%4) + ".0")
			done <- project.IsCompatibleWith(ParseTargetFramework(fmt.Sprintf("netstandard2.%d", i%2)))
		}(i)
	}
	for i := 0; i < 8; i++ {
		if !<-done {
			t.Fatal("net5+ must use netstandard2.x")
		}
	}
}
