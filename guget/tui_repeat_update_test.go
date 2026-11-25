package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
)

const repeatTestProject = `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/><PackageReference Include="B" Version="1.0.0"/><PackageReference Include="C" Version="1.0.0"/></ItemGroup></Project>`

func repeatTestVersions(t *testing.T, path string) map[string]string {
	t.Helper()
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	versions := make(map[string]string)
	for ref := range project.Packages {
		versions[ref.Name] = ref.Version.String()
	}
	return versions
}

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

// A key the user keeps pressed is a stream of ordinary presses. Under the
// status sort each updated row moves down, the next outdated package takes
// its place under the cursor, and the next press updates that one.
func TestRepeatedUpdatePressesWorkDownTheStatusSortedList(t *testing.T) {
	m, messages, path := newActionTestApp(t, repeatTestProject)
	press := func() ui.Effect { return m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u', IsRepeat: true})) }
	for _, name := range []string{"A", "B", "C"} {
		if m.packages.cursor != 0 || selectedActionPackage(m) != name {
			t.Fatalf("cursor=%d selected=%s, want %s at row 0", m.packages.cursor, selectedActionPackage(m), name)
		}
		save := press()
		if save == nil || !m.ctx.Saving {
			t.Fatalf("press did not start the update of %s: status=%q", name, m.ctx.StatusLine)
		}
		// Repeats that arrive during the save start nothing and leave the
		// status row showing the result before.
		before := m.ctx.StatusLine
		for range 3 {
			if press() != nil || m.ctx.StatusLine != before || m.ctx.StatusIsErr {
				t.Fatalf("a press during the save scheduled work or changed the status to %q", m.ctx.StatusLine)
			}
		}
		executeActionCmd(t, m, save)
		if m.ctx.Saving || m.ctx.Reloading {
			t.Fatal("the save did not reconcile without a reload")
		}
		finishActionReload(t, m, messages)
		if want := "✓ Updated " + name + " to 2.0.0"; !strings.Contains(m.ctx.StatusLine, want) {
			t.Fatalf("status=%q, want %q", m.ctx.StatusLine, want)
		}
	}
	if versions := repeatTestVersions(t, path); versions["A"] != "2.0.0" || versions["B"] != "2.0.0" || versions["C"] != "2.0.0" {
		t.Fatalf("versions=%v", versions)
	}
	// Nothing is left: further presses report it and write nothing.
	if press() != nil || m.ctx.Saving || m.packages.cursor != 0 || !strings.Contains(m.ctx.StatusLine, "is already at 2.0.0") {
		t.Fatalf("cursor=%d status=%q", m.packages.cursor, m.ctx.StatusLine)
	}
}

// A press during a reload is dropped like one during a save: the footer
// shows "reloading..." in place of the status row.
func TestUpdatePressDuringReloadLeavesTheStatusRow(t *testing.T) {
	m, _, _ := newActionTestApp(t, repeatTestProject)
	m.ctx.Reloading = true
	m.ctx.StatusLine = "last result"
	if m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'})) != nil || m.ctx.Saving || m.ctx.StatusLine != "last result" {
		t.Fatalf("saving=%v status=%q", m.ctx.Saving, m.ctx.StatusLine)
	}
	if footer := m.renderFooter(); !strings.Contains(footer, "reloading...") {
		t.Fatalf("footer does not explain the dropped press:\n%s", footer)
	}
}

// A row the key cannot update stops the progress there: repeated presses
// stay on it with its reason and touch no other row.
func TestRepeatedUpdatePressesStayOnARowTheyCannotUpdate(t *testing.T) {
	m, _, path := newActionTestApp(t, repeatTestProject)
	m.ctx.Results["A"] = nugetResult{pkg: &PackageInfo{ID: "A", Versions: []PackageVersion{
		{SemVer: ParseSemVer("9.0.0"), Frameworks: []TargetFramework{ParseTargetFramework("net9.0")}},
	}}}
	m.rebuildPackageRows()
	m.selectPackageByName("A")
	for range 5 {
		if m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u', IsRepeat: true})) != nil || m.ctx.Saving || selectedActionPackage(m) != "A" ||
			!strings.Contains(m.ctx.StatusLine, "No compatible stable version for A") || !m.ctx.StatusIsErr {
			t.Fatalf("selected=%s status=%q", selectedActionPackage(m), m.ctx.StatusLine)
		}
	}
	if versions := repeatTestVersions(t, path); versions["A"] != "1.0.0" || versions["B"] != "1.0.0" || versions["C"] != "1.0.0" {
		t.Fatalf("versions=%v", versions)
	}
}

// A failed save leaves the cursor on its row, and the next press tries again.
func TestUpdatePressAfterAFailedSaveRetriesTheSameRow(t *testing.T) {
	m, _, _ := newActionTestApp(t, repeatTestProject)
	m.selectPackageByName("A")
	for range 2 {
		if m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'})) == nil || !m.ctx.Saving {
			t.Fatalf("press did not start a save: %q", m.ctx.StatusLine)
		}
		m.HandleEvent(writeResultMsg{err: errors.New("boom")})
		if m.ctx.Saving || selectedActionPackage(m) != "A" || !strings.Contains(m.ctx.StatusLine, "Save failed: boom") || !m.ctx.StatusIsErr {
			t.Fatalf("selected=%s status=%q", selectedActionPackage(m), m.ctx.StatusLine)
		}
	}
}

// u on a pinned reference asks first. The repeats of the same key land on
// the dialog and must neither confirm it nor reach the list behind it.
func TestRepeatedUpdatePressesNeverConfirmAPin(t *testing.T) {
	for _, key := range []rune{'u', 'a'} {
		t.Run(string(key), func(t *testing.T) {
			m, _, path := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[1.0.0]"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			m.selectPackageByName("A")
			for range 10 {
				if m.HandleEvent(ui.KeyPress(ui.Key{Code: key, IsRepeat: true})) != nil {
					t.Fatal("a press on the pinned row or its dialog scheduled work")
				}
				if !m.confirmUpdate.active || m.confirmUpdate.pkgName != "A" || m.ctx.Saving || selectedActionPackage(m) != "A" {
					t.Fatalf("confirm=%v saving=%v selected=%s", m.confirmUpdate.active, m.ctx.Saving, selectedActionPackage(m))
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("project changed without a confirmation: %s", after)
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
	for _, key := range []rune{'u', 'U'} {
		if cmd := m.handleKey(ui.KeyPress(ui.Key{Code: key})); cmd != nil || m.ctx.Saving || m.ctx.StatusIsErr || m.ctx.StatusLine != want {
			t.Fatalf("%c: status=%q saving=%v", key, m.ctx.StatusLine, m.ctx.Saving)
		}
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
