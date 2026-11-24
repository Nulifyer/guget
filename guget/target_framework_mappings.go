package main

import (
	"strconv"
	"strings"
)

// NuGet framework tables, ported from NuGet.Frameworks
// (FrameworkConstants.cs, DefaultFrameworkMappings.cs,
// DefaultPortableFrameworkMappings.cs, NuGetFrameworkFactory.cs) and the
// lookups FrameworkNameProvider.cs and FrameworkExpander.cs build on them.

const (
	fwNetCoreApp       = ".NETCoreApp"
	fwNetStandardApp   = ".NETStandardApp"
	fwNetStandard      = ".NETStandard"
	fwNetPlatform      = ".NETPlatform"
	fwNet              = ".NETFramework"
	fwNetCore          = ".NETCore"
	fwWinRT            = "WinRT"
	fwNetMicro         = ".NETMicroFramework"
	fwPortable         = ".NETPortable"
	fwWindowsPhone     = "WindowsPhone"
	fwWindows          = "Windows"
	fwWindowsPhoneApp  = "WindowsPhoneApp"
	fwDnx              = "DNX"
	fwDnxCore          = "DNXCore"
	fwAspNet           = "ASP.NET"
	fwAspNetCore       = "ASP.NETCore"
	fwSilverlight      = "Silverlight"
	fwNative           = "native"
	fwMonoAndroid      = "MonoAndroid"
	fwMonoTouch        = "MonoTouch"
	fwMonoMac          = "MonoMac"
	fwXamarinIOs       = "Xamarin.iOS"
	fwXamarinMac       = "Xamarin.Mac"
	fwXamarinPS3       = "Xamarin.PlayStation3"
	fwXamarinPS4       = "Xamarin.PlayStation4"
	fwXamarinPSVita    = "Xamarin.PlayStationVita"
	fwXamarinWatchOS   = "Xamarin.WatchOS"
	fwXamarinTVOS      = "Xamarin.TVOS"
	fwXamarinXbox360   = "Xamarin.Xbox360"
	fwXamarinXboxOne   = "Xamarin.XboxOne"
	fwUAP              = "UAP"
	fwTizen            = "Tizen"
	fwNanoFramework    = ".NETnanoFramework"
	frameworkMaxMajor  = 2147483647
	portableProfileTag = "Profile"
)

