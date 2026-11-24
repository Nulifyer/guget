package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// sourceFailure is one source's failure to answer a metadata request. A
// source that answers "not found" has not failed.
type sourceFailure struct {
	Source string
	Err    error
}

// packageMetadata is the merged metadata for one package across every source
// its package source mapping allows.
type packageMetadata struct {
	Info *PackageInfo
	// Source names the source the package-level metadata came from: the one
	// providing the newest listed stable version (as SearchExact chooses).
	Source string
	// Failures lists sources that failed while others may have answered, so
	// Info can lack versions those sources have.
	Failures []sourceFailure
	// Err is set when no source supplied the package.
	Err error
}

// fetchPackageMetadata asks every allowed source for packageID in parallel
// and merges the listed versions, as NuGet does when it looks for the latest
// version (PackageUpdateIO.GetLatestVersionAsync queries each mapped source
// and takes the highest). A failing source is reported, not fatal, while
// another source answers. Cancelling ctx stops every request.
//
// The services are filtered by the workspace view of the mapping, which lets
// a source serve a package when any project may use it. Decisions for a
// project use fetchScopedPackageMetadata.
func fetchPackageMetadata(ctx context.Context, services []*NugetService, mapping *PackageSourceMapping, packageID string) packageMetadata {
	return fetchMetadataFrom(ctx, FilterServices(services, mapping, packageID), noEligibleSource(services, mapping.IsConfigured(), packageID), packageID)
}

// noEligibleSource is the error for a package no available source may supply.
func noEligibleSource(services []*NugetService, mapped bool, packageID string) error {
	if len(services) == 0 || !mapped {
		return fmt.Errorf("no metadata-capable NuGet source is available for %q", packageID)
	}
	return fmt.Errorf("package source mapping allows no available source for %q", packageID)
}

// fetchScopedPackageMetadata returns packageID's metadata for each scope
// index, each from only the sources that scope may use for the package.
// Scopes that may use the same sources share one fetch, so a workspace with
// one source configuration asks each source once per package.
func fetchScopedPackageMetadata(ctx context.Context, services []*NugetService, mapping *PackageSourceMapping, packageID string, scopes []int) map[int]packageMetadata {
	positions := make(map[*NugetService]int, len(services))
	for i, service := range services {
		positions[service] = i
	}
	result := make(map[int]packageMetadata, len(scopes))
	fetched := make(map[string]packageMetadata)
	for _, scope := range scopes {
		eligible := mapping.eligibleServices(services, scope, packageID)
		empty := noEligibleSource(services, mapping.scopeMapped(scope), packageID)
		var key strings.Builder
		for _, service := range eligible {
			fmt.Fprintf(&key, "%d,", positions[service])
		}
		if len(eligible) == 0 {
			key.WriteString(empty.Error())
		}
		metadata, ok := fetched[key.String()]
		if !ok {
			metadata = fetchMetadataFrom(ctx, eligible, empty, packageID)
			fetched[key.String()] = metadata
		}
		result[scope] = metadata
	}
	return result
}

// intersectScopedMetadata combines one package's metadata from several
// scopes into what every one of them can use: only the versions (by NuGet
// version equality) each scope has, unlisted when any scope lists it so.
// Package-level fields come from a scope that supplies the newest common
// version, chosen as mergePackageInfos chooses. A scope without metadata
// leaves nothing every scope can use, so its error is the result.
func intersectScopedMetadata(scopes []int, byScope map[int]packageMetadata) packageMetadata {
	var parts []packageMetadata
	var failures []sourceFailure
	seen := make(map[*PackageInfo]bool)
	for _, scope := range scopes {
		metadata := byScope[scope]
		for _, failure := range metadata.Failures {
			if !slices.ContainsFunc(failures, func(f sourceFailure) bool { return f.Source == failure.Source }) {
				failures = append(failures, failure)
			}
		}
		if metadata.Err != nil || metadata.Info == nil {
			if metadata.Err == nil {
				metadata.Err = errPackageNotFound
			}
			metadata.Failures = failures
			return metadata
		}
		if !seen[metadata.Info] {
			seen[metadata.Info] = true
			parts = append(parts, metadata)
		}
	}
	if len(parts) == 0 {
		return packageMetadata{Err: errPackageNotFound}
	}
	if len(parts) == 1 {
		parts[0].Failures = failures
		return parts[0]
	}
	type presence struct {
		count    int
		unlisted bool
	}
	versions := make(map[string]*presence)
	for _, part := range parts {
		counted := make(map[string]bool)
		for _, v := range part.Info.Versions {
			key := v.SemVer.versionKey()
			if counted[key] {
				continue
			}
			counted[key] = true
			p := versions[key]
			if p == nil {
				p = &presence{}
				versions[key] = p
			}
			p.count++
			p.unlisted = p.unlisted || v.Unlisted
		}
	}
	common := func(part packageMetadata) []PackageVersion {
		var kept []PackageVersion
		for _, v := range part.Info.Versions {
			if p := versions[v.SemVer.versionKey()]; p != nil && p.count == len(parts) {
				v.Unlisted = p.unlisted
				if v.Source == "" {
					v.Source = part.Source
				}
				kept = append(kept, v)
			}
		}
		sortVersionsDesc(kept)
		return kept
	}
	first := common(parts[0])
	if len(first) == 0 {
		return packageMetadata{Failures: failures, Err: fmt.Errorf("package %q: no version is available from the sources of every project that references it", parts[0].Info.ID)}
	}
	newest := newestForMetadata(first)
	chosen, chosenVersions := parts[0], first
	for _, part := range parts {
		if latest := ParseSemVer(part.Info.LatestVersion); latest.IsValid() && latest.Equal(newest.SemVer) {
			chosen, chosenVersions = part, common(part)
			break
		}
	}
	merged := *chosen.Info
	merged.Versions = chosenVersions
	if latest := ParseSemVer(merged.LatestVersion); !latest.IsValid() || !latest.Equal(newest.SemVer) {
		merged.LatestVersion = newest.SemVer.String()
	}
	return packageMetadata{Info: &merged, Source: chosen.Source, Failures: failures}
}

