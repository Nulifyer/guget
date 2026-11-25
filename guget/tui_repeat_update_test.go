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

// A's update waits for B's, and A is vulnerable, so by priority alone A
// would be first and stop the key. The status sort lists B just above A:
// repeated presses from the top update B, then A, then the rest.
func TestRepeatedUpdatePressesUpdateADependencyBeforeItsDependent(t *testing.T) {
	for _, view := range []string{"project", "all projects"} {
		t.Run(view, func(t *testing.T) {
			m, messages, path := newActionTestApp(t, repeatTestProject)
			versions := m.ctx.Results["A"].pkg.Versions
			versions[1].DependencyGroups = []dependencyGroup{{TargetFramework: "net8.0", Dependencies: []packageDependency{{ID: "b", Range: "[2.0.0, )"}}}}
			versions[2].Vulnerabilities = []PackageVulnerability{{AdvisoryURL: "https://example.invalid/security", Severity: 2}}
			if view == "all projects" {
				selectAllProjectsForTest(t, m)
			}
			m.rebuildPackageRows()
			m.packages.cursor = 0
			var order []string
			for _, row := range m.packages.rows {
				order = append(order, row.ref.Name)
			}
			if got := strings.Join(order, " "); got != "B A C" {
				t.Fatalf("order = %s, want B A C", got)
			}
			if a := m.packages.rows[1]; !a.vulnerable || len(a.updateRequirements) == 0 {
				t.Fatalf("A: vulnerable=%v requirements=%v", a.vulnerable, a.updateRequirements)
			}
			for _, name := range []string{"B", "A", "C"} {
				if m.packages.cursor != 0 || selectedActionPackage(m) != name {
					t.Fatalf("cursor=%d selected=%s, want %s at row 0", m.packages.cursor, selectedActionPackage(m), name)
				}
				save := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u', IsRepeat: true}))
				if save == nil || !m.ctx.Saving {
					t.Fatalf("press did not start the update of %s: status=%q", name, m.ctx.StatusLine)
				}
				executeActionCmd(t, m, save)
				finishActionReload(t, m, messages)
				if want := "✓ Updated " + name + " to 2.0.0"; !strings.Contains(m.ctx.StatusLine, want) {
					t.Fatalf("status=%q, want %q", m.ctx.StatusLine, want)
				}
			}
			if versions := repeatTestVersions(t, path); versions["A"] != "2.0.0" || versions["B"] != "2.0.0" || versions["C"] != "2.0.0" {
				t.Fatalf("versions=%v", versions)
			}
		})
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

// Before metadata arrives the rows are in name order. Metadata then re-sorts
// them. A cursor the user has not moved stays on the top row, so the first
// u acts on the package the sort put first. Once the user picks a row, a
// later re-sort follows that package.
func TestMetadataResortKeepsAnUnmovedCursorOnTheTopRow(t *testing.T) {
	m, _, _ := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Alpha" Version="1.0.0"/><PackageReference Include="Bravo" Version="1.0.0"/><PackageReference Include="Charlie" Version="1.0.0"/></ItemGroup></Project>`)
	arrive := func(name string, versions ...string) {
		t.Helper()
		info := &PackageInfo{ID: name}
		for _, version := range versions {
			info.Versions = append(info.Versions, PackageVersion{SemVer: ParseSemVer(version), Frameworks: []TargetFramework{ParseTargetFramework("net8.0")}})
		}
		m.HandleEvent(packageReadyMsg{generation: m.workspaceGeneration, name: name, result: nugetResult{pkg: info}})
		m.HandleEvent(packageRowsRefreshMsg{})
	}
	check := func(cursor int, selected, order string) {
		t.Helper()
		var names []string
		for _, row := range m.packages.rows {
			names = append(names, row.ref.Name)
		}
		if got := strings.Join(names, " "); got != order || m.packages.cursor != cursor || selectedActionPackage(m) != selected {
			t.Fatalf("order=%q cursor=%d selected=%s, want %q with %s at row %d", got, m.packages.cursor, selectedActionPackage(m), order, selected, cursor)
		}
		// A row without metadata has no detail to show yet.
		if detail := m.detail.vp.GetContent(); m.packages.rows[cursor].info != nil && !strings.Contains(detail, selected) {
			t.Fatalf("the detail panel does not show %s:\n%s", selected, detail)
		}
	}
	check(0, "Alpha", "Alpha Bravo Charlie")

	// Bravo has an update and sorts first; the cursor stays at the top.
	arrive("Bravo", "2.0.0", "1.0.0")
	check(0, "Bravo", "Bravo Alpha Charlie")
	arrive("Alpha", "1.0.0")
	check(0, "Bravo", "Bravo Alpha Charlie")

	// The user picks Alpha. Charlie's update moves it above Alpha, and the
	// cursor follows Alpha down.
	m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyDown}))
	check(1, "Alpha", "Bravo Alpha Charlie")
	arrive("Charlie", "2.0.0", "1.0.0")
	check(2, "Alpha", "Bravo Charlie Alpha")

	// Picking the top row is a choice as well: Up from row 1 selects Bravo,
	// and a re-sort follows Bravo.
	m.selectPackageByName("Charlie")
	m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyUp}))
	check(0, "Bravo", "Bravo Charlie Alpha")
	arrive("Bravo", "1.0.0")
	check(2, "Bravo", "Charlie Alpha Bravo")

	// A new sort starts at the top again.
	m.cyclePackageSort()
	m.packages.sortMode, m.packages.sortDir = sortByStatus, true
	m.refreshPackageSort()
	check(0, "Charlie", "Charlie Alpha Bravo")
	arrive("Alpha", "3.0.0", "1.0.0")
	check(0, "Alpha", "Alpha Charlie Bravo")
}