// FrameworkConstants.CommonFrameworks used by the tables.
var (
	fwNet11      = fw(fwNet, 1, 1)
	fwNet2       = fw(fwNet, 2)
	fwNet35      = fw(fwNet, 3, 5)
	fwNet4       = fw(fwNet, 4)
	fwNet403     = fw(fwNet, 4, 0, 3)
	fwNet45      = fw(fwNet, 4, 5)
	fwNet451     = fw(fwNet, 4, 5, 1)
	fwNet452     = fw(fwNet, 4, 5, 2)
	fwNet46      = fw(fwNet, 4, 6)
	fwNet461     = fw(fwNet, 4, 6, 1)
	fwNet462     = fw(fwNet, 4, 6, 2)
	fwNet463     = fw(fwNet, 4, 6, 3)
	fwNet47      = fw(fwNet, 4, 7)
	fwNet471     = fw(fwNet, 4, 7, 1)
	fwNet472     = fw(fwNet, 4, 7, 2)
	fwNet48      = fw(fwNet, 4, 8)
	fwNet481     = fw(fwNet, 4, 8, 1)
	fwNetCore45  = fw(fwNetCore, 4, 5)
	fwNetCore451 = fw(fwNetCore, 4, 5, 1)
	fwNetCore50  = fw(fwNetCore, 5)
	fwWin8       = fw(fwWindows, 8)
	fwWin81      = fw(fwWindows, 8, 1)
	fwSL4        = fw(fwSilverlight, 4)
	fwSL5        = fw(fwSilverlight, 5)
	fwWP7        = fw(fwWindowsPhone, 7)
	fwWP75       = fw(fwWindowsPhone, 7, 5)
	fwWP8        = fw(fwWindowsPhone, 8)
	fwWP81       = fw(fwWindowsPhone, 8, 1)
	fwWPA81      = fw(fwWindowsPhoneApp, 8, 1)
	fwTizen3     = fw(fwTizen, 3)
	fwTizen4     = fw(fwTizen, 4)
	fwTizen6     = fw(fwTizen, 6)
	fwAspNet0    = fw(fwAspNet)
	fwAspNetC0   = fw(fwAspNetCore)
	fwAspNet50   = fw(fwAspNet, 5)
	fwAspNetC50  = fw(fwAspNetCore, 5)
	fwDnx0       = fw(fwDnx)
	fwDnx45      = fw(fwDnx, 4, 5)
	fwDnxCore0   = fw(fwDnxCore)
	fwDnxCore50  = fw(fwDnxCore, 5)
	fwDotNet     = fw(fwNetPlatform)
	fwDotNet50   = fw(fwNetPlatform, 5)
	fwDotNet51   = fw(fwNetPlatform, 5, 1)
	fwDotNet52   = fw(fwNetPlatform, 5, 2)
	fwDotNet53   = fw(fwNetPlatform, 5, 3)
	fwDotNet54   = fw(fwNetPlatform, 5, 4)
	fwDotNet55   = fw(fwNetPlatform, 5, 5)
	fwDotNet56   = fw(fwNetPlatform, 5, 6)
	fwNS         = fw(fwNetStandard)
	fwNS10       = fw(fwNetStandard, 1, 0)
	fwNS11       = fw(fwNetStandard, 1, 1)
	fwNS12       = fw(fwNetStandard, 1, 2)
	fwNS13       = fw(fwNetStandard, 1, 3)
	fwNS14       = fw(fwNetStandard, 1, 4)
	fwNS15       = fw(fwNetStandard, 1, 5)
	fwNS16       = fw(fwNetStandard, 1, 6)
	fwNS17       = fw(fwNetStandard, 1, 7)
	fwNS20       = fw(fwNetStandard, 2, 0)
	fwNS21       = fw(fwNetStandard, 2, 1)
	fwUAP10      = fw(fwUAP, 10)
	fwCoreApp10  = fw(fwNetCoreApp, 1, 0)
	fwCoreApp11  = fw(fwNetCoreApp, 1, 1)
	fwCoreApp20  = fw(fwNetCoreApp, 2, 0)
	fwCoreApp21  = fw(fwNetCoreApp, 2, 1)
	fwCoreApp22  = fw(fwNetCoreApp, 2, 2)
	fwCoreApp30  = fw(fwNetCoreApp, 3, 0)
	fwCoreApp31  = fw(fwNetCoreApp, 3, 1)
)

// commonFrameworks ports NuGetFramework.TryParseCommonFramework.
var commonFrameworks = func() map[string]TargetFramework {
	m := map[string]TargetFramework{}
	add := func(f TargetFramework, names ...string) {
		for _, name := range names {
			m[name] = f
		}
	}
	add(fwDotNet50, "dotnet", "dotnet50", "dotnet5.0")
	add(fwNet4, "net40", "net4")
	add(fwNet403, "net403")
	add(fwNet45, "net45")
	add(fwNet451, "net451")
	add(fwNet452, "net452")
	add(fwNet46, "net46")
	add(fwNet461, "net461")
	add(fwNet462, "net462")
	add(fwNet463, "net463")
	add(fwNet47, "net47")
	add(fwNet471, "net471")
	add(fwNet472, "net472")
	add(fwNet48, "net48")
	add(fwNet481, "net481")
	add(fwWin8, "win8")
	add(fwWin81, "win81")
	add(fwNS, "netstandard")
	add(fwNS10, "netstandard1.0", "netstandard10")
	add(fwNS11, "netstandard1.1", "netstandard11")
	add(fwNS12, "netstandard1.2", "netstandard12")
	add(fwNS13, "netstandard1.3", "netstandard13")
	add(fwNS14, "netstandard1.4", "netstandard14")
	add(fwNS15, "netstandard1.5", "netstandard15")
	add(fwNS16, "netstandard1.6", "netstandard16")
	add(fwNS17, "netstandard1.7", "netstandard17")
	add(fwNS20, "netstandard2.0", "netstandard20")
	add(fwNS21, "netstandard2.1", "netstandard21")
	add(fwCoreApp10, "netcoreapp1.0")
	add(fwCoreApp11, "netcoreapp1.1")
	add(fwCoreApp20, "netcoreapp2.0")
	add(fwCoreApp21, "netcoreapp2.1", "netcoreapp21")
	add(fwCoreApp22, "netcoreapp2.2")
	add(fwCoreApp30, "netcoreapp3.0", "netcoreapp30")
	add(fwCoreApp31, "netcoreapp3.1", "netcoreapp31")
	add(fw(fwNetCoreApp, 5), "netcoreapp5.0", "netcoreapp50", "net5.0", "net50")
	add(fw(fwNetCoreApp, 6), "netcoreapp6.0", "netcoreapp60", "net6.0", "net60")
	add(fw(fwNetCoreApp, 7), "netcoreapp7.0", "netcoreapp70", "net7.0", "net70")
	add(fw(fwNetCoreApp, 8), "netcoreapp8.0", "netcoreapp80", "net8.0", "net80")
	add(fw(fwNetCoreApp, 9), "net9.0")
	add(fw(fwNetCoreApp, 10), "net10.0")
	add(fw(fwNetCoreApp, 11), "net11.0")
	return m
}()

