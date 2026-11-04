package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
)

func preflightTestApp(t *testing.T) (*App, string) {
	t.Helper()
	m, _, path := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
	for i := range m.ctx.Results["A"].pkg.Versions {
		m.ctx.Results["A"].pkg.Versions[i].DependencyGroups = []dependencyGroup{{TargetFramework: "net8.0", Dependencies: []packageDependency{{ID: "B", Range: "[2.0.0, )"}}}}
	}
	m.rebuildPackageRows()
	return m, path
}

func TestCompatibleQuickUpdatePreflightsDirectDependency(t *testing.T) {
	m, path := preflightTestApp(t)
	before, _ := os.ReadFile(path)
	if cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'})); cmd != nil || m.ctx.Saving {
		t.Fatal("known direct downgrade started a write")
	}
	for _, text := range []string{"Update B:", "1.0.0 -> >=2.0.0", "v/a override"} {
		if !m.ctx.StatusIsErr || !strings.Contains(m.renderFooter(), text) {
			t.Fatalf("missing actionable conflict %q: %q", text, m.renderFooter())
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("preflight refusal changed the project")
	}
	// Updating the dependency first must allow the original quick update.
	executeActionCmd(t, m, m.applyVersion("B", "2.0.0", m.selectedProject()))
	m.selectPackageByName("A")
	cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'}))
	if cmd == nil || !m.ctx.Saving {
		t.Fatalf("compatible update still refused: %q", m.ctx.StatusLine)
	}
	executeActionCmd(t, m, cmd)
	updated, _ := os.ReadFile(path)
	for _, reference := range []string{`Include="A" Version="2.0.0"`, `Include="B" Version="2.0.0"`} {
		if !strings.Contains(string(updated), reference) {
			t.Fatalf("missing updated reference %q in %s", reference, updated)
		}
	}
}

func TestExplicitUpdatesBypassCompatiblePreflight(t *testing.T) {
	for _, key := range []rune{'a', 'A', 'v'} {
		t.Run(string(key), func(t *testing.T) {
			m, path := preflightTestApp(t)
			var cmd ui.Effect
			want := "3.0.0"
			if key == 'v' {
				want = "2.0.0"
				cmd = m.applyOrConfirmUpdate("A", want, m.selectedProject())
			} else {
				cmd = m.HandleEvent(ui.KeyPress(ui.Key{Code: key}))
			}
			if cmd == nil || !m.ctx.Saving {
				t.Fatal("explicit update was refused")
			}
			executeActionCmd(t, m, cmd)
			updated, _ := os.ReadFile(path)
			if !strings.Contains(string(updated), `Include="A" Version="`+want+`"`) {
				t.Fatalf("explicit version not written: %s", updated)
			}
		})
	}
}

func TestAllProjectsQuickUpdatePreflightsDirectDependency(t *testing.T) {
	m, _ := preflightTestApp(t)
	if cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'U'})); cmd != nil || m.ctx.Saving || !m.ctx.StatusIsErr || !strings.Contains(m.ctx.StatusLine, "Update B:") {
		t.Fatalf("all-project update did not refuse known downgrade: %q", m.ctx.StatusLine)
	}
}