// newestForMetadata returns the version SearchExact takes package-level
// metadata from: the newest listed stable, then newest listed, newest
// stable, newest. versions must be sorted newest first and not be empty.
func newestForMetadata(versions []PackageVersion) PackageVersion {
	for _, accept := range []func(PackageVersion) bool{
		func(v PackageVersion) bool { return !v.Unlisted && !v.SemVer.IsPreRelease() },
		func(v PackageVersion) bool { return !v.Unlisted },
		func(v PackageVersion) bool { return !v.SemVer.IsPreRelease() },
	} {
		for _, v := range versions {
			if accept(v) {
				return v
			}
		}
	}
	return versions[0]
}

// fetchMetadataFrom asks each eligible service for packageID in parallel and
// merges the answers. empty is the error when no service is eligible.
func fetchMetadataFrom(ctx context.Context, eligible []*NugetService, empty error, packageID string) packageMetadata {
	if len(eligible) == 0 {
		return packageMetadata{Err: empty}
	}
	type answer struct {
		info *PackageInfo
		err  error
	}
	answers := make([]answer, len(eligible))
	var wg sync.WaitGroup
	for i, service := range eligible {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := service.SearchExactContext(ctx, packageID)
			answers[i] = answer{info, err}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return packageMetadata{Err: err}
	}

	var found []*PackageInfo
	var foundSources []string
	var failures []sourceFailure
	var notFound error
	var nugetOrg *PackageInfo
	for i, a := range answers {
		name := eligible[i].SourceName()
		switch {
		case a.err == nil && a.info != nil:
			found = append(found, a.info)
			foundSources = append(foundSources, name)
			if strings.EqualFold(name, "nuget.org") {
				nugetOrg = a.info
			}
		case errors.Is(a.err, errPackageNotFound):
			if notFound == nil {
				notFound = a.err
			}
		default:
			err := a.err
			if err == nil {
				err = fmt.Errorf("package %q %w", packageID, errPackageNotFound)
			}
			logDebug("Source [%s] failed for %s: %v", name, packageID, err)
			failures = append(failures, sourceFailure{Source: name, Err: err})
		}
	}
	if len(found) == 0 {
		switch len(failures) {
		case 0:
			return packageMetadata{Err: notFound}
		case 1:
			return packageMetadata{Failures: failures, Err: failures[0].Err}
		}
		parts := make([]string, len(failures))
		for i, failure := range failures {
			parts[i] = failure.Source + ": " + failure.Err.Error()
		}
		return packageMetadata{Failures: failures, Err: fmt.Errorf("every source failed: %s", strings.Join(parts, "; "))}
	}

	info, source := mergePackageInfos(found, foundSources)
	if nugetOrg != nil && !strings.EqualFold(source, "nuget.org") {
		info.NugetOrgURL = "https://www.nuget.org/packages/" + nugetOrg.ID
		enrichFromNugetOrg(info, nugetOrg)
	}
	return packageMetadata{Info: info, Source: source, Failures: failures}
}

// mergePackageInfos merges one package's metadata from several sources,
// given in source order. A version several sources have keeps the first
// listed entry, so a version counts as listed when any source lists it
// (each source's listed versions are NuGet's candidates). Package-level
// fields come from the source providing the version SearchExact would take
// them from: the newest listed stable, then newest listed, newest stable,
// newest.
func mergePackageInfos(infos []*PackageInfo, sources []string) (*PackageInfo, string) {
	if len(infos) == 1 {
		return infos[0], sources[0]
	}
	index := make(map[string]int)
	var versions []PackageVersion
	for i, info := range infos {
		for _, v := range info.Versions {
			if v.Source == "" {
				v.Source = sources[i]
			}
			key := v.SemVer.versionKey()
			existing, ok := index[key]
			switch {
			case !ok:
				index[key] = len(versions)
				versions = append(versions, v)
			case versions[existing].Unlisted && !v.Unlisted:
				versions[existing] = v
			}
		}
	}
	sortVersionsDesc(versions)

	var meta *PackageVersion
	for _, accept := range []func(PackageVersion) bool{
		func(v PackageVersion) bool { return !v.Unlisted && !v.SemVer.IsPreRelease() },
		func(v PackageVersion) bool { return !v.Unlisted },
		func(v PackageVersion) bool { return !v.SemVer.IsPreRelease() },
		func(PackageVersion) bool { return true },
	} {
		for i := range versions {
			if accept(versions[i]) {
				meta = &versions[i]
				break
			}
		}
		if meta != nil {
			break
		}
	}
	chosen := 0
	if meta != nil {
		for i, source := range sources {
			if strings.EqualFold(source, meta.Source) {
				chosen = i
				break
			}
		}
	}
	merged := *infos[chosen]
	merged.Versions = versions
	return &merged, sources[chosen]
}
