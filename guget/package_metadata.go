package main

import (
	"context"
	"errors"
	"fmt"
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
func fetchPackageMetadata(ctx context.Context, services []*NugetService, mapping *PackageSourceMapping, packageID string) packageMetadata {
	eligible := FilterServices(services, mapping, packageID)
	if len(eligible) == 0 {
		if len(services) == 0 {
			return packageMetadata{Err: fmt.Errorf("no metadata-capable NuGet source is available for %q", packageID)}
		}
		return packageMetadata{Err: fmt.Errorf("package source mapping allows no available source for %q", packageID)}
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
