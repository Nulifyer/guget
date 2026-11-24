package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func equalityTestApp(projects ...*ParsedProject) *App {
	m := &App{ctx: &AppContext{Results: make(map[string]nugetResult), PendingPackages: NewSet[string]()}}
	m.ctx.ParsedProjects = projects
	return m
}

func equalityTestProject(path string, packages map[string]string, frameworks ...string) *ParsedProject {
	p := &ParsedProject{FilePath: path, FileName: path, Packages: NewSet[PackageReference](), TargetFrameworks: NewSet[TargetFramework](), PackageSources: map[string]string{}}
	for _, f := range frameworks {
		p.TargetFrameworks.Add(ParseTargetFramework(f))
	}
	for name, expression := range packages {
		p.Packages.Add(PackageReference{Name: name, Version: requestedVersion(expression), Requested: expression})
		p.PackageSources[strings.ToLower(name)] = path
	}
	return p
}

func selectProject(m *App, project *ParsedProject) {
	m.projects.items = []projectItem{{}, {project: project}}
	m.projects.cursor = 1
}

var vulnerableOne = []PackageVulnerability{{AdvisoryURL: "https://example.invalid/advisory", Severity: 2}}

func registrationVersions() []PackageVersion {
	return []PackageVersion{
		{SemVer: ParseSemVer("2.0.0"), Published: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
		{SemVer: ParseSemVer("1.5.0"), Published: time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)},
		{SemVer: ParseSemVer("1.0.0"), Published: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Vulnerabilities: vulnerableOne},
	}
}

func TestShortInstalledVersionMatchesRegistrationVersion(t *testing.T) {
	project := equalityTestProject("/w/App.csproj", map[string]string{"A": "1.0"}, "net8.0")
	m := equalityTestApp(project)
	m.ctx.Results["A"] = nugetResult{pkg: &PackageInfo{ID: "A", Versions: registrationVersions()}}
	selectProject(m, project)
	if m.selectedProject() != project {
		t.Fatal("test expects the project to be selected")
	}
	m.rebuildPackageRows()
	row := m.packages.rows[0]
	if !row.vulnerable {
		t.Fatal("installed 1.0 is the vulnerable registration version 1.0.0")
	}
	if currentVersionText(row) != "1.0" {
		t.Fatalf("the Requested column changed to %q", currentVersionText(row))
	}
	if !strings.Contains(m.renderDetailVersionList(row, 60), "▶ ") {
		t.Fatal("detail version list does not mark 1.0.0 as current for an installed 1.0")
	}

	rows := []packageRow{row, {ref: PackageReference{Name: "B", Version: ParseSemVer("2.0.0.0")}, info: row.info}}
	sortPackageRowsByCurrent(rows)
	if rows[0].ref.Name != "A" {
		t.Fatal("sorting by current must find 1.0's publish date through 1.0.0")
	}
}

func TestAllProjectsViewComparesVersionsByNuGetEquality(t *testing.T) {
	a := equalityTestProject("/w/A.csproj", map[string]string{"A": "1.0"}, "net8.0")
	b := equalityTestProject("/w/B.csproj", map[string]string{"A": "1.0.0"}, "net8.0")
	m := equalityTestApp(a, b)
	m.ctx.Results["A"] = nugetResult{pkg: &PackageInfo{ID: "A", Versions: registrationVersions()}}
	m.rebuildPackageRows()
	row := m.packages.rows[0]
	if row.diverged {
		t.Fatal("1.0 and 1.0.0 are the same version, not a divergence")
	}
	if !row.vulnerable {
		t.Fatal("the shared vulnerable version must be flagged")
	}
}

func TestFloatingRequestUsesTheVersionRestoreSelects(t *testing.T) {
	project := equalityTestProject("/w/App.csproj", map[string]string{"A": "1.*", "B": "(,0.5]"}, "net8.0")
	m := equalityTestApp(project)
	m.ctx.Results["A"] = nugetResult{pkg: &PackageInfo{ID: "A", Versions: registrationVersions()}}
	m.ctx.Results["B"] = nugetResult{pkg: &PackageInfo{ID: "B", Versions: registrationVersions()}}
	selectProject(m, project)
	m.rebuildPackageRows()
	for _, row := range m.packages.rows {
		switch row.ref.Name {
		case "A":
			if row.ref.Version.String() != "1.5.0" || currentVersionText(row) != "1.*" {
				t.Fatalf("1.* resolved to %q and shows %q", row.ref.Version, currentVersionText(row))
			}
			if row.statusIcon() != "↑" {
				t.Fatalf("1.* at 1.5.0 with 2.0.0 available shows %q", row.statusIcon())
			}
		case "B":
			if row.ref.Version.IsValid() || currentVersionText(row) != "(,0.5]" {
				t.Fatalf("an unsatisfiable request must stay unknown, got %+v", row.ref.Version)
			}
			_ = row.statusIcon() // must not panic
		}
	}
}

func TestQuickUpdateTreatsShortVersionAsCurrent(t *testing.T) {
	m, _, path := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="2.0"/></ItemGroup></Project>`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cmd := m.updatePackage(false, scopeSelected); cmd != nil {
		t.Fatal("an update to the version already requested scheduled work")
	}
	if !strings.Contains(m.ctx.StatusLine, "A is already at 2.0.0") {
		t.Fatalf("status = %q", m.ctx.StatusLine)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("project changed")
	}
}

func TestUnlistedInstalledVersionStaysVisible(t *testing.T) {
	// The installed unlisted version still shows, with its vulnerabilities.
	project := equalityTestProject("/w/App.csproj", map[string]string{"A": "1.0.0"}, "net8.0")
	m := equalityTestApp(project)
	versions := registrationVersions()
	versions[2].Unlisted = true
	m.ctx.Results["A"] = nugetResult{pkg: &PackageInfo{ID: "A", Versions: versions}}
	selectProject(m, project)
	m.rebuildPackageRows()
	if row := m.packages.rows[0]; !row.vulnerable || !strings.Contains(m.renderDetailVersionList(row, 60), "1.0.0") {
		t.Fatal("an unlisted installed version must stay visible and flagged")
	}
}
