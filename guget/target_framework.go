package main

import (
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// This file ports NuGet.Frameworks parsing and compatibility
// (NuGetFrameworkFactory.cs, NuGetFramework.cs, FrameworkNameProvider.cs,
// FrameworkExpander.cs, CompatibilityProvider.cs). The tables it reads are in
// target_framework_mappings.go.

// fwVersion is a System.Version normalized to four parts.
type fwVersion [4]int

func (v fwVersion) compare(o fwVersion) int {
	for i := range v {
		if v[i] != o[i] {
			if v[i] < o[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func (v fwVersion) isZero() bool { return v == fwVersion{} }

// TargetFramework is a parsed NuGet framework (NuGetFramework). Raw keeps the
// text it was parsed from for display.
type TargetFramework struct {
	Raw             string
	Framework       string // identifier, e.g. ".NETFramework", ".NETCoreApp"
	Version         fwVersion
	Profile         string
	Platform        string // net5.0+ only, e.g. "windows"
	PlatformVersion fwVersion
}

// newFramework applies the NuGetFramework constructor rule that only net5.0+
// (.NETCoreApp >= 5) frameworks carry a platform.
func newFramework(framework string, version fwVersion, profile, platform string, platformVersion fwVersion) TargetFramework {
	f := TargetFramework{Framework: framework, Version: version, Profile: profile}
	if f.isNet5Era() {
		f.Platform, f.PlatformVersion = platform, platformVersion
	}
	return f
}

func fw(framework string, version ...int) TargetFramework {
	var v fwVersion
	copy(v[:], version)
	return newFramework(framework, v, "", "", fwVersion{})
}

func fwProfile(framework string, profile string, version ...int) TargetFramework {
	f := fw(framework, version...)
	f.Profile = profile
	return f
}

var (
	frameworkAny         = TargetFramework{Framework: "Any"}
	frameworkAgnostic    = TargetFramework{Framework: "Agnostic"}
	frameworkUnsupported = TargetFramework{Framework: "Unsupported"}
)

func (tf TargetFramework) isSpecial(special TargetFramework) bool {
	return strings.EqualFold(tf.Framework, special.Framework) && tf.Version.isZero() && tf.Profile == "" && tf.Platform == "" && tf.PlatformVersion.isZero()
}

// IsUnsupported reports whether NuGet could not parse the framework. Project
// targets are unsupported when they come from MSBuild expressions guget
// cannot evaluate, such as $(TargetFrameworksForLibraries).
func (tf TargetFramework) IsUnsupported() bool { return tf.isSpecial(frameworkUnsupported) }
func (tf TargetFramework) isAny() bool         { return tf.isSpecial(frameworkAny) }
func (tf TargetFramework) isAgnostic() bool    { return tf.isSpecial(frameworkAgnostic) }
func (tf TargetFramework) isSpecificFramework() bool {
	return !tf.isAny() && !tf.isAgnostic() && !tf.IsUnsupported()
}

func (tf TargetFramework) isNet5Era() bool {
	return tf.Version[0] >= 5 && strings.EqualFold(tf.Framework, fwNetCoreApp)
}

func (tf TargetFramework) isPCL() bool {
	return strings.EqualFold(tf.Framework, fwPortable) && tf.Version[0] < 5
}

func (tf TargetFramework) hasProfile() bool  { return tf.Profile != "" }
func (tf TargetFramework) hasPlatform() bool { return tf.Platform != "" }

// frameworkKey is NuGetFramework equality (NuGetFrameworkFullComparer):
// identifier, profile and platform ignore case.
type frameworkKey struct {
	framework, profile, platform string
	version, platformVersion     fwVersion
}

func (tf TargetFramework) key() frameworkKey {
	return frameworkKey{strings.ToLower(tf.Framework), strings.ToLower(tf.Profile), strings.ToLower(tf.Platform), tf.Version, tf.PlatformVersion}
}

// sameFramework is NuGetFramework.Equals. As in NuGet, the parser's single
// Unsupported value equals itself.
func sameFramework(a, b TargetFramework) bool { return a.key() == b.key() }

func (tf TargetFramework) String() string {
	if tf.Raw != "" {
		return tf.Raw
	}
	return tf.Framework + versionDisplay(tf.Version)
}

func versionDisplay(v fwVersion) string {
	s := strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1])
	if v[2] > 0 || v[3] > 0 {
		s += "." + strconv.Itoa(v[2])
		if v[3] > 0 {
			s += "." + strconv.Itoa(v[3])
		}
	}
	return s
}

var parsedFrameworks sync.Map // string → TargetFramework without Raw

// ParseTargetFramework parses a target framework moniker the way
// NuGetFramework.Parse does: short folder names (net48, net8.0-windows10.0.19041,
// portable-net45+win8), identifiers with versions (.NETFramework4.7.2) and
// full names (.NETFramework,Version=v4.7.2). Surrounding whitespace is
// trimmed as MSBuild does for TargetFrameworks items. Anything NuGet rejects
// is Unsupported (see IsUnsupported).
func ParseTargetFramework(raw string) TargetFramework {
	s := strings.TrimSpace(raw)
	if cached, ok := parsedFrameworks.Load(s); ok {
		f := cached.(TargetFramework)
		f.Raw = raw
		return f
	}
	var f TargetFramework
	if strings.Contains(s, ",") {
		f = parseFrameworkName(s)
	} else {
		f = parseFrameworkFolder(s)
	}
	parsedFrameworks.Store(s, f)
	f.Raw = raw
	return f
}

// parsePackageFramework parses a registration dependency group's
// targetFramework. An empty value is NuGet's "any framework" group. Raw keeps
// guget's short display form.
func parsePackageFramework(raw string) TargetFramework {
	if strings.TrimSpace(raw) == "" {
		f := frameworkAny
		f.Raw = "any"
		return f
	}
	f := ParseTargetFramework(raw)
	f.Raw = normFramework(raw)
	return f
}

func parseSpecialFramework(s string) (TargetFramework, bool) {
	for _, special := range []TargetFramework{frameworkAny, frameworkAgnostic, frameworkUnsupported} {
		if strings.EqualFold(s, special.Framework) {
			return special, true
		}
	}
	return TargetFramework{}, false
}

// parseFrameworkFolder ports NuGetFramework.ParseFolder.
func parseFrameworkFolder(folder string) TargetFramework {
	if strings.Contains(folder, "%") {
		if unescaped, err := url.PathUnescape(folder); err == nil {
			folder = unescaped
		}
	}
	if f, ok := parseSpecialFramework(folder); ok {
		return f
	}
	if f, ok := commonFrameworks[strings.ToLower(folder)]; ok {
		return f
	}
	identifier, versionText, hasVersion, profileShort, ok := rawParseFramework(folder)
	if !ok {
		if f, ok := deprecatedFrameworks[folder]; ok {
			return f
		}
		return frameworkUnsupported
	}
	framework, ok := frameworkNames.identifier(identifier)
	if !ok {
		return frameworkUnsupported
	}
	var version fwVersion
	if hasVersion {
		if version, ok = parseShortFrameworkVersion(versionText); !ok {
			return frameworkUnsupported
		}
	}
	if version[0] >= 5 && (strings.EqualFold(framework, fwNet) || strings.EqualFold(framework, fwNetCoreApp)) {
		if profileShort == "" {
			return newFramework(fwNetCoreApp, version, "", "", fwVersion{})
		}
		platformEnd := 0
		for platformEnd < len(profileShort) && isLetterOrDot(profileShort[platformEnd]) {
			platformEnd++
		}
		platform, platformVersionText := profileShort, ""
		if platformEnd > 0 {
			platform, platformVersionText = profileShort[:platformEnd], profileShort[platformEnd:]
		}
		var platformVersion fwVersion
		if platformVersionText != "" {
			if platformVersion, ok = parsePlatformVersion(platformVersionText); !ok {
				return frameworkUnsupported
			}
		}
		return newFramework(fwNetCoreApp, version, "", platform, platformVersion)
	}
	profile, ok := frameworkNames.profile(profileShort)
	if !ok {
		profile = profileShort
	}
	if strings.EqualFold(fwPortable, framework) {
		clients, ok := frameworkNames.portableFrameworksFromShortNames(profileShort)
		if !ok {
			return frameworkUnsupported
		}
		if number, ok := frameworkNames.portableProfile(clients); ok {
			return newFramework(framework, version, "Profile"+strconv.Itoa(number), "", fwVersion{})
		}
		return newFramework(framework, version, profileShort, "", fwVersion{})
	}
	return newFramework(framework, version, profile, "", fwVersion{})
}

// rawParseFramework ports NuGetFramework.RawParse: letters and dots, then
// digits and dots, then an optional "-profile".
func rawParseFramework(s string) (identifier, version string, hasVersion bool, profile string, ok bool) {
	versionStart := 0
	for versionStart < len(s) && isLetterOrDot(s[versionStart]) {
		versionStart++
	}
	if versionStart == 0 {
		return "", "", false, "", false
	}
	identifier = s[:versionStart]
	profileStart := versionStart
	for profileStart < len(s) && (s[profileStart] >= '0' && s[profileStart] <= '9' || s[profileStart] == '.') {
		profileStart++
	}
	if profileStart > versionStart {
		version, hasVersion = s[versionStart:profileStart], true
	}
	if profileStart < len(s) {
		if s[profileStart] != '-' || profileStart+1 == len(s) {
			return "", "", false, "", false
		}
		profile = s[profileStart+1:]
		for i := 0; i < len(profile); i++ {
			c := profile[i]
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '.' || c == '+' || c == '-') {
				return "", "", false, "", false
			}
		}
	}
	return identifier, version, hasVersion, profile, true
}

func isLetterOrDot(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '.' }

// parseSystemVersion ports System.Version.TryParse: two to four
// non-negative integer components.
func parseSystemVersion(s string) (fwVersion, bool) {
	var v fwVersion
	parts := strings.Split(s, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return v, false
	}
	for i, part := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(part), 10, 32)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = int(n)
	}
	return v, true
}

