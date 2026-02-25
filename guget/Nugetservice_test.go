//go:build integration

package main

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

// nugetOrgService returns a NugetService initialised against nuget.org.
// Calls t.Fatal if the service cannot be created, which naturally
// prevents all dependent tests from running when the network is unavailable.
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

// ─────────────────────────────────────────────
// NewNugetService — successful init
// ─────────────────────────────────────────────

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

// ─────────────────────────────────────────────
// NewNugetService — failure with invalid URL
// ─────────────────────────────────────────────

func TestNewNugetService_InvalidURL(t *testing.T) {
	_, err := NewNugetService(NugetSource{
		Name: "bad",
		URL:  "https://not-a-real-nuget-feed.example.invalid/v3/index.json",
	})
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

// ─────────────────────────────────────────────
// Registration base URL — trailing slash normalization
// ─────────────────────────────────────────────

func TestRegBase_TrailingSlash(t *testing.T) {
	svc := nugetOrgService(t)

	if !strings.HasSuffix(svc.regBase, "/") {
		t.Errorf("regBase should end with '/', got %q", svc.regBase)
	}
}

// ─────────────────────────────────────────────
// Search — returns results for well-known query
// ─────────────────────────────────────────────

func TestSearch_Newtonsoft(t *testing.T) {
	svc := nugetOrgService(t)

	results, err := svc.Search("Newtonsoft", 5)
	if err != nil {
		t.Fatalf("Search(\"Newtonsoft\", 5): %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result for \"Newtonsoft\"")
	}

	found := false
	for _, r := range results {
		if strings.EqualFold(r.ID, "Newtonsoft.Json") {
			found = true
			if r.Version == "" {
				t.Error("Newtonsoft.Json result has empty Version")
			}
			if r.TotalDownloads == 0 {
				t.Error("Newtonsoft.Json result has zero TotalDownloads")
			}
			break
		}
	}
	if !found {
		ids := make([]string, len(results))
		for i, r := range results {
			ids[i] = r.ID
		}
		t.Fatalf("Newtonsoft.Json not in search results: %v", ids)
	}
}

// ─────────────────────────────────────────────
// SearchExact — well-known package metadata
// ─────────────────────────────────────────────

func TestSearchExact_NewtonsoftJson(t *testing.T) {
	svc := nugetOrgService(t)

	pkg, err := svc.SearchExact("Newtonsoft.Json")
	if err != nil {
		t.Fatalf("SearchExact(\"Newtonsoft.Json\"): %v", err)
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
	if pkg.TotalDownloads == 0 {
		t.Error("TotalDownloads is zero")
	}
	if pkg.ProjectURL == "" {
		t.Error("ProjectURL is empty")
	}

	// Newtonsoft.Json has 80+ releases
	if len(pkg.Versions) < 50 {
		t.Errorf("expected at least 50 versions, got %d", len(pkg.Versions))
	}

	// Versions should be sorted newest → oldest
	for i := 0; i < len(pkg.Versions)-1; i++ {
		cur := pkg.Versions[i].SemVer
		next := pkg.Versions[i+1].SemVer
		if next.IsNewerThan(cur) {
			t.Errorf("versions not sorted descending: %s before %s", cur, next)
			break
		}
	}
}

// ─────────────────────────────────────────────
// SearchExact — second well-known package (Serilog)
// ─────────────────────────────────────────────

func TestSearchExact_Serilog(t *testing.T) {
	svc := nugetOrgService(t)

	pkg, err := svc.SearchExact("Serilog")
	if err != nil {
		t.Fatalf("SearchExact(\"Serilog\"): %v", err)
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
		t.Fatal("LatestStable() returned nil for Serilog")
	}
	if stable.SemVer.IsPreRelease() {
		t.Errorf("LatestStable() returned pre-release: %s", stable.SemVer)
	}
}

// ─────────────────────────────────────────────
// SearchExact — nonexistent package
// ─────────────────────────────────────────────

func TestSearchExact_NonexistentPackage(t *testing.T) {
	svc := nugetOrgService(t)

	_, err := svc.SearchExact("This.Package.Does.Not.Exist.Xyz.12345")
	if err == nil {
		t.Fatal("expected error for nonexistent package, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error message, got: %v", err)
	}
}

// ─────────────────────────────────────────────
// LatestStable — returns non-pre-release version
// ─────────────────────────────────────────────

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

	// Newtonsoft.Json latest stable is at least 13.x
	if stable.SemVer.Major < 13 {
		t.Errorf("expected LatestStable major >= 13, got %d", stable.SemVer.Major)
	}
}

// ─────────────────────────────────────────────
// VersionsSince — returns versions newer than a known old version
// ─────────────────────────────────────────────

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

	// All returned versions must actually be newer than 12.0.0
	floor := ParseSemVer("12.0.0")
	for _, v := range newer {
		if !v.SemVer.IsNewerThan(floor) {
			t.Errorf("VersionsSince returned %s which is not newer than 12.0.0", v.SemVer)
		}
	}

	// At least 5 versions after 12.0.0 (13.0.1, 13.0.2, 13.0.3, etc.)
	if len(newer) < 5 {
		t.Errorf("expected at least 5 versions newer than 12.0.0, got %d", len(newer))
	}
}

// ─────────────────────────────────────────────
// SearchExact — versions include framework info
// ─────────────────────────────────────────────

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
		t.Error("no versions had parsed Frameworks; expected at least some")
	}
}
