package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// defaultMetadataTimeout bounds the package metadata fetch of list and update
// when --timeout is not given, as search's default does. Workspace scanning
// and MSBuild evaluation are not bounded by it.
var defaultMetadataTimeout = 30 * time.Second

// cliUpdateTarget returns the update-target policy for an invocation: an
// alias flag, then --update-target, then the user setting.
func cliUpdateTarget(args cliArgs) UpdateTarget {
	switch {
	case args.bools["latest-compatible"]:
		return UpdateTargetCompatibleStable
	case args.bools["latest-stable"]:
		return UpdateTargetStable
	}
	if raw := args.value("update-target", ""); raw != "" {
		return updateTargetOrDefault(raw)
	}
	return updateTargetOrDefault(effectiveSettings().UpdateTarget)
}

// cliMetadata is the package metadata a CLI command fetched.
type cliMetadata struct {
	packages map[string]packageMetadata // lower-case package ID
	warnings []string
	// failed counts packages without metadata; sourceFailed counts those
	// whose sources failed rather than answering "not found".
	failed, sourceFailed int
	incomplete           bool // a package's metadata misses a failed source
}

// metadataContext bounds the metadata phase with the default timeout unless
// the user gave --timeout, which already bounds the whole command.
func metadataContext(ctx context.Context, inv *cliInvocation) (context.Context, context.CancelFunc) {
	if inv.args.value("timeout", "") != "" {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultMetadataTimeout)
}

// metadataTimedOut reports a default-timeout expiry of the metadata phase.
func metadataTimedOut(parent, phase context.Context) bool {
	return parent.Err() == nil && errors.Is(phase.Err(), context.DeadlineExceeded)
}

// fetchCLIMetadata creates a service for every HTTP source and fetches the
// merged metadata of each package, eight packages at a time.
func (r *cliRuntime) fetchCLIMetadata(ctx context.Context, snapshot *workspaceSnapshot, packageIDs []string) cliMetadata {
	result := cliMetadata{packages: make(map[string]packageMetadata, len(packageIDs))}
	if len(packageIDs) == 0 {
		return result
	}
	var httpSources []NugetSource
	for _, source := range snapshot.Sources {
		if source.IsHTTP() {
			httpSources = append(httpSources, source)
		}
	}
	created := make([]*NugetService, len(httpSources))
	errs := make([]error, len(httpSources))
	var wg sync.WaitGroup
	for i, source := range httpSources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created[i], errs[i] = r.newService(ctx, source)
		}()
	}
	wg.Wait()
	var services []*NugetService
	for i, service := range created {
		if errs[i] != nil {
			if ctx.Err() == nil {
				result.warnings = append(result.warnings, fmt.Sprintf("source %s unavailable: %v", httpSources[i].Name, errs[i]))
			}
			continue
		}
		services = append(services, service)
	}
	DeduplicateADOUpstreams(services)

	var mu sync.Mutex
	jobs := make(chan string)
	for range min(8, len(packageIDs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				metadata := fetchPackageMetadata(ctx, services, snapshot.SourceMapping, id)
				mu.Lock()
				result.packages[strings.ToLower(id)] = metadata
				mu.Unlock()
			}
		}()
	}
	for _, id := range packageIDs {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return result
	}

	sorted := append([]string(nil), packageIDs...)
	sort.Slice(sorted, func(i, j int) bool { return strings.ToLower(sorted[i]) < strings.ToLower(sorted[j]) })
	for _, id := range sorted {
		metadata := result.packages[strings.ToLower(id)]
		switch {
		case metadata.Err != nil:
			result.failed++
			if len(metadata.Failures) > 0 || len(services) == 0 {
				result.sourceFailed++
			}
			result.warnings = append(result.warnings, fmt.Sprintf("metadata for %s unavailable: %v", id, metadata.Err))
		case len(metadata.Failures) > 0:
			result.incomplete = true
			for _, failure := range metadata.Failures {
				result.warnings = append(result.warnings, fmt.Sprintf("metadata for %s may be incomplete: source %s failed: %v", id, failure.Source, failure.Err))
			}
		}
	}
	return result
}

// allSourcesFailed reports that no package got metadata because sources
// failed, the case list and update exit 3 for.
func (m cliMetadata) allSourcesFailed() bool {
	return len(m.packages) > 0 && m.sourceFailed == len(m.packages)
}

// assessableRow reports whether a list row can have an update target:
// PackageDownload rows are exact restore-only downloads, not dependencies.
func assessableRow(row cliPackageRow) bool { return !row.Download }