// parseShortFrameworkVersion ports FrameworkNameProvider.TryGetVersion:
// "472" is 4.7.2, "4" is 4.0, and dotted versions parse as written.
func parseShortFrameworkVersion(s string) (fwVersion, bool) {
	if s == "" {
		return fwVersion{}, false
	}
	if strings.Contains(s, ".") {
		return parseSystemVersion(s)
	}
	if len(s) < 2 {
		s += "0"
	}
	if len(s) > 4 {
		s = s[:4]
	}
	return parseSystemVersion(strings.Join(strings.Split(s, ""), "."))
}

// parsePlatformVersion ports FrameworkNameProvider.TryGetPlatformVersion.
func parsePlatformVersion(s string) (fwVersion, bool) {
	if s == "" {
		return fwVersion{}, false
	}
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return parseSystemVersion(s)
}

// parseFrameworkName ports NuGetFramework.ParseFrameworkName for
// ".NETFramework,Version=v4.7.2,Profile=Client". Inputs NuGet rejects with an
// exception are Unsupported here.
func parseFrameworkName(name string) TargetFramework {
	var parts []string
	for _, part := range strings.Split(name, ",") {
		if part != "" {
			parts = append(parts, strings.TrimSpace(part))
		}
	}
	if len(parts) == 0 {
		return frameworkUnsupported
	}
	if f, ok := parseSpecialFramework(parts[0]); ok {
		return f
	}
	framework, ok := frameworkNames.identifier(parts[0])
	if !ok {
		framework = parts[0]
	}
	version := fwVersion{}
	profile := ""
	if value, ok := singleFrameworkNamePart(parts, "Version="); ok {
		value = strings.TrimLeft(value, "v")
		if !strings.Contains(value, ".") {
			value += ".0"
		}
		if version, ok = parseSystemVersion(value); !ok {
			return frameworkUnsupported
		}
	}
	if value, ok := singleFrameworkNamePart(parts, "Profile="); ok {
		profile = value
	}
	if strings.EqualFold(fwPortable, framework) && strings.Contains(profile, "-") {
		return frameworkUnsupported
	}
	if version[0] >= 5 && strings.EqualFold(fwNetCoreApp, framework) {
		profile = ""
	}
	return newFramework(framework, version, profile, "", fwVersion{})
}

