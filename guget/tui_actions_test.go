package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
)

func newActionTestApp(t *testing.T, content string) (*App, chan ui.Event, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "App.csproj")
	if content == "" {
		content = `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`
	}
	mustWriteFile(t, path, content)
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="local" value="./packages"/></packageSources></configuration>`)
	snapshot, err := scanWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewApp(root, snapshot, nil, BuiltFlags{SortBy: "status:asc"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.lifecycle = ctx
	m.ctx.Width, m.ctx.Height = 140, 40
	m.focus = focusPackages
	m.projects.cursor = 1
	for _, id := range []string{"A", "B", "C"} {
		m.ctx.Results[id] = nugetResult{pkg: &PackageInfo{ID: id, Versions: []PackageVersion{
			{SemVer: ParseSemVer("3.0.0"), Frameworks: []TargetFramework{ParseTargetFramework("net9.0")}},
			{SemVer: ParseSemVer("2.0.0"), Frameworks: []TargetFramework{ParseTargetFramework("net8.0")}},
			{SemVer: ParseSemVer("1.0.0")},
		}}}
	}
	m.rebuildPackageRows()
	m.refreshDetail()
	messages := make(chan ui.Event, 30)
	m.SetSender(func(msg ui.Event) { messages <- msg })
	return m, messages, path
}

func executeActionCmd(t *testing.T, m *App, cmd ui.Effect) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(ui.Effects); ok {
		for _, cmd := range batch {
			executeActionCmd(t, m, cmd)
		}
		return
	}
	next := m.HandleEvent(msg)
	executeActionCmd(t, m, next)
}

func finishActionReload(t *testing.T, m *App, messages <-chan ui.Event) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for m.ctx.Reloading {
		select {
		case msg := <-messages:
			cmd := m.HandleEvent(msg)
			executeActionCmd(t, m, cmd)
		case <-deadline.C:
			t.Fatal("reload did not finish")
		}
	}
}

func selectedActionPackage(m *App) string { return m.packageSelection().name }

func TestHeldQuickUpdatesProcessEveryPackage(t *testing.T) {
	for _, key := range []rune{'u', 'U', 'a', 'A'} {
		t.Run(string(key), func(t *testing.T) {
			m, messages, path := newActionTestApp(t, "")
			wantVersion := "2.0.0"
			if key == 'a' || key == 'A' {
				wantVersion = "3.0.0"
			}
			for _, name := range []string{"A", "B"} {
				if selectedActionPackage(m) != name {
					t.Fatalf("selected=%s, want %s", selectedActionPackage(m), name)
				}
				cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: key, IsRepeat: true}))
				if cmd == nil || !m.ctx.Saving {
					t.Fatal("update did not start")
				}
				if m.handleKey(ui.KeyPress(ui.Key{Code: key, IsRepeat: true})) != nil {
					t.Fatal("repeat scheduled an overlapping write")
				}
				executeActionCmd(t, m, cmd)
				if m.ctx.Saving || m.ctx.Reloading {
					t.Fatal("successful write did not reconcile locally")
				}
				finishActionReload(t, m, messages)
				if m.packages.cursor != 0 {
					t.Fatalf("cursor=%d, want row 0", m.packages.cursor)
				}
			}
			project, err := ParseCsproj(path)
			if err != nil {
				t.Fatal(err)
			}
			for ref := range project.Packages {
				if ref.Version.String() != wantVersion {
					t.Fatalf("%s=%s, want %s", ref.Name, ref.Version.String(), wantVersion)
				}
			}
			if m.handleKey(ui.KeyPress(ui.Key{Code: key, IsRepeat: true})) != nil || m.ctx.Saving || m.ctx.Reloading {
				t.Fatal("up-to-date package triggered work")
			}
		})
	}
}

func TestMutationSelectionPolicies(t *testing.T) {
	t.Run("add selects new package", func(t *testing.T) {
		m, messages, _ := newActionTestApp(t, "")
		executeActionCmd(t, m, m.addPackageToProject("C", "1.0.0", m.selectedProject()))
		finishActionReload(t, m, messages)
		if selectedActionPackage(m) != "C" {
			t.Fatalf("selected=%s", selectedActionPackage(m))
		}
	})
	t.Run("remove keeps neighboring row", func(t *testing.T) {
		m, messages, _ := newActionTestApp(t, `<Project><ItemGroup><PackageReference Include="A" Version="1.0.0"/><PackageReference Include="B" Version="1.0.0"/><PackageReference Include="C" Version="1.0.0"/></ItemGroup></Project>`)
		executeActionCmd(t, m, m.removePackage("A"))
		finishActionReload(t, m, messages)
		if selectedActionPackage(m) != "B" || m.packages.cursor != 0 {
			t.Fatalf("selected=%s cursor=%d", selectedActionPackage(m), m.packages.cursor)
		}
	})
	t.Run("remove last package", func(t *testing.T) {
		m, messages, _ := newActionTestApp(t, `<Project><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
		executeActionCmd(t, m, m.removePackage("A"))
		finishActionReload(t, m, messages)
		if len(m.packages.rows) != 0 || m.packages.cursor != 0 {
			t.Fatalf("rows=%v cursor=%d", m.packages.rows, m.packages.cursor)
		}
	})
	t.Run("version picker preserves identity", func(t *testing.T) {
		m, messages, _ := newActionTestApp(t, "")
		m.openVersionPicker()
		m.picker.cursor = 1
		executeActionCmd(t, m, m.picker.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEnter})))
		finishActionReload(t, m, messages)
		if selectedActionPackage(m) != "A" || m.packages.cursor != 1 {
			t.Fatalf("selected=%s cursor=%d", selectedActionPackage(m), m.packages.cursor)
		}
	})
	t.Run("navigation during save wins", func(t *testing.T) {
		m, messages, _ := newActionTestApp(t, "")
		cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'}))
		m.movePackageCursor(1)
		executeActionCmd(t, m, cmd)
		finishActionReload(t, m, messages)
		if selectedActionPackage(m) != "B" {
			t.Fatalf("selected=%s", selectedActionPackage(m))
		}
	})
	t.Run("ordinary reload preserves identity", func(t *testing.T) {
		m, messages, path := newActionTestApp(t, "")
		if err := UpdatePackageVersion(path, "A", "2.0.0"); err != nil {
			t.Fatal(err)
		}
		m.requestReload(reloadRequestedMsg{reason: "manual reload"})
		finishActionReload(t, m, messages)
		if selectedActionPackage(m) != "A" || m.packages.cursor != 1 {
			t.Fatalf("selected=%s cursor=%d", selectedActionPackage(m), m.packages.cursor)
		}
	})
}