// deprecatedFrameworks ports NuGetFramework.TryParseDeprecatedFramework
// (bare versions in old lib folders).
var deprecatedFrameworks = map[string]TargetFramework{
	"45": fwNet45, "4.5": fwNet45,
	"40": fwNet4, "4.0": fwNet4, "4": fwNet4,
	"35": fwNet35, "3.5": fwNet35,
	"20": fwNet2, "2": fwNet2, "2.0": fwNet2,
}

type frameworkRange struct{ min, max TargetFramework }

func (r frameworkRange) satisfies(f TargetFramework) bool {
	return strings.EqualFold(r.min.Framework, f.Framework) && strings.EqualFold(r.min.Profile, f.Profile) &&
		r.min.Version.compare(f.Version) <= 0 && r.max.Version.compare(f.Version) >= 0
}

type compatibilityMapping struct{ target, supported frameworkRange }

func fwMax(framework string) TargetFramework { return fw(framework, frameworkMaxMajor) }

func standardMapping(f, netStandard TargetFramework) compatibilityMapping {
	return compatibilityMapping{frameworkRange{f, fwMax(f.Framework)}, frameworkRange{fwNS10, netStandard}}
}

func generationAndStandardMapping(f, netPlatform, netStandard TargetFramework) []compatibilityMapping {
	return []compatibilityMapping{
		{frameworkRange{f, fwMax(f.Framework)}, frameworkRange{fwDotNet, netPlatform}},
		standardMapping(f, netStandard),
	}
}

type frameworkNameProvider struct {
	synonyms           map[string]string // lower identifier or short name → identifier
	identifiers        map[string]string // lower identifier → identifier
	profileShortToLong map[string]string
	profileLongs       map[string]string
	equivalent         map[frameworkKey][]TargetFramework
	equivalentKeys     []TargetFramework
	equivalentProfiles map[string]map[string][]string
	compatibility      map[string][]compatibilityMapping
	subsets            map[string][]string
	portable           map[int][]TargetFramework
	portableOrder      []int
	portableOptional   map[int][]TargetFramework
	portableCompat     map[int][]frameworkRange
}

var frameworkNames = newFrameworkNameProvider()