// assessListRows fills each row's assessment fields. A direct row is judged
// against every target framework of the projects sharing its version owner,
// since an update changes them all; a transitive row against its own
// framework. The version in use is the restored version when known.
func assessListRows(rows []cliPackageRow, snapshot *workspaceSnapshot, metadata cliMetadata, policy UpdateTarget) {
	projects := make(map[string]*ParsedProject, len(snapshot.ParsedProjects))
	for _, project := range snapshot.ParsedProjects {
		projects[project.FilePath] = project
	}
	frameworkViews := make(map[*ParsedProject]map[string]Set[TargetFramework])
	for i := range rows {
		row := &rows[i]
		if !assessableRow(*row) {
			continue
		}
		info := metadata.packages[strings.ToLower(row.Package)].Info
		if info == nil {
			continue
		}
		frameworks := NewSet[TargetFramework]()
		if project := projects[row.ProjectPath]; project != nil && row.Direct {
			view, ok := frameworkViews[project]
			if !ok {
				view = packageFrameworksForView(snapshot.ParsedProjects, project)
				frameworkViews[project] = view
			}
			for framework := range view[strings.ToLower(row.Package)] {
				frameworks.Add(framework)
			}
		}
		if frameworks.Len() == 0 && row.TargetFramework != nil {
			frameworks.Add(ParseTargetFramework(*row.TargetFramework))
		}
		ref := PackageReference{Name: row.Package}
		if row.RequestedExpression != nil {
			ref.Requested = *row.RequestedExpression
			ref.Version = requestedVersion(ref.Requested)
		}
		if row.ResolvedVersion != nil {
			if resolved := ParseSemVer(*row.ResolvedVersion); resolved.IsValid() {
				ref.Version = resolved
				if ref.Requested == "" {
					ref.Requested = resolved.String()
				}
			}
		}
		setAssessmentFields(row, assessPackage(info, []PackageReference{ref}, frameworks, policy))
	}
}

func setAssessmentFields(row *cliPackageRow, a PackageAssessment) {
	version := func(v *PackageVersion) *string {
		if v == nil {
			return nil
		}
		return stringPtr(v.SemVer.String())
	}
	row.UpdateTarget = version(a.Target)
	if a.Target != nil {
		row.UpdateTargetCompatibility = stringPtr(compatibilityLabel(a.CandidateCompatibility))
	}
	row.LatestStable, row.LatestVersion = version(a.LatestStable), version(a.LatestVersion)
	vulnerable, deprecated := a.Vulnerable(), a.Deprecated()
	row.Vulnerable, row.Deprecated = &vulnerable, &deprecated
	row.Advisories = []cliAdvisory{}
	for _, advisory := range a.Advisories {
		row.Advisories = append(row.Advisories, cliAdvisory{Severity: advisory.SeverityLabel(), URL: advisory.AdvisoryURL})
	}
	row.DeprecationReasons = []string{}
	for _, deprecation := range a.Deprecations {
		for _, reason := range deprecation.Reasons {
			if !containsFold(row.DeprecationReasons, reason) {
				row.DeprecationReasons = append(row.DeprecationReasons, reason)
			}
		}
		if row.AlternatePackage == nil && deprecation.AlternatePackageID != "" {
			row.AlternatePackage = stringPtr(deprecation.AlternatePackageID)
		}
	}
}

// listFilter is the --outdated, --vulnerable and --deprecated selection. A
// row matches when it has any selected problem.
type listFilter struct{ outdated, vulnerable, deprecated bool }

func (f listFilter) active() bool { return f.outdated || f.vulnerable || f.deprecated }

func (f listFilter) matches(row cliPackageRow) bool {
	return f.outdated && row.UpdateTarget != nil ||
		f.vulnerable && row.Vulnerable != nil && *row.Vulnerable ||
		f.deprecated && row.Deprecated != nil && *row.Deprecated
}

// latestVersionSelector returns update's per-project version choice for
// --latest: the assessment target of the installed reference under policy,
// judged against the frameworks of every project sharing its version owner.
// It compares with the requested lower bound, as dotnet package update does.
func latestVersionSelector(snapshot *workspaceSnapshot, info *PackageInfo, policy UpdateTarget) func(*ParsedProject, PackageReference) (string, bool) {
	return func(project *ParsedProject, installed PackageReference) (string, bool) {
		frameworks := NewSet[TargetFramework]()
		for framework := range packageFrameworksForView(snapshot.ParsedProjects, project)[strings.ToLower(installed.Name)] {
			frameworks.Add(framework)
		}
		for framework := range project.TargetFrameworks {
			frameworks.Add(framework)
		}
		target := assessPackage(info, []PackageReference{installed}, frameworks, policy).Target
		if target == nil {
			return "", false
		}
		// The editor keeps an exact pin's brackets.
		return target.SemVer.String(), true
	}
}