func TestFailedOrNoOpWritesReleaseSavingState(t *testing.T) {
	m, _, path := newActionTestApp(t, "")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	executeActionCmd(t, m, m.applyVersion("A", "$(Unsupported)", m.selectedProject()))
	if m.ctx.Saving || m.ctx.Reloading || selectedActionPackage(m) != "A" || !m.ctx.StatusIsErr {
		t.Fatalf("state=%+v", m.ctx)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("failed write changed project")
	}
	executeActionCmd(t, m, m.applyVersion("A", "1.0.0", m.selectedProject()))
	if m.ctx.Saving || m.ctx.Reloading {
		t.Fatal("no-op write did not release saving state")
	}
	// A failed completion must also release a queued watcher reload.
	m.ctx.Saving = true
	m.HandleEvent(writeResultMsg{err: errors.New("injected save failure")})
	if m.ctx.Saving || !strings.Contains(m.ctx.StatusLine, "injected save failure") {
		t.Fatal("failure state not reported")
	}
}

func TestWatcherReloadWaitsForMutation(t *testing.T) {
	m, messages, _ := newActionTestApp(t, "")
	cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'}))
	m.requestReload(reloadRequestedMsg{automatic: true, paths: []string{"App.csproj"}})
	if m.ctx.Reloading || !m.hasPendingReload || m.workspaceGeneration != 0 {
		t.Fatal("watcher reloaded during a write")
	}
	executeActionCmd(t, m, cmd)
	finishActionReload(t, m, messages)
	if selectedActionPackage(m) != "B" || m.hasPendingReload || m.ctx.Saving {
		t.Fatalf("selected=%s state=%+v", selectedActionPackage(m), m.ctx)
	}
}

func TestQuickUpdatePinnedVersionRequiresConfirmation(t *testing.T) {
	m, messages, path := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[1.0.0]"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
	if cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})); cmd != nil || !m.confirmUpdate.active || m.ctx.Saving {
		t.Fatal("pinned update did not require confirmation")
	}
	// A repeated u cannot accept the pin confirmation.
	cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u', IsRepeat: true}))
	executeActionCmd(t, m, cmd)
	if !m.confirmUpdate.active || m.ctx.Saving {
		t.Fatal("repeat accepted confirmation")
	}
	cmd = m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyEnter}))
	executeActionCmd(t, m, cmd)
	finishActionReload(t, m, messages)
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `Version="[2.0.0]"`) || selectedActionPackage(m) != "B" {
		t.Fatalf("selected=%s project=%s", selectedActionPackage(m), data)
	}
}