func newFrameworkNameProvider() *frameworkNameProvider {
	p := &frameworkNameProvider{
		synonyms: map[string]string{}, identifiers: map[string]string{},
		profileShortToLong: map[string]string{}, profileLongs: map[string]string{},
		equivalent: map[frameworkKey][]TargetFramework{}, equivalentProfiles: map[string]map[string][]string{},
		compatibility: map[string][]compatibilityMapping{}, subsets: map[string][]string{},
		portable: map[int][]TargetFramework{}, portableOptional: map[int][]TargetFramework{}, portableCompat: map[int][]frameworkRange{},
	}

	// Equivalent profiles are added before frameworks, as in InitMappings.
	for _, m := range [][3]string{
		{fwNet, "Client", ""}, {fwNet, "Full", ""},
		{fwSilverlight, "WindowsPhone71", "WindowsPhone"}, {fwWindowsPhone, "WindowsPhone71", "WindowsPhone"},
	} {
		profiles := p.equivalentProfiles[strings.ToLower(m[0])]
		if profiles == nil {
			profiles = map[string][]string{}
			p.equivalentProfiles[strings.ToLower(m[0])] = profiles
		}
		profiles[strings.ToLower(m[1])] = append(profiles[strings.ToLower(m[1])], m[2])
		profiles[strings.ToLower(m[2])] = append(profiles[strings.ToLower(m[2])], m[1])
	}

	for _, pair := range [][2]TargetFramework{
		{fw(fwUAP), fwUAP10},
		{fw(fwWindows), fwWin8},
		{fwWin8, fwNetCore45},
		{fwNetCore45, fw(fwWinRT, 4, 5)},
		{fw(fwNetCore), fwNetCore45},
		{fw(fwWinRT), fw(fwWinRT, 4, 5)},
		{fwWin81, fwNetCore451},
		{fw(fwWindowsPhone), fwWP7},
		{fwWP7, fwProfile(fwSilverlight, "WindowsPhone", 3)},
		{fw(fwWindowsPhone, 7, 1), fwProfile(fwSilverlight, "WindowsPhone71", 4)},
		{fwWP8, fwProfile(fwSilverlight, "WindowsPhone", 8)},
		{fwWP81, fwProfile(fwSilverlight, "WindowsPhone", 8, 1)},
		{fw(fwWindowsPhoneApp), fwWPA81},
		{fw(fwTizen), fwTizen3},
		{fwDnx0, fwDnx45},
		{fwDnxCore0, fwDnxCore50},
		{fwDotNet, fwDotNet50},
		{fwAspNet0, fwAspNet50},
		{fwAspNetC0, fwAspNetC50},
		{fwDnx45, fwAspNet50},
		{fwDnxCore50, fwAspNetC50},
	} {
		p.addEquivalentFrameworks(pair[0], pair[1])
	}

	for _, s := range [][2]string{
		{"NETFramework", fwNet}, {".NET", fwNet}, {"NETCore", fwNetCore}, {"NETPortable", fwPortable},
		{"asp.net", fwAspNet}, {"asp.netcore", fwAspNetCore},
		{"Xamarin.PlayStationThree", fwXamarinPS3}, {"XamarinPlayStationThree", fwXamarinPS3},
		{"Xamarin.PlayStationFour", fwXamarinPS4}, {"XamarinPlayStationFour", fwXamarinPS4},
		{"XamarinPlayStationVita", fwXamarinPSVita},
	} {
		p.synonyms[strings.ToLower(s[0])] = s[1]
	}
	for _, s := range [][2]string{
		{fwNetCoreApp, "netcoreapp"}, {fwNetStandardApp, "netstandardapp"}, {fwNetStandard, "netstandard"},
		{fwNetPlatform, "dotnet"}, {fwNet, "net"}, {fwNetMicro, "netmf"}, {fwSilverlight, "sl"},
		{fwPortable, "portable"}, {fwWindowsPhone, "wp"}, {fwWindowsPhoneApp, "wpa"}, {fwWindows, "win"},
		{fwAspNet, "aspnet"}, {fwAspNetCore, "aspnetcore"}, {fwNative, "native"}, {fwMonoAndroid, "monoandroid"},
		{fwMonoTouch, "monotouch"}, {fwMonoMac, "monomac"}, {fwXamarinIOs, "xamarinios"}, {fwXamarinMac, "xamarinmac"},
		{fwXamarinPS3, "xamarinpsthree"}, {fwXamarinPS4, "xamarinpsfour"}, {fwXamarinPSVita, "xamarinpsvita"},
		{fwXamarinWatchOS, "xamarinwatchos"}, {fwXamarinTVOS, "xamarintvos"}, {fwXamarinXbox360, "xamarinxboxthreesixty"},
		{fwXamarinXboxOne, "xamarinxboxone"}, {fwDnx, "dnx"}, {fwDnxCore, "dnxcore"}, {fwNetCore, "netcore"},
		{fwWinRT, "winrt"}, {fwUAP, "uap"}, {fwTizen, "tizen"}, {fwNanoFramework, "netnano"},
	} {
		if _, exists := p.synonyms[s[1]]; !exists {
			p.synonyms[s[1]] = s[0]
		}
		p.identifiers[strings.ToLower(s[0])] = s[0]
	}
	for _, s := range [][2]string{
		{"Client", "Client"}, {"CF", "CompactFramework"}, {"Full", ""}, {"WP", "WindowsPhone"}, {"WP71", "WindowsPhone71"},
	} {
		p.profileShortToLong[strings.ToLower(s[0])] = s[1]
		p.profileLongs[strings.ToLower(s[1])] = s[1]
	}

	mappings := []compatibilityMapping{
		{frameworkRange{fw(fwUAP), fwMax(fwUAP)}, frameworkRange{fw(fwWindows), fw(fwWindows, 8, 1)}},
		{frameworkRange{fw(fwUAP), fwMax(fwUAP)}, frameworkRange{fw(fwWindowsPhoneApp), fw(fwWindowsPhoneApp, 8, 1)}},
		{frameworkRange{fw(fwUAP), fwMax(fwUAP)}, frameworkRange{fwNetCore50, fwNetCore50}},
		{frameworkRange{fw(fwWindows), fwMax(fwWindows)}, frameworkRange{fw(fwWinRT), fw(fwWinRT, 4, 5)}},
		standardMapping(fwTizen3, fwNS16),
		standardMapping(fwTizen4, fwNS20),
		standardMapping(fwTizen6, fwNS21),
		standardMapping(fw(fwUAP, 10, 0, 15064), fwNS20),
		standardMapping(fwCoreApp10, fwNS16),
		standardMapping(fwCoreApp11, fwNS17),
		standardMapping(fwCoreApp20, fwNS20),
		standardMapping(fwCoreApp30, fwNS21),
		standardMapping(fwNet463, fwNS20),
	}
	for _, group := range [][]compatibilityMapping{
		generationAndStandardMapping(fw(fwDnxCore), fwDotNet56, fwNS15),
		generationAndStandardMapping(fw(fwUAP), fwDotNet55, fwNS14),
		generationAndStandardMapping(fwNetCore50, fwDotNet55, fwNS14),
		generationAndStandardMapping(fwWPA81, fwDotNet53, fwNS12),
		generationAndStandardMapping(fwWP8, fwDotNet51, fwNS10),
		generationAndStandardMapping(fwNet45, fwDotNet52, fwNS11),
		generationAndStandardMapping(fwNet451, fwDotNet53, fwNS12),
		generationAndStandardMapping(fwNet46, fwDotNet54, fwNS13),
		generationAndStandardMapping(fwNet461, fwDotNet55, fwNS20),
		generationAndStandardMapping(fwNet462, fwDotNet56, fwNS20),
		generationAndStandardMapping(fwNetCore45, fwDotNet52, fwNS11),
		generationAndStandardMapping(fwNetCore451, fwDotNet53, fwNS12),
		generationAndStandardMapping(fw(fwMonoAndroid), fwDotNet56, fwNS21),
		generationAndStandardMapping(fw(fwMonoMac), fwDotNet56, fwNS21),
		generationAndStandardMapping(fw(fwMonoTouch), fwDotNet56, fwNS21),
		generationAndStandardMapping(fw(fwXamarinIOs), fwDotNet56, fwNS21),
		generationAndStandardMapping(fw(fwXamarinMac), fwDotNet56, fwNS21),
		generationAndStandardMapping(fw(fwXamarinPS3), fwDotNet56, fwNS20),
		generationAndStandardMapping(fw(fwXamarinPS4), fwDotNet56, fwNS20),
		generationAndStandardMapping(fw(fwXamarinPSVita), fwDotNet56, fwNS20),
		generationAndStandardMapping(fw(fwXamarinXbox360), fwDotNet56, fwNS20),
		generationAndStandardMapping(fw(fwXamarinXboxOne), fwDotNet56, fwNS20),
		generationAndStandardMapping(fw(fwXamarinTVOS), fwDotNet56, fwNS21),
		generationAndStandardMapping(fw(fwXamarinWatchOS), fwDotNet56, fwNS21),
	} {
		mappings = append(mappings, group...)
	}
	for _, m := range mappings {
		id := strings.ToLower(m.target.min.Framework)
		p.compatibility[id] = append(p.compatibility[id], m)
	}

	for _, s := range [][2]string{{fwNet, fwDnx}, {fwNetPlatform, fwDnxCore}, {fwNetStandard, fwNetStandardApp}} {
		id := strings.ToLower(s[1])
		p.subsets[id] = append(p.subsets[id], s[0])
	}

	for _, profile := range []struct {
		number     int
		frameworks []TargetFramework
	}{
		// v4.6
		{31, []TargetFramework{fwWin81, fwWP81}},
		{32, []TargetFramework{fwWin81, fwWPA81}},
		{44, []TargetFramework{fwNet451, fwWin81}},
		{84, []TargetFramework{fwWP81, fwWPA81}},
		{151, []TargetFramework{fwNet451, fwWin81, fwWPA81}},
		{157, []TargetFramework{fwWin81, fwWP81, fwWPA81}},
		// v4.5
		{7, []TargetFramework{fwNet45, fwWin8}},
		{49, []TargetFramework{fwNet45, fwWP8}},
		{78, []TargetFramework{fwNet45, fwWin8, fwWP8}},
		{111, []TargetFramework{fwNet45, fwWin8, fwWPA81}},
		{259, []TargetFramework{fwNet45, fwWin8, fwWPA81, fwWP8}},
		// v4.0
		{2, []TargetFramework{fwNet4, fwWin8, fwSL4, fwWP7}},
		{3, []TargetFramework{fwNet4, fwSL4}},
		{4, []TargetFramework{fwNet45, fwSL4, fwWin8, fwWP7}},
		{5, []TargetFramework{fwNet4, fwWin8}},
		{6, []TargetFramework{fwNet403, fwWin8}},
		{14, []TargetFramework{fwNet4, fwSL5}},
		{18, []TargetFramework{fwNet403, fwSL4}},
		{19, []TargetFramework{fwNet403, fwSL5}},
		{23, []TargetFramework{fwNet45, fwSL4}},
		{24, []TargetFramework{fwNet45, fwSL5}},
		{36, []TargetFramework{fwNet4, fwSL4, fwWin8, fwWP8}},
		{37, []TargetFramework{fwNet4, fwSL5, fwWin8}},
		{41, []TargetFramework{fwNet403, fwSL4, fwWin8}},
		{42, []TargetFramework{fwNet403, fwSL5, fwWin8}},
		{46, []TargetFramework{fwNet45, fwSL4, fwWin8}},
		{47, []TargetFramework{fwNet45, fwSL5, fwWin8}},
		{88, []TargetFramework{fwNet4, fwSL4, fwWin8, fwWP75}},
		{92, []TargetFramework{fwNet4, fwWin8, fwWPA81}},
		{95, []TargetFramework{fwNet403, fwSL4, fwWin8, fwWP7}},
		{96, []TargetFramework{fwNet403, fwSL4, fwWin8, fwWP75}},
		{102, []TargetFramework{fwNet403, fwWin8, fwWPA81}},
		{104, []TargetFramework{fwNet45, fwSL4, fwWin8, fwWP75}},
		{136, []TargetFramework{fwNet4, fwSL5, fwWin8, fwWP8}},
		{143, []TargetFramework{fwNet403, fwSL4, fwWin8, fwWP8}},
		{147, []TargetFramework{fwNet403, fwSL5, fwWin8, fwWP8}},
		{154, []TargetFramework{fwNet45, fwSL4, fwWin8, fwWP8}},
		{158, []TargetFramework{fwNet45, fwSL5, fwWin8, fwWP8}},
		{225, []TargetFramework{fwNet4, fwSL5, fwWin8, fwWPA81}},
		{240, []TargetFramework{fwNet403, fwSL5, fwWin8, fwWPA81}},
		{255, []TargetFramework{fwNet45, fwSL5, fwWin8, fwWPA81}},
		{328, []TargetFramework{fwNet4, fwSL5, fwWin8, fwWPA81, fwWP8}},
		{336, []TargetFramework{fwNet403, fwSL5, fwWin8, fwWPA81, fwWP8}},
		{344, []TargetFramework{fwNet45, fwSL5, fwWin8, fwWPA81, fwWP8}},
	} {
		p.portable[profile.number] = profile.frameworks
		p.portableOrder = append(p.portableOrder, profile.number)
	}
	mono := []TargetFramework{fw(fwMonoAndroid), fw(fwMonoTouch), fw(fwXamarinIOs), fw(fwXamarinMac), fw(fwXamarinWatchOS), fw(fwXamarinTVOS)}
	for _, number := range []int{5, 6, 7, 14, 19, 24, 37, 42, 44, 47, 49, 78, 92, 102, 111, 136, 147, 151, 158, 225, 255, 259, 328, 336, 344} {
		p.portableOptional[number] = mono
	}
	for _, m := range []struct {
		number      int
		netStandard TargetFramework
	}{
		{7, fwNS11}, {31, fwNS10}, {32, fwNS12}, {44, fwNS12}, {49, fwNS10}, {78, fwNS10},
		{84, fwNS10}, {111, fwNS11}, {151, fwNS12}, {157, fwNS10}, {259, fwNS10},
	} {
		p.portableCompat[m.number] = append(p.portableCompat[m.number], frameworkRange{fwNS10, m.netStandard})
	}
	return p
}

