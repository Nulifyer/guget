package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
)

func TestUpdateRequirementsVisibleBeforeActionAndClearAfterDependencyEdit(t *testing.T) {
	m, path := preflightTestApp(t)
	m.selectPackageByName("A")
	row := m.packages.rows[m.packages.cursor]
	if !strings.Contains(availableVersionText(row), "2.0.0 [deps]") {
		t.Fatal("candidate has no dependency warning before an update is attempted")
	}
	detail := m.renderDetail(row)
	for _, want := range []string{"Update requirements", "For 2.0.0:", "App.csproj", "B >=2.0.0", "requested 1.0.0", "dependencies first"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("missing requirement %q: %s", want, detail)
		}
	}
	if strings.Contains(detail, "Version pinned") {
		t.Fatal("dependency requirement was presented as a version pin")
	}
	before, _ := os.ReadFile(path)
	if cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'})); cmd != nil || m.ctx.Saving {
		t.Fatal("known unmet dependency did not stop the quick update")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("refusal changed the project")
	}
	executeActionCmd(t, m, m.applyVersion("B", "2.0.0", m.selectedProject()))
	m.selectPackageByName("A")
	row = m.packages.rows[m.packages.cursor]
	if strings.Contains(availableVersionText(row), "[deps]") || strings.Contains(m.renderDetail(row), "Update requirements") {
		t.Fatal("resolved requirement remained visible")
	}
	executeActionCmd(t, m, m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'})))
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `Include="A" Version="2.0.0"`) {
		t.Fatal("update did not succeed after fixing the dependency")
	}
}

func TestUpdateRequirementsIdentifyAffectedProjectsAndPins(t *testing.T) {
	for _, scope := range []string{"all", "selected independent", "selected shared", "pinned other"} {
		t.Run(scope, func(t *testing.T) {
			m, path := preflightTestApp(t)
			first := m.selectedProject()
			first.Packages = Set[PackageReference]{{Name: "A", Version: ParseSemVer("1.0.0")}: {}}
			second := &ParsedProject{FileName: "Other.csproj", FilePath: filepath.Join(filepath.Dir(path), "Other.csproj"), TargetFrameworks: NewSet[TargetFramework]()}
			second.TargetFrameworks.Add(ParseTargetFramework("net8.0"))
			second.Packages = Set[PackageReference]{{Name: "A", Version: ParseSemVer("1.0.0"), Requested: "1.0.0"}: {}, {Name: "B", Version: ParseSemVer("1.0.0")}: {}}
			if scope == "pinned other" {
				second.Packages = Set[PackageReference]{{Name: "A", Version: ParseSemVer("1.0.0"), Requested: "[1.0.0]", Locked: true}: {}, {Name: "B", Version: ParseSemVer("1.0.0")}: {}}
			}
			m.ctx.ParsedProjects = append(m.ctx.ParsedProjects, second)
			if scope == "selected shared" {
				owner := filepath.Join(filepath.Dir(path), "shared.props")
				first.PackageSources = map[string]string{"a": owner}
				second.PackageSources = map[string]string{"a": owner}
			}
			if scope == "all" || scope == "pinned other" {
				m.projects.cursor = 0
			}
			m.rebuildPackageRows()
			m.selectPackageByName("A")
			row := m.packages.rows[m.packages.cursor]
			wantConflict := scope == "all" || scope == "selected shared"
			if (len(row.updateRequirements) > 0) != wantConflict {
				t.Fatalf("requirements=%+v, want conflict=%v", row.updateRequirements, wantConflict)
			}
			detail := m.renderDetail(row)
			if wantConflict && (!strings.Contains(detail, "Other.csproj") || !strings.Contains(detail, "B >=2.0.0")) {
				t.Fatalf("affected consumer is missing: %s", detail)
			}
			if scope == "pinned other" && !strings.Contains(text.Strip(m.renderDetailProjectVersions(row)), "[1.0.0] pinned") {
				t.Fatal("combined view hides the other project's pin")
			}
		})
	}
}