func TestKeyReleasesDoNotNavigateOrMutate(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	for _, key := range []ui.Key{{Code: ui.KeyDown}, {Code: 'u'}, {Code: 'o'}, {Code: ui.KeyTab}, {Code: 'd'}} {
		selection := m.packageSelection()
		focus := m.focus
		cmd := m.HandleEvent(ui.KeyRelease(key))
		if cmd != nil || m.packageSelection() != selection || m.focus != focus || m.ctx.Saving || m.confirmRemove.active {
			t.Fatalf("release caused action: %+v", key)
		}
	}
	m.openVersionPicker()
	m.picker.cursor = 1
	cmd := m.HandleEvent(ui.KeyRelease(ui.Key{Code: ui.KeyEnter}))
	if cmd != nil || !m.picker.active || m.ctx.Saving {
		t.Fatal("release applied version picker")
	}
}

func TestGlobalInterruptAndModalEscape(t *testing.T) {
	for _, overlay := range []string{"help", "picker", "search", "confirm"} {
		t.Run(overlay, func(t *testing.T) {
			m, _, _ := newActionTestApp(t, "")
			switch overlay {
			case "help":
				m.help.active = true
			case "picker":
				m.openVersionPicker()
			case "search":
				m.search.active = true
			case "confirm":
				m.confirmRemove = newConfirmRemove(m, "A")
			}
			cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'c', Mod: ui.ModCtrl}))
			if cmd == nil {
				t.Fatal("ctrl+c swallowed by overlay")
			}
			if _, ok := cmd().(ui.QuitEvent); !ok {
				t.Fatal("ctrl+c did not quit")
			}
			cmd = m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyEscape}))
			if cmd != nil || m.anyOverlayActive() {
				t.Fatal("escape did not close modal")
			}
		})
	}
}

func TestSortAndNavigationAliases(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.handleKey(ui.KeyPress(ui.Key{Code: 'j'}))
	if selectedActionPackage(m) != "B" {
		t.Fatal("j did not navigate")
	}
	m.handleKey(ui.KeyPress(ui.Key{Code: 'o'}))
	if m.packages.sortMode != sortByName || m.packages.cursor != 0 {
		t.Fatal("o did not reset name sort")
	}
	m.handleKey(ui.KeyPress(ui.Key{Code: 'O'}))
	if m.packages.sortDir || selectedActionPackage(m) != "B" {
		t.Fatal("O did not reverse name sort")
	}
	m.handleKey(ui.KeyPress(ui.Key{Code: 'j'}))
	m.handleKey(ui.KeyPress(ui.Key{Code: 'k'}))
	if selectedActionPackage(m) != "B" {
		t.Fatal("k did not navigate")
	}
	m.help.refreshView()
	if !strings.Contains(m.help.vp.GetContent(), "keep row") {
		t.Fatal("quick-update help omits cursor behavior")
	}
}

func TestMetadataRepliesPreserveSelectedPackage(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.movePackageCursor(1)
	cmd := m.HandleEvent(packageReadyMsg{generation: m.workspaceGeneration, name: "A", result: nugetResult{pkg: &PackageInfo{ID: "A", Versions: []PackageVersion{{SemVer: ParseSemVer("1.0.0")}}}}})
	executeActionCmd(t, m, cmd)
	if selectedActionPackage(m) != "B" || m.packages.cursor != 0 {
		t.Fatalf("selected=%s cursor=%d", selectedActionPackage(m), m.packages.cursor)
	}
}

func TestTUIBulkAddUsesOneCentralTransaction(t *testing.T) {
	m, messages, first := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
	root := filepath.Dir(first)
	central := filepath.Join(root, "Directory.Packages.props")
	second := filepath.Join(root, "Second.csproj")
	mustWriteFile(t, central, `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup></Project>`)
	mustWriteFile(t, second, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
	snapshot, err := scanWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	m.applyWorkspaceSnapshot(snapshot)
	m.projects.cursor = 0
	items := make([]projectPickItem, 0, len(snapshot.ParsedProjects))
	for _, project := range snapshot.ParsedProjects {
		items = append(items, projectPickItem{project: project, selected: true})
	}
	m.projectPick = projectPicker{sectionBase: sectionBase{app: m, active: true}, pkgName: "C", version: "2.0.0", items: items}
	cmd := m.projectPick.confirmSelection()
	if cmd == nil || !m.ctx.Saving {
		t.Fatal("bulk add did not start")
	}
	msg := cmd()
	result := actionWriteResult(t, msg)
	if result.err != nil || result.written != 3 {
		t.Fatalf("bulk result=%+v", result)
	}
	m.HandleEvent(msg)
	finishActionReload(t, m, messages)
	data, _ := os.ReadFile(central)
	if strings.Count(string(data), "PackageVersion Include=") != 1 {
		t.Fatalf("central=%s", data)
	}
	for _, path := range []string{first, second} {
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), `PackageReference Include="C" />`) {
			t.Fatalf("project=%s", data)
		}
	}
	if selectedActionPackage(m) != "C" {
		t.Fatalf("selected=%s", selectedActionPackage(m))
	}
}