// addEquivalentFrameworks ports FrameworkNameProvider.AddEquivalentFrameworks:
// every framework in the connected group becomes equivalent to every other.
func (p *frameworkNameProvider) addEquivalentFrameworks(a, b TargetFramework) {
	stack := []TargetFramework{a, b}
	var seen []TargetFramework
	seenKeys := map[frameworkKey]bool{}
	for len(stack) > 0 {
		next := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seenKeys[next.key()] {
			continue
		}
		seenKeys[next.key()] = true
		seen = append(seen, next)
		if existing, ok := p.equivalent[next.key()]; ok {
			stack = append(stack, existing...)
		} else {
			p.equivalent[next.key()] = nil
			p.equivalentKeys = append(p.equivalentKeys, next)
		}
	}
	for _, f := range seen {
		for _, other := range seen {
			if !sameFramework(f, other) {
				p.equivalent[f.key()] = appendFramework(p.equivalent[f.key()], other)
			}
		}
	}
}

func appendFramework(list []TargetFramework, f TargetFramework) []TargetFramework {
	for _, existing := range list {
		if sameFramework(existing, f) {
			return list
		}
	}
	return append(list, f)
}

// identifier ports FrameworkNameProvider.TryGetIdentifier.
func (p *frameworkNameProvider) identifier(s string) (string, bool) {
	lower := strings.ToLower(s)
	if id, ok := p.synonyms[lower]; ok {
		return id, true
	}
	id, ok := p.identifiers[lower]
	return id, ok
}