func singleFrameworkNamePart(parts []string, prefix string) (string, bool) {
	value, count := "", 0
	for _, part := range parts {
		if len(part) >= len(prefix) && strings.EqualFold(part[:len(prefix)], prefix) {
			value, count = part, count+1
		}
	}
	if count != 1 {
		return "", false
	}
	_, after, _ := strings.Cut(value, "=")
	if before, _, found := strings.Cut(after, "="); found {
		after = before
	}
	return after, true
}

// IsCompatibleWith reports whether a project targeting tf can use assets
// built for pkg (CompatibilityProvider.IsCompatible). It does not apply
// AssetTargetFallback; see CompatibilityWith.
func (tf TargetFramework) IsCompatibleWith(pkg TargetFramework) bool {
	return frameworkCompat.isCompatible(tf, pkg)
}

// FrameworkCompatibility says whether a project can use a package.
type FrameworkCompatibility int

const (
	// FrameworkIncompatible: restore fails with NU1202.
	FrameworkIncompatible FrameworkCompatibility = iota
	// FrameworkFallback: usable only through the SDK's implicit
	// AssetTargetFallback; restore succeeds with warning NU1701.
	FrameworkFallback
	// FrameworkCompatible: some package framework is compatible.
	FrameworkCompatible
)

func (c FrameworkCompatibility) String() string {
	switch c {
	case FrameworkCompatible:
		return "compatible"
	case FrameworkFallback:
		return "compatible through AssetTargetFallback"
	}
	return "incompatible"
}

