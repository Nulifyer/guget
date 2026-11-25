package main

import (
	"slices"
	"strings"
)

// UpdateTarget is the update-target policy: which listed versions an update
// may aim for. It is the single owner of that rule for the TUI and the CLI.
//
//   - compatible-stable (default): the newest listed stable version every
//     affected target framework can use (directly or through
//     AssetTargetFallback).
//   - compatible: the same, with prereleases always counting.
//   - stable: the newest listed stable version regardless of frameworks.
//     For a stable reference this is what `dotnet package update` writes and
//     what `dotnet list package --outdated` reports as Latest.
//
// A reference on a prerelease follows newer prereleases until a stable
// version newer than it is listed, then the newest stable, under
// compatible-stable and stable. `dotnet package update` looks at prereleases
// whenever the reference's VersionRange.MinVersion is a prerelease
// (NuGet.CommandLine.XPlat PackageUpdateCommandRunner.cs:553 and :725), and
// would offer 12.0.0-preview.1 over a newer 11.0.0; guget offers 11.0.0, and
// leaves following every prerelease to compatible. Candidates are listed
// versions only (PackageUpdateIO.cs:511-517, includeUnlisted: false), and an
// update never goes below the requested lower bound
// (PackageUpdateCommandRunner.cs:583).
type UpdateTarget string

const (
	UpdateTargetCompatibleStable UpdateTarget = "compatible-stable"
	UpdateTargetCompatible       UpdateTarget = "compatible"
	UpdateTargetStable           UpdateTarget = "stable"
)

// parseUpdateTarget accepts a policy name in any case.
func parseUpdateTarget(value string) (UpdateTarget, bool) {
	switch target := UpdateTarget(strings.ToLower(strings.TrimSpace(value))); target {
	case UpdateTargetCompatibleStable, UpdateTargetCompatible, UpdateTargetStable:
		return target, true
	}
	return "", false
}

// updateTargetOrDefault parses a configured policy, falling back to the
// default for an empty or unknown value (settings report the warning).
func updateTargetOrDefault(value string) UpdateTarget {
	if target, ok := parseUpdateTarget(value); ok {
		return target
	}
	return UpdateTargetCompatibleStable
}

func (t UpdateTarget) checksCompatibility() bool { return t != UpdateTargetStable }

func (t UpdateTarget) alwaysPrerelease() bool { return t == UpdateTargetCompatible }

// InstalledAdvisory is a vulnerability reported for an installed version.
type InstalledAdvisory struct {
	Version SemVer
	PackageVulnerability
}

// InstalledDeprecation is the deprecation of an installed version.
type InstalledDeprecation struct {
	Version SemVer
	*PackageDeprecation
}

// PackageAssessment is guget's judgement of one package in one scope: a
// project, or every project sharing a decision such as All Projects.
type PackageAssessment struct {
	Policy UpdateTarget
	// Prerelease reports whether prereleases were candidates: always under
	// compatible, otherwise while the reference is on a prerelease and no
	// stable version newer than it qualifies.
	Prerelease bool
	// Candidate is the newest listed version the policy allows, whether or
	// not it is newer than what is installed; nil when none qualifies.
	Candidate *PackageVersion
	// CandidateCompatibility is Candidate's framework compatibility with the
	// scope's targets. Under stable it can be FrameworkIncompatible.
	CandidateCompatibility FrameworkCompatibility
	// Target is Candidate when it is an update: no reference would move
	// below its current version and at least one would move up. Nil when the
	// scope is up to date under the policy.
	Target *PackageVersion
	// LatestStable is the newest listed stable version, ignoring frameworks.
	LatestStable *PackageVersion
	// LatestVersion is the newest listed version, prerelease or not.
	LatestVersion *PackageVersion
	// Installed are the distinct versions the scope uses, oldest first.
	// Unknown versions (an unresolved float) are left out.
	Installed []SemVer
	// Advisories and Deprecations apply to the installed versions, matched by
	// NuGet version equality.
	Advisories   []InstalledAdvisory
	Deprecations []InstalledDeprecation
}

func (a PackageAssessment) Vulnerable() bool { return len(a.Advisories) > 0 }

func (a PackageAssessment) Deprecated() bool { return len(a.Deprecations) > 0 }

// assessPackage computes the assessment for package info as referenced by
// refs, used by projects targeting frameworks, under policy. Each ref's
// Version is the version in use: the restored version where the caller knows
// it, otherwise guget's known version; an invalid Version is resolved from
// info's versions the way restore would (rowVersion). Requested holds the
// version expression for the prerelease rule and lower bound.
//
// For several references (All Projects, or a shared central version), the
// target never moves any reference below its current version, and the
// prerelease rule follows the references at the newest installed version.
// It does no I/O.
func assessPackage(info *PackageInfo, refs []PackageReference, frameworks Set[TargetFramework], policy UpdateTarget) PackageAssessment {
	if info == nil {
		return assessFloors(nil, packageFloors{}, frameworks, policy)
	}
	return assessFloors(info, floorsOf(info, refs), frameworks, policy)
}