func TestSelectedPinIsVisibleAndStillRequiresConfirmation(t *testing.T) {
	m, _, path := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[1.0.0]"/></ItemGroup></Project>`)
	row := m.packages.rows[0]
	if !strings.Contains(currentVersionText(row), "[1.0.0] pin") || !strings.Contains(m.renderDetail(row), "Version pinned") {
		t.Fatal("selected project pin is not explained before updating")
	}
	before, _ := os.ReadFile(path)
	if effect := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'})); effect != nil || !m.confirmUpdate.active || m.ctx.Saving {
		t.Fatal("pin indication changed confirmation behavior")
	}
	m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyEscape}))
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("canceling the pinned update changed its declaration")
	}
}

func TestUpdateIndicatorFrames(t *testing.T) {
	for _, theme := range []string{"auto-dark", "auto-light"} {
		for _, width := range []int{180, 120, 80} {
			for _, state := range []string{"selected", "all", "pinned"} {
				t.Run(fmt.Sprintf("%s-%s-%d", theme, state, width), func(t *testing.T) {
					initTheme(theme, false)
					m, _ := preflightTestApp(t)
					if state == "all" {
						m.projects.cursor = 0
					} else if state == "pinned" {
						m.selectedProject().Packages = Set[PackageReference]{{Name: "A", Version: ParseSemVer("1.0.0"), Requested: "[1.0.0]", Locked: true}: {}}
					}
					m.HandleEvent(ui.ResizeEvent{Width: width, Height: 30})
					m.rebuildPackageRows()
					m.selectPackageByName("A")
					m.refreshDetail()
					frame := m.Render().Content
					lines := strings.Split(frame, "\n")
					if len(lines) > 30 {
						t.Fatal("indicator frame exceeds terminal height")
					}
					for _, line := range lines {
						if text.StringWidth(line) > width {
							t.Fatal("indicator frame wraps beyond terminal width")
						}
					}
					if dir := os.Getenv("GUGET_VISUAL_DIR"); dir != "" {
						if err := os.MkdirAll(dir, 0o755); err != nil {
							t.Fatal(err)
						}
						name := fmt.Sprintf("%s-%s-%dx30.ansi", theme, state, width)
						if err := os.WriteFile(filepath.Join(dir, name), []byte(frame), 0o644); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

// Keep proactive warnings inexpensive when many projects share package IDs.
func BenchmarkUpdateIndicatorsLargeWorkspace(b *testing.B) {
	m := &App{ctx: &AppContext{Results: make(map[string]nugetResult)}}
	for i := range 100 {
		project := &ParsedProject{
			FilePath:         fmt.Sprintf("/workspace/Project%d.csproj", i),
			Packages:         NewSet[PackageReference](),
			TargetFrameworks: NewSet[TargetFramework](),
		}
		project.TargetFrameworks.Add(ParseTargetFramework("net8.0"))
		for j := range 100 {
			project.Packages.Add(PackageReference{Name: fmt.Sprintf("Package%d", j), Version: ParseSemVer("1.0.0")})
		}
		m.ctx.ParsedProjects = append(m.ctx.ParsedProjects, project)
	}
	for j := range 100 {
		id := fmt.Sprintf("Package%d", j)
		m.ctx.Results[id] = nugetResult{pkg: &PackageInfo{ID: id, Versions: []PackageVersion{
			{SemVer: ParseSemVer("2.0.0"), DependencyGroups: []dependencyGroup{{TargetFramework: "net8.0", Dependencies: []packageDependency{{ID: fmt.Sprintf("Package%d", (j+1)%100), Range: "[2.0.0,)"}}}}},
			{SemVer: ParseSemVer("1.0.0")},
		}}}
	}
	b.ResetTimer()
	for b.Loop() {
		m.rebuildPackageRows()
	}
}