// profile ports FrameworkNameProvider.TryGetProfile.
func (p *frameworkNameProvider) profile(short string) (string, bool) {
	lower := strings.ToLower(short)
	if long, ok := p.profileShortToLong[lower]; ok {
		return long, true
	}
	long, ok := p.profileLongs[lower]
	return long, ok
}

// equivalentFrameworks ports TryGetEquivalentFrameworks(NuGetFramework).
func (p *frameworkNameProvider) equivalentFrameworks(f TargetFramework) []TargetFramework {
	var result []TargetFramework
	for _, eq := range p.equivalent[f.key()] {
		result = appendFramework(result, eq)
	}
	base := append(append([]TargetFramework(nil), result...), f)
	for _, b := range base {
		for _, profile := range p.equivalentProfiles[strings.ToLower(b.Framework)][strings.ToLower(b.Profile)] {
			result = appendFramework(result, newFramework(b.Framework, b.Version, profile, "", fwVersion{}))
		}
	}
	out := result[:0]
	for _, r := range result {
		if !sameFramework(r, f) {
			out = append(out, r)
		}
	}
	return out
}

// equivalentFrameworksInRange ports TryGetEquivalentFrameworks(FrameworkRange).
func (p *frameworkNameProvider) equivalentFrameworksInRange(r frameworkRange) []TargetFramework {
	var result []TargetFramework
	for _, key := range p.equivalentKeys {
		if r.satisfies(key) {
			for _, eq := range p.equivalentFrameworks(key) {
				result = appendFramework(result, eq)
			}
		}
	}
	return result
}

