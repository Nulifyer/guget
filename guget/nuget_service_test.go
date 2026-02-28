//go:build integration

package main

import (
	"strings"
	"testing"
)

func nugetOrgService(t *testing.T) *NugetService {
	t.Helper()
	svc, err := NewNugetService(NugetSource{
		Name: "nuget.org",
		URL:  defaultNugetSource,
	})
	if err != nil {
		t.Fatalf("NewNugetService(nuget.org): %v", err)
	}
	return svc
}

func TestNewNugetService_NugetOrg(t *testing.T) {
	svc := nugetOrgService(t)

	if svc.SourceName() != "nuget.org" {
		t.Errorf("SourceName() = %q, want %q", svc.SourceName(), "nuget.org")
	}
	if svc.searchBase == "" {
		t.Error("searchBase was not resolved")
	}
	if svc.regBase == "" {
		t.Error("regBase was not resolved")
	}
}

func TestNewNugetService_InvalidURL(t *testing.T) {
	_, err := NewNugetService(NugetSource{
		Name: "bad",
		URL:  "https://not-a-real-nuget-feed.example.invalid/v3/index.json",
	})
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestRegBase_TrailingSlash(t *testing.T) {
	svc := nugetOrgService(t)

	if !strings.HasSuffix(svc.regBase, "/") {
		t.Errorf("regBase should end with '/', got %q", svc.regBase)
	}
}

func TestSearch_Newtonsoft(t *testing.T) {
	svc := nugetOrgService(t)

	results, err := svc.Search("Newtonsoft", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	found := false
	for _, r := range results {
		if strings.EqualFold(r.ID, "Newtonsoft.Json") {
			found = true
			if r.Version == "" {
				t.Error("Newtonsoft.Json has empty Version")
			}
			break
		}
	}
	if !found {
		ids := make([]string, len(results))
		for i, r := range results {
			ids[i] = r.ID
		}
		t.Fatalf("Newtonsoft.Json not in results: %v", ids)
	}
}

func TestSearchExact_NewtonsoftJson(t *testing.T) {
	svc := nugetOrgService(t)

	pkg, err := svc.SearchExact("Newtonsoft.Json")
	if err != nil {
		t.Fatalf("SearchExact: %v", err)
	}

	if pkg.ID != "Newtonsoft.Json" {
		t.Errorf("ID = %q, want %q", pkg.ID, "Newtonsoft.Json")
	}
	if pkg.Description == "" {
		t.Error("Description is empty")
	}
	if pkg.Authors.Len() == 0 {
		t.Error("Authors set is empty")
	}
	if len(pkg.Versions) == 0 {
		t.Fatal("Versions slice is empty")
	}
	if pkg.ProjectURL == "" {
		t.Error("ProjectURL is empty")
	}
	if len(pkg.Versions) < 50 {
		t.Errorf("expected at least 50 versions, got %d", len(pkg.Versions))
	}

	for i := 0; i < len(pkg.Versions)-1; i++ {
		cur := pkg.Versions[i].SemVer
		next := pkg.Versions[i+1].SemVer
		if next.IsNewerThan(cur) {
			t.Errorf("versions not sorted descending: %s before %s", cur, next)
			break
		}
	}
}

func TestSearchExact_Serilog(t *testing.T) {
	svc := nugetOrgService(t)

	pkg, err := svc.SearchExact("Serilog")
	if err != nil {
		t.Fatalf("SearchExact: %v", err)
	}
	if pkg.ID != "Serilog" {
		t.Errorf("ID = %q, want %q", pkg.ID, "Serilog")
	}
	if pkg.Description == "" {
		t.Error("Description is empty")
	}
	if len(pkg.Versions) < 10 {
		t.Errorf("expected at least 10 versions, got %d", len(pkg.Versions))
	}

	stable := pkg.LatestStable()
	if stable == nil {
		t.Fatal("LatestStable() returned nil")
	}
	if stable.SemVer.IsPreRelease() {
		t.Errorf("LatestStable() returned pre-release: %s", stable.SemVer)
	}
}

func TestSearchExact_NonexistentPackage(t *testing.T) {
	svc := nugetOrgService(t)

	_, err := svc.SearchExact("This.Package.Does.Not.Exist.Xyz.12345")
	if err == nil {
		t.Fatal("expected error for nonexistent package, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestLatestStable_NoPreRelease(t *testing.T) {
	svc := nugetOrgService(t)

	pkg, err := svc.SearchExact("Newtonsoft.Json")
	if err != nil {
		t.Fatalf("SearchExact: %v", err)
	}

	stable := pkg.LatestStable()
	if stable == nil {
		t.Fatal("LatestStable() returned nil")
	}
	if stable.SemVer.IsPreRelease() {
		t.Errorf("LatestStable() returned pre-release: %s", stable.SemVer)
	}
	if stable.SemVer.Major == 0 && stable.SemVer.Minor == 0 && stable.SemVer.Patch == 0 {
		t.Error("LatestStable() returned a zero version")
	}
	if stable.SemVer.Major < 13 {
		t.Errorf("expected major >= 13, got %d", stable.SemVer.Major)
	}
}

func TestVersionsSince_OldVersion(t *testing.T) {
	svc := nugetOrgService(t)

	pkg, err := svc.SearchExact("Newtonsoft.Json")
	if err != nil {
		t.Fatalf("SearchExact: %v", err)
	}

	newer := pkg.VersionsSince("12.0.0")
	if len(newer) == 0 {
		t.Fatal("expected versions newer than 12.0.0")
	}

	floor := ParseSemVer("12.0.0")
	for _, v := range newer {
		if !v.SemVer.IsNewerThan(floor) {
			t.Errorf("VersionsSince returned %s which is not newer than 12.0.0", v.SemVer)
		}
	}
	if len(newer) < 5 {
		t.Errorf("expected at least 5 versions newer than 12.0.0, got %d", len(newer))
	}
}

func TestSearchExact_FrameworkInfo(t *testing.T) {
	svc := nugetOrgService(t)

	pkg, err := svc.SearchExact("Newtonsoft.Json")
	if err != nil {
		t.Fatalf("SearchExact: %v", err)
	}

	hasFrameworks := false
	for _, v := range pkg.Versions {
		if len(v.Frameworks) > 0 {
			hasFrameworks = true
			for _, fw := range v.Frameworks {
				if fw.Raw == "" {
					t.Errorf("version %s has a framework with empty Raw", v.SemVer)
				}
			}
			break
		}
	}
	if !hasFrameworks {
		t.Error("no versions had parsed Frameworks")
	}
}