// packageFloors holds each reference's floor: the version in use, else its
// lower bound. It depends only on the references and info, so a caller that
// assesses several policies computes it once.
type packageFloors struct {
	floors     []packageFloor
	newest     SemVer
	haveNewest bool
}

type packageFloor struct {
	version    SemVer
	known      bool // version is set
	installed  bool // version is the one in use, not only the lower bound
	preRelease bool // the requested lower bound is a prerelease
}

func floorsOf(info *PackageInfo, refs []PackageReference) packageFloors {
	result := packageFloors{floors: make([]packageFloor, 0, len(refs))}
	for _, ref := range refs {
		r, parsed := ref.RequestedRange()
		f := packageFloor{preRelease: parsed && r.LowerBoundIsPreRelease()}
		if installed := rowVersion(ref, info); installed.IsValid() {
			f.version, f.known, f.installed = installed, true, true
		} else if lower, ok := r.LowerBound(); parsed && ok && lower.IsValid() {
			f.version, f.known = lower, true
		}
		if f.known && (!result.haveNewest || f.version.IsNewerThan(result.newest)) {
			result.newest, result.haveNewest = f.version, true
		}
		result.floors = append(result.floors, f)
	}
	return result
}

func assessFloors(info *PackageInfo, computed packageFloors, frameworks Set[TargetFramework], policy UpdateTarget) PackageAssessment {
	if _, ok := parseUpdateTarget(string(policy)); !ok {
		policy = UpdateTargetCompatibleStable
	}
	a := PackageAssessment{Policy: policy}
	if info == nil {
		return a
	}
	floors, newest, haveNewest := computed.floors, computed.newest, computed.haveNewest

	a.Prerelease = policy.alwaysPrerelease()
	if !a.Prerelease {
		for _, f := range floors {
			if f.preRelease && (!haveNewest || (f.known && f.version.Equal(newest))) {
				a.Prerelease = true
				break
			}
		}
	}

	// A prerelease reference follows prereleases only until a qualifying
	// stable version newer than it is listed.
	untilStable := a.Prerelease && !policy.alwaysPrerelease() && haveNewest
	var newerStable *PackageVersion
	var newerStableCompatibility FrameworkCompatibility
	for i := range info.Versions { // newest first
		if a.Candidate != nil && a.LatestStable != nil && (!untilStable || newerStable != nil || !a.Candidate.SemVer.IsNewerThan(newest)) {
			break
		}
		v := &info.Versions[i]
		if v.Unlisted || !v.SemVer.IsValid() {
			continue
		}
		if a.LatestVersion == nil {
			a.LatestVersion = v
		}
		if a.LatestStable == nil && !v.SemVer.IsPreRelease() {
			a.LatestStable = v
		}
		if v.SemVer.IsPreRelease() && !a.Prerelease {
			continue
		}
		needStable := untilStable && newerStable == nil && !v.SemVer.IsPreRelease() && v.SemVer.IsNewerThan(newest)
		if a.Candidate != nil && !needStable {
			continue
		}
		compatibility := v.compatibilityFor(frameworks)
		if policy.checksCompatibility() && compatibility == FrameworkIncompatible {
			continue
		}
		if needStable {
			newerStable, newerStableCompatibility = v, compatibility
		}
		if a.Candidate == nil {
			a.Candidate, a.CandidateCompatibility = v, compatibility
		}
	}
	if newerStable != nil {
		a.Candidate, a.CandidateCompatibility, a.Prerelease = newerStable, newerStableCompatibility, false
	}

	if a.Candidate != nil {
		raises := len(floors) == 0
		lowers := false
		for _, f := range floors {
			switch {
			case !f.known:
				raises = true // no lower bound: any version is an update
			case a.Candidate.SemVer.IsNewerThan(f.version):
				raises = true
			case !a.Candidate.SemVer.Equal(f.version):
				lowers = true
			}
		}
		if raises && !lowers {
			a.Target = a.Candidate
		}
	}

	seen := NewSet[string]()
	for _, f := range floors {
		if !f.installed || seen.Contains(f.version.versionKey()) {
			continue
		}
		seen.Add(f.version.versionKey())
		a.Installed = append(a.Installed, f.version)
	}
	slices.SortFunc(a.Installed, SemVer.Compare)
	for _, installed := range a.Installed {
		for _, v := range info.Versions {
			if !v.SemVer.Equal(installed) {
				continue
			}
			for _, advisory := range v.Vulnerabilities {
				a.Advisories = append(a.Advisories, InstalledAdvisory{Version: v.SemVer, PackageVulnerability: advisory})
			}
			if v.Deprecation != nil {
				a.Deprecations = append(a.Deprecations, InstalledDeprecation{Version: v.SemVer, PackageDeprecation: v.Deprecation})
			}
			break
		}
	}
	return a
}

// compatibilityLabel is the CLI and JSON name of a compatibility level.
func compatibilityLabel(c FrameworkCompatibility) string {
	switch c {
	case FrameworkCompatible:
		return "compatible"
	case FrameworkFallback:
		return "fallback"
	}
	return "incompatible"
}