func TestBulkUpdateSkipsLockedProjects(t *testing.T) {
	m, messages, path := newActionTestApp(t, "")
	locked := filepath.Join(filepath.Dir(path), "Locked.csproj")
	mustWriteFile(t, locked, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[1.0.0]"/></ItemGroup></Project>`)
	snapshot, err := scanWorkspace(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	m.applyWorkspaceSnapshot(snapshot)
	m.projects.cursor = 0
	m.rebuildPackageRows()
	cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'U'}))
	if cmd == nil {
		t.Fatal("no workspace update command")
	}
	msg := cmd()
	result := actionWriteResult(t, msg)
	if result.err != nil || result.skipped != 1 {
		t.Fatalf("result=%+v", result)
	}
	m.HandleEvent(msg)
	finishActionReload(t, m, messages)
	data, _ := os.ReadFile(locked)
	if !strings.Contains(string(data), `Version="[1.0.0]"`) {
		t.Fatal("changed pinned project")
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), `Include="A" Version="2.0.0"`) {
		t.Fatalf("project=%s", data)
	}
}

func TestQuickUpdateScopesAndAllLockedWarning(t *testing.T) {
	t.Run("selected versus all projects", func(t *testing.T) {
		m, messages, path := newActionTestApp(t, "")
		other := filepath.Join(filepath.Dir(path), "Other.csproj")
		mustWriteFile(t, other, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
		snapshot, err := scanWorkspace(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		m.applyWorkspaceSnapshot(snapshot)
		m.selectProjectByPath(path)
		m.rebuildPackageRows()
		executeActionCmd(t, m, m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})))
		finishActionReload(t, m, messages)
		data, _ := os.ReadFile(other)
		if !strings.Contains(string(data), `Version="1.0.0"`) {
			t.Fatal("lowercase u changed another project")
		}
		m.selectPackageByName("A")
		executeActionCmd(t, m, m.handleKey(ui.KeyPress(ui.Key{Code: 'U'})))
		finishActionReload(t, m, messages)
		data, _ = os.ReadFile(other)
		if !strings.Contains(string(data), `Version="2.0.0"`) {
			t.Fatal("uppercase U did not update another project")
		}
	})
	t.Run("all locked", func(t *testing.T) {
		m, _, _ := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[1.0.0]"/></ItemGroup></Project>`)
		if cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'U'})); cmd != nil || m.ctx.Saving || !strings.Contains(m.ctx.StatusLine, "1 skipped") {
			t.Fatalf("status=%q saving=%v", m.ctx.StatusLine, m.ctx.Saving)
		}
	})
}

func TestSortingDuringSavePreservesLaterSelection(t *testing.T) {
	m, messages, _ := newActionTestApp(t, "")
	cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'}))
	m.handleKey(ui.KeyPress(ui.Key{Code: 'o'}))
	executeActionCmd(t, m, cmd)
	finishActionReload(t, m, messages)
	if m.packages.sortMode != sortByName || selectedActionPackage(m) != "A" {
		t.Fatalf("sort=%v selected=%s", m.packages.sortMode, selectedActionPackage(m))
	}
}

func TestFailedWriteReloadPreservesSelectionAndError(t *testing.T) {
	m, messages, path := newActionTestApp(t, "")
	selection := m.rowReloadSelection()
	// A failed rollback can leave a changed file. Reload disk state without
	// treating the failed action as a successful quick update.
	if err := UpdatePackageVersion(path, "A", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	m.ctx.Saving = true
	m.requestReload(reloadRequestedMsg{automatic: true, paths: []string{path}})
	m.HandleEvent(writeResultMsg{err: errors.New("injected rollback failure"), written: 1, reload: true, selection: selection})
	finishActionReload(t, m, messages)
	if selectedActionPackage(m) != "A" || !m.ctx.StatusIsErr || !strings.Contains(m.ctx.StatusLine, "rollback failure") {
		t.Fatalf("selected=%s error=%v status=%q", selectedActionPackage(m), m.ctx.StatusIsErr, m.ctx.StatusLine)
	}
}

func actionWriteResult(t *testing.T, msg ui.Event) writeResultMsg {
	t.Helper()
	switch result := msg.(type) {
	case writeResultMsg:
		return result
	case mutationReconciledMsg:
		return result.writeResultMsg
	default:
		t.Fatalf("unexpected write completion %T", msg)
		return writeResultMsg{}
	}
}