func TestQuickUpdatePreflightDependencyGroups(t *testing.T) {
	group := func(framework, expression string) dependencyGroup {
		g := dependencyGroup{TargetFramework: framework}
		if expression != "" {
			g.Dependencies = []packageDependency{{ID: "b", Range: expression}}
		}
		return g
	}
	tests := []struct {
		name      string
		groups    []dependencyGroup
		requested string
		target    string
		conflict  bool
	}{
		{"exact group wins", []dependencyGroup{group("net8.0", "[2.0.0,)"), group(".NETStandard2.0", "1.0.0")}, "1.0.0", "net8.0", true},
		{"exact group has no dependency", []dependencyGroup{group("net8.0", ""), group(".NETStandard2.0", "[2.0.0,)")}, "1.0.0", "net8.0", false},
		{"all compatible groups conflict", []dependencyGroup{group(".NETStandard2.0", "[2.0.0,)"), group(".NETStandard2.1", "[3.0.0,)")}, "1.0.0", "net8.0", true},
		{"one compatible group permits", []dependencyGroup{group(".NETStandard2.0", "[2.0.0,)"), group(".NETStandard2.1", "1.0.0")}, "1.0.0", "net8.0", false},
		{"incompatible group ignored", []dependencyGroup{group("net9.0", "[2.0.0,)"), group(".NETStandard2.0", "1.0.0")}, "1.0.0", "net8.0", false},
		{"unknown group is ambiguous", []dependencyGroup{group("net8.0-windows", ""), group(".NETStandard2.0", "[2.0.0,)")}, "1.0.0", "net8.0", false},
		{"unknown target", []dependencyGroup{group("", "[2.0.0,)")}, "1.0.0", "$(Framework)", false},
		{"no dependency metadata", nil, "1.0.0", "net8.0", false},
		{"exact direct request", []dependencyGroup{group("net8.0", "[2.0.0,)")}, "[1.0.0]", "net8.0", true},
		{"higher direct request", []dependencyGroup{group("net8.0", "[2.0.0,)")}, "3.0.0", "net8.0", false},
		{"matching inclusive bound", []dependencyGroup{group("net8.0", "[1.0.0,)")}, "1.0.0", "net8.0", false},
		{"matching exclusive bound", []dependencyGroup{group("net8.0", "(1.0.0,2.0.0]")}, "1.0.0", "net8.0", true},
		{"floating direct request", []dependencyGroup{group("net8.0", "[2.0.0,)")}, "1.*", "net8.0", false},
		{"range direct request", []dependencyGroup{group("net8.0", "[2.0.0,)")}, "[1.0.0,)", "net8.0", false},
		{"property direct request", []dependencyGroup{group("net8.0", "[2.0.0,)")}, "$(Version)", "net8.0", false},
		{"invalid dependency range", []dependencyGroup{group("net8.0", "[3.0.0,2.0.0)")}, "1.0.0", "net8.0", false},
		{"empty dependency range", []dependencyGroup{group("net8.0", "(2.0.0,2.0.0)")}, "1.0.0", "net8.0", false},
		{"unbounded lower range", []dependencyGroup{group("net8.0", "(,2.0.0]")}, "1.0.0", "net8.0", false},
		{"floating dependency", []dependencyGroup{group("net8.0", "2.*")}, "1.0.0", "net8.0", false},
		{"duplicate dependency metadata", []dependencyGroup{{TargetFramework: "net8.0", Dependencies: []packageDependency{{ID: "B", Range: "[2.0.0,)"}, {ID: "b", Range: "[3.0.0,)"}}}}, "1.0.0", "net8.0", false},
		{"prerelease lower bound", []dependencyGroup{group("net8.0", "[2.0.0-rc.2,)")}, "2.0.0-rc.1", "net8.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := preflightTestApp(t)
			p := m.selectedProject()
			p.Packages = Set[PackageReference]{{Name: "A", Version: ParseSemVer("1.0.0")}: {}, {Name: "B", Version: ParseSemVer(tt.requested), Requested: tt.requested}: {}}
			p.TargetFrameworks = Set[TargetFramework]{ParseTargetFramework(tt.target): {}}
			candidate := &PackageVersion{SemVer: ParseSemVer("2.0.0"), DependencyGroups: tt.groups}
			got := m.quickUpdateDependencyConflict("A", candidate, scopeSelected)
			if (got != "") != tt.conflict {
				t.Fatalf("conflict=%q, want conflict=%v", got, tt.conflict)
			}
		})
	}
}

func TestQuickUpdatePreflightScopesAndSharedConsumers(t *testing.T) {
	for _, scope := range []string{"selected independent", "all", "selected shared", "locked independent"} {
		t.Run(scope, func(t *testing.T) {
			m, path := preflightTestApp(t)
			first := m.selectedProject()
			first.Packages = Set[PackageReference]{{Name: "A", Version: ParseSemVer("1.0.0")}: {}}
			second := &ParsedProject{FilePath: filepath.Join(filepath.Dir(path), "Other.csproj"), TargetFrameworks: Set[TargetFramework]{ParseTargetFramework("net8.0"): {}}, Packages: Set[PackageReference]{{Name: "A", Version: ParseSemVer("1.0.0"), Locked: scope == "locked independent"}: {}, {Name: "B", Version: ParseSemVer("1.0.0")}: {}}}
			m.ctx.ParsedProjects = append(m.ctx.ParsedProjects, second)
			if scope == "selected shared" {
				owner := filepath.Join(filepath.Dir(path), "shared.props")
				first.PackageSources = map[string]string{"a": owner}
				second.PackageSources = map[string]string{"a": owner}
			}
			action := scopeSelected
			if scope == "all" || scope == "locked independent" {
				action = scopeAll
			}
			candidate := &m.ctx.Results["A"].pkg.Versions[1]
			conflict := m.quickUpdateDependencyConflict("A", candidate, action)
			want := scope == "all" || scope == "selected shared"
			if (conflict != "") != want {
				t.Fatalf("conflict=%q want=%v", conflict, want)
			}
		})
	}
}

func TestQuickUpdatePreflightAmbiguousDirectDeclarations(t *testing.T) {
	m, _ := preflightTestApp(t)
	m.selectedProject().Packages.Add(PackageReference{Name: "b", Version: ParseSemVer("3.0.0")})
	if conflict := m.quickUpdateDependencyConflict("A", &m.ctx.Results["A"].pkg.Versions[1], scopeSelected); conflict != "" {
		t.Fatalf("ambiguous declarations treated as known: %q", conflict)
	}
}