// implicitAssetTargetFallback is the SDK's implicit AssetTargetFallback
// (dotnet/sdk Microsoft.NET.Sdk.BeforeCommon.targets).
var implicitAssetTargetFallback = []TargetFramework{
	fw(fwNet, 4, 6, 1), fw(fwNet, 4, 6, 2), fw(fwNet, 4, 7), fw(fwNet, 4, 7, 1),
	fw(fwNet, 4, 7, 2), fw(fwNet, 4, 8), fw(fwNet, 4, 8, 1),
}

// usesImplicitAssetTargetFallback mirrors the SDK condition: the target
// framework identifier is .NETCoreApp or .NETStandard at version 2.0 or
// later. Projects that set DisableImplicitAssetTargetFallback or their own
// AssetTargetFallback are not evaluated; guget assumes the default.
func (tf TargetFramework) usesImplicitAssetTargetFallback() bool {
	return (strings.EqualFold(tf.Framework, fwNetCoreApp) || strings.EqualFold(tf.Framework, fwNetStandard)) &&
		tf.Version.compare(fwVersion{2}) >= 0
}

// restoreFramework is the framework restore sees for a project's
// TargetFramework. The SDK defaults a missing Windows platform version to 7.0
// (Microsoft.NET.Windows.targets), so net8.0-windows restores as
// net8.0-windows7.0. Other platforms (android, ios, maccatalyst, tvos, macos,
// tizen) get their default version from an installed workload that guget
// cannot see; for those guget accepts any version of the same platform
// rather than guess one (unknownWorkloadPlatformVersion).
func (tf TargetFramework) restoreFramework() TargetFramework {
	if !tf.isNet5Era() || !tf.hasPlatform() || !tf.PlatformVersion.isZero() {
		return tf
	}
	if strings.EqualFold(tf.Platform, "windows") {
		tf.PlatformVersion = fwVersion{7}
	} else {
		tf.PlatformVersion = unknownWorkloadPlatformVersion
	}
	return tf
}

// unknownWorkloadPlatformVersion stands for a workload-supplied default
// platform version; it is at least every real platform version.
var unknownWorkloadPlatformVersion = fwVersion{frameworkMaxMajor}

// CompatibilityWith reports how a project targeting tf can use a package
// whose assets target packageFrameworks. As in restore, AssetTargetFallback
// applies only when no package framework is compatible.
func (tf TargetFramework) CompatibilityWith(packageFrameworks []TargetFramework) FrameworkCompatibility {
	tf = tf.restoreFramework()
	for _, pkg := range packageFrameworks {
		if tf.IsCompatibleWith(pkg) {
			return FrameworkCompatible
		}
	}
	if tf.usesImplicitAssetTargetFallback() {
		for _, fallback := range implicitAssetTargetFallback {
			for _, pkg := range packageFrameworks {
				if fallback.IsCompatibleWith(pkg) {
					return FrameworkFallback
				}
			}
		}
	}
	return FrameworkIncompatible
}

// compatibilityFor is the single compatibility decision for a package version
// against a set of project targets, used by the version picker, its default
// cursor and the Available column alike. A version without declared
// frameworks supports everything. Targets NuGet cannot parse (unevaluated
// MSBuild expressions) carry no information and are skipped, so a version
// is never rejected for them. The result is the weakest level across the
// remaining targets.
func (v PackageVersion) compatibilityFor(targets Set[TargetFramework]) FrameworkCompatibility {
	if len(v.Frameworks) == 0 {
		return FrameworkCompatible
	}
	level := FrameworkCompatible
	for target := range targets {
		if target.IsUnsupported() {
			continue
		}
		level = min(level, target.CompatibilityWith(v.Frameworks))
		if level == FrameworkIncompatible {
			break
		}
	}
	return level
}