func (p *frameworkNameProvider) compatibilityMappings(f TargetFramework) []frameworkRange {
	var ranges []frameworkRange
	for _, m := range p.compatibility[strings.ToLower(f.Framework)] {
		if m.target.satisfies(f) {
			ranges = append(ranges, m.supported)
		}
	}
	return ranges
}

// expand ports FrameworkExpander.Expand: every framework a project targeting
// f can also consume.
func (p *frameworkNameProvider) expand(f TargetFramework) []TargetFramework {
	seen := map[frameworkKey]bool{f.key(): true}
	stack := []TargetFramework{f}
	var out []TargetFramework
	for len(stack) > 0 {
		next := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range p.expandOnce(next) {
			if !seen[e.key()] {
				seen[e.key()] = true
				out = append(out, e)
				stack = append(stack, e)
			}
		}
	}
	// PCLs expand to netstandard but not, recursively, to dotnet.
	if f.isPCL() {
		if number, ok := portableProfileNumber(f.Profile); ok {
			for _, r := range p.portableCompat[number] {
				out = append(out, r.min)
				if !sameFramework(r.min, r.max) {
					out = append(out, r.max)
				}
			}
		}
	}
	return out
}

func (p *frameworkNameProvider) expandOnce(f TargetFramework) []TargetFramework {
	out := p.equivalentFrameworks(f)
	out = append(out, p.equivalentFrameworksInRange(frameworkRange{newFramework(f.Framework, fwVersion{}, f.Profile, "", fwVersion{}), f})...)
	if !f.hasProfile() {
		for _, sub := range p.subsets[strings.ToLower(f.Framework)] {
			out = append(out, newFramework(sub, f.Version, f.Profile, "", fwVersion{}))
		}
	}
	for _, r := range p.compatibilityMappings(f) {
		out = append(out, r.min)
		if !sameFramework(r.min, r.max) {
			out = append(out, r.max)
		}
	}
	return out
}

