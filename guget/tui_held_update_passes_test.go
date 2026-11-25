package main

import (
	"path/filepath"
	"strings"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
)

// selectAllProjectsForTest selects the All Projects entry.
func selectAllProjectsForTest(t *testing.T, m *App) {
	t.Helper()
	for i, item := range m.projects.items {
		if item.project == nil {
			m.selectProjectIndex(i)
			return
		}
	}
	t.Fatal("no All Projects entry")
}

// Across projects no update key changes a pin, so a hold must pass a row
// whose only outdated references are pins instead of resting on it.
func TestHeldUpdateAcrossProjectsPassesRowsOnlyPinsHoldBack(t *testing.T) {
	for _, key := range []rune{'u', 'U'} {
		t.Run(string(key), func(t *testing.T) {
			m, messages, path, now := heldTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[1.0.0]"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
			if key == 'u' {
				// Lowercase keys act across projects only from All Projects.
				selectAllProjectsForTest(t, m)
			}
			m.selectPackageByName("A")
			// A tap on the pinned row says why nothing changed and what
			// would change it.
			if pressHeld(m, now, key, 1) != nil || m.ctx.Saving || m.ctx.StatusIsErr ||
				m.ctx.StatusLine != "A: 1 pinned skipped; select a project to update a pin" {
				t.Fatalf("status=%q saving=%v", m.ctx.StatusLine, m.ctx.Saving)
			}
			m.held = heldUpdate{key: string(key), run: heldKeyRun, last: *now}
			save := pressHeld(m, now, key, 1)
			if save == nil || !m.ctx.Saving || selectedActionPackage(m) != "B" {
				t.Fatalf("held press did not pass the pin: selected=%s status=%q", selectedActionPackage(m), m.ctx.StatusLine)
			}
			executeActionCmd(t, m, save)
			finishActionReload(t, m, messages)
			// Nothing is left that the key can do: the hold stays where it
			// is and never returns to the pinned row as if a press could
			// resolve it.
			if pressHeld(m, now, key, 3) != nil || m.ctx.Saving || m.confirmUpdate.active || selectedActionPackage(m) != "B" ||
				!strings.Contains(m.ctx.StatusLine, "B is already at 2.0.0") {
				t.Fatalf("selected=%s status=%q", selectedActionPackage(m), m.ctx.StatusLine)
			}
			if versions := heldTestVersions(t, path); versions["A"] != "1.0.0" || versions["B"] != "2.0.0" {
				t.Fatalf("versions=%v", versions)
			}
		})
	}
}

// The other projects are at the target and one project pins an older
// version: the status must name the pin, not call the package up to date.
func TestQuickUpdateAcrossProjectsNamesAPinLeftBehind(t *testing.T) {
	m, _, path := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[1.0.0]"/></ItemGroup></Project>`)
	other := filepath.Join(filepath.Dir(path), "Other.csproj")
	mustWriteFile(t, other, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="2.0.0"/></ItemGroup></Project>`)
	snapshot, err := scanWorkspace(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	m.applyWorkspaceSnapshot(snapshot)
	selectAllProjectsForTest(t, m)
	m.selectPackageByName("A")
	const want = "A: 1 pinned skipped; select a project to update a pin"
	if cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})); cmd != nil || m.ctx.Saving || m.ctx.StatusLine != want {
		t.Fatalf("status=%q saving=%v", m.ctx.StatusLine, m.ctx.Saving)
	}
	if plan := m.planQuickUpdate(m.packages.rows[m.packages.cursor], false, scopeAll, nil); !plan.settled {
		t.Fatalf("a row only a pin keeps behind is not settled: %+v", plan)
	}
	// A pin already at the target is not reported as skipped.
	mustWriteFile(t, path, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[2.0.0]"/></ItemGroup></Project>`)
	if snapshot, err = scanWorkspace(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	m.applyWorkspaceSnapshot(snapshot)
	selectAllProjectsForTest(t, m)
	m.selectPackageByName("A")
	if cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})); cmd != nil || m.ctx.StatusLine != "A is already at 2.0.0" {
		t.Fatalf("status=%q", m.ctx.StatusLine)
	}
}