type compatibilityProvider struct {
	mu    sync.Mutex
	cache map[[2]frameworkKey]bool
}

var frameworkCompat = &compatibilityProvider{cache: map[[2]frameworkKey]bool{}}

func (p *compatibilityProvider) isCompatible(target, candidate TargetFramework) bool {
	cacheKey := [2]frameworkKey{target.key(), candidate.key()}
	p.mu.Lock()
	result, ok := p.cache[cacheKey]
	p.mu.Unlock()
	if ok {
		return result
	}
	result = p.isCompatibleCore(target, candidate)
	p.mu.Lock()
	p.cache[cacheKey] = result
	p.mu.Unlock()
	return result
}

func (p *compatibilityProvider) isCompatibleCore(target, candidate TargetFramework) bool {
	if sameFramework(target, candidate) {
		return true
	}
	if !target.isSpecificFramework() || !candidate.isSpecificFramework() {
		switch {
		case target.isAny() || candidate.isAny():
			return true
		case target.IsUnsupported():
			return false
		case candidate.isAgnostic():
			return true
		case candidate.IsUnsupported():
			return false
		}
	}
	if target.isPCL() || candidate.isPCL() {
		return p.isPCLCompatible(target, candidate)
	}
	return p.isCompatibleWithTarget(target, candidate)
}

func (p *compatibilityProvider) isPCLCompatible(target, candidate TargetFramework) bool {
	if target.isPCL() && !candidate.isPCL() {
		return p.isCompatibleWithTarget(target, candidate)
	}
	targets := []TargetFramework{target}
	if target.isPCL() {
		// Optional frameworks are excluded on the project side because it is
		// unknown what is optional on the other framework.
		targets, _ = frameworkNames.portableFrameworks(target.Profile, false)
	}
	candidates := []TargetFramework{candidate}
	if candidate.isPCL() {
		candidates, _ = frameworkNames.portableFrameworks(candidate.Profile, true)
	}
	if len(targets) > len(candidates) {
		return false
	}
	for _, t := range targets {
		found := false
		for _, c := range candidates {
			if p.isCompatible(t, c) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (p *compatibilityProvider) isCompatibleWithTarget(target, candidate TargetFramework) bool {
	targets := append([]TargetFramework{target}, frameworkNames.expand(target)...)
	candidates := append([]TargetFramework{candidate}, frameworkNames.equivalentClosure(candidate)...)
	for _, c := range candidates {
		for _, t := range targets {
			if isCompatibleWithTargetCore(t, c) {
				return true
			}
		}
	}
	return false
}

func isCompatibleWithTargetCore(target, candidate TargetFramework) bool {
	sameName := strings.EqualFold(target.Framework, candidate.Framework)
	if target.isNet5Era() && target.Version[0] >= 6 && target.hasPlatform() && !sameName {
		// net6.0+ platform projects accept their Xamarin-era equivalents.
		switch {
		case strings.EqualFold(candidate.Framework, fwMonoAndroid):
			return strings.EqualFold(target.Platform, "android")
		case strings.EqualFold(candidate.Framework, fwTizen):
			return strings.EqualFold(target.Platform, "tizen")
		}
		return false
	}
	result := sameName && isFrameworkVersionCompatible(target.Version, candidate.Version) && strings.EqualFold(target.Profile, candidate.Profile)
	if target.isNet5Era() && candidate.hasPlatform() {
		// net10.0-windows10.0.x encodes the CsWinRT major version in the
		// platform revision; different revisions are incompatible.
		if result && target.Version[0] >= 10 && strings.EqualFold(target.Platform, "windows") &&
			target.PlatformVersion[0] >= 10 && candidate.PlatformVersion[0] >= 10 &&
			target.PlatformVersion[3] != candidate.PlatformVersion[3] {
			result = false
		}
		result = result && strings.EqualFold(target.Platform, candidate.Platform) &&
			isFrameworkVersionCompatible(target.PlatformVersion, candidate.PlatformVersion)
	}
	return result
}

func isFrameworkVersionCompatible(target, candidate fwVersion) bool {
	return candidate.isZero() || candidate.compare(target) <= 0
}