// equivalentClosure ports CompatibilityProvider.GetEquivalentFrameworksClosure.
func (p *frameworkNameProvider) equivalentClosure(f TargetFramework) []TargetFramework {
	seen := map[frameworkKey]bool{f.key(): true}
	stack := []TargetFramework{f}
	var out []TargetFramework
	for len(stack) > 0 {
		next := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, eq := range p.equivalentFrameworks(next) {
			if !seen[eq.key()] {
				seen[eq.key()] = true
				out = append(out, eq)
				stack = append(stack, eq)
			}
		}
	}
	return out
}

func portableProfileNumber(profile string) (int, bool) {
	if len(profile) < len(portableProfileTag) || !strings.EqualFold(profile[:len(portableProfileTag)], portableProfileTag) {
		return -1, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(profile[len(portableProfileTag):]))
	return n, err == nil
}

// portableFrameworks ports TryGetPortableFrameworks(string, bool): a
// "ProfileN" name or a '+'-joined list of short names.
func (p *frameworkNameProvider) portableFrameworks(profile string, includeOptional bool) ([]TargetFramework, bool) {
	if number, ok := portableProfileNumber(profile); ok {
		var result []TargetFramework
		for _, f := range p.portable[number] {
			result = appendFramework(result, f)
		}
		if includeOptional {
			for _, f := range p.portableOptional[number] {
				result = appendFramework(result, f)
			}
		}
		return result, len(result) > 0
	}
	return p.portableFrameworksFromShortNames(profile)
}

// portableFrameworksFromShortNames parses "net45+win8+wpa81". NuGet throws
// when a member has a profile; that is reported as not ok.
func (p *frameworkNameProvider) portableFrameworksFromShortNames(names string) ([]TargetFramework, bool) {
	var result []TargetFramework
	for _, name := range strings.Split(names, "+") {
		if name == "" {
			continue
		}
		f := ParseTargetFramework(name)
		if f.hasProfile() {
			return nil, false
		}
		f.Raw = ""
		result = append(result, f)
	}
	return result, len(result) > 0
}

// portableProfile ports TryGetPortableProfile: the numbered profile whose
// frameworks, or their equivalents, are exactly the given ones after optional
// Xamarin frameworks are set aside.
func (p *frameworkNameProvider) portableProfile(supported []TargetFramework) (int, bool) {
	var deduped []TargetFramework
	existing := map[frameworkKey]bool{}
	for _, f := range supported {
		if existing[f.key()] {
			continue
		}
		deduped = append(deduped, f)
		existing[f.key()] = true
		for _, eq := range p.equivalentClosure(f) {
			existing[eq.key()] = true
		}
	}
	for _, number := range p.portableOrder {
		frameworks := p.portable[number]
		if len(frameworks) > len(deduped) {
			continue
		}
		reduced := map[frameworkKey]bool{}
		for _, f := range deduped {
			optional := false
			for _, o := range p.portableOptional[number] {
				if strings.EqualFold(o.Framework, f.Framework) && strings.EqualFold(o.Profile, f.Profile) && f.Version.compare(o.Version) >= 0 {
					optional = true
				}
			}
			if !optional {
				reduced[f.key()] = true
			}
		}
		if p.someEquivalentPermutationEquals(frameworks, map[frameworkKey]bool{}, reduced) {
			return number, true
		}
	}
	return -1, false
}

// someEquivalentPermutationEquals reports whether replacing each framework by
// itself or one of its equivalents can produce exactly the target set
// (GetEquivalentPermutations followed by SetEquals).
func (p *frameworkNameProvider) someEquivalentPermutationEquals(frameworks []TargetFramework, chosen, target map[frameworkKey]bool) bool {
	if len(frameworks) == 0 {
		if len(chosen) != len(target) {
			return false
		}
		for k := range chosen {
			if !target[k] {
				return false
			}
		}
		return true
	}
	options := append([]TargetFramework{frameworks[0]}, p.equivalent[frameworks[0].key()]...)
	for _, option := range options {
		k := option.key()
		if !target[k] {
			continue
		}
		added := !chosen[k]
		chosen[k] = true
		if p.someEquivalentPermutationEquals(frameworks[1:], chosen, target) {
			return true
		}
		if added {
			delete(chosen, k)
		}
	}
	return false
}
