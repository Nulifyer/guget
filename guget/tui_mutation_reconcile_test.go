package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
)

func TestMutationReconcilesWithoutScanningUnrelatedProject(t *testing.T) {
	m, _, path := newActionTestApp(t, "")
	// A newly malformed unrelated project would fail a full workspace scan.
	unrelated := filepath.Join(filepath.Dir(path), "Unrelated.csproj")
	mustWriteFile(t, unrelated, `<Project><ItemGroup><PackageReference Include="C" Version="1.0.0"/></ItemGroup></Project>`)
	loaded, err := ParseCsproj(unrelated)
	if err != nil {
		t.Fatal(err)
	}
	m.ctx.ParsedProjects = append(m.ctx.ParsedProjects, loaded)
	mustWriteFile(t, unrelated, `<Project`)
	executeActionCmd(t, m, m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})))
	if m.ctx.Reloading || m.ctx.Saving || !m.canMutate() {
		t.Fatal("save waited for unrelated rediscovery")
	}
	if m.ctx.ParsedProjects[1] != loaded {
		t.Fatal("unrelated immutable project snapshot was replaced")
	}
	if !strings.Contains(m.Render().Content, "2.0.0") {
		t.Fatal("committed version is not visible")
	}
	if cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})); cmd == nil {
		t.Fatal("next package update was dropped")
	}
}

func TestMutationReconcileRejectsExternalWriteDuringParse(t *testing.T) {
	m, _, path := newActionTestApp(t, "")
	previous := &workspaceSnapshot{ParsedProjects: m.ctx.ParsedProjects, PropsProjects: m.ctx.PropsProjects}
	external := `<Project><ItemGroup><PackageReference Include="A" Version="9.0.0"/></ItemGroup></Project>`
	snapshot, _, err := reconcileMutationSnapshot(previous, []string{path}, func(file string) (*ParsedProject, error) {
		parsed, err := ParseCsproj(file)
		if err == nil {
			err = os.WriteFile(file, []byte(external), 0o644)
		}
		return parsed, err
	})
	if err == nil || snapshot != nil {
		t.Fatal("mixed revision snapshot was published")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != external {
		t.Fatal("external write was not preserved")
	}
}

func TestMutationUsesLifecycleCancellationBeforeWriting(t *testing.T) {
	m, _, path := newActionTestApp(t, "")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, cancel := context.WithCancel(context.Background())
	m.lifecycle = lifecycle
	cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'}))
	cancel()
	result := actionWriteResult(t, cmd())
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancellation=%v", result.err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("canceled mutation changed files")
	}
}

func TestOwnWatcherRevisionIsSkippedButOverlappingExternalRevisionReloads(t *testing.T) {
	m, _, path := newActionTestApp(t, "")
	executeActionCmd(t, m, m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})))
	state, exists, err := readWatchedFileState(path)
	if err != nil || !exists {
		t.Fatal(err)
	}
	m.handleWatchedWorkspaceChange(watchedWorkspaceChangeMsg{paths: []string{path}, states: map[string]watchedFileState{path: state}})
	if m.ctx.Reloading {
		t.Fatal("own committed revision caused a duplicate reload")
	}
	if err := UpdatePackageVersion(path, "A", "9.0.0"); err != nil {
		t.Fatal(err)
	}
	external, _, err := readWatchedFileState(path)
	if err != nil {
		t.Fatal(err)
	}
	m.handleWatchedWorkspaceChange(watchedWorkspaceChangeMsg{paths: []string{path}, states: map[string]watchedFileState{path: external}})
	if !m.ctx.Reloading {
		t.Fatal("external write sharing the same owner was suppressed")
	}
}

func TestMutationReconcileUpdatesEverySharedOwnerConsumer(t *testing.T) {
	root := t.TempDir()
	owner := filepath.Join(root, "Shared.props")
	mustWriteFile(t, owner, `<Project><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
	var projects []*ParsedProject
	for _, name := range []string{"First.csproj", "Second.csproj"} {
		file := filepath.Join(root, name)
		mustWriteFile(t, file, `<Project><Import Project="Shared.props"/></Project>`)
		project, err := ParseCsproj(file)
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, project)
	}
	previous := &workspaceSnapshot{ParsedProjects: projects, PropsProjects: collectPropsProjects(projects)}
	if err := UpdatePackageVersion(owner, "A", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	next, _, err := reconcileMutationSnapshot(previous, []string{owner}, ParseCsproj)
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range append(next.ParsedProjects, next.PropsProjects...) {
		for ref := range project.Packages {
			if ref.Name == "A" && ref.Version.String() != "2.0.0" {
				t.Fatalf("stale shared consumer %s=%s", project.FilePath, ref.Version.String())
			}
		}
	}
}

func TestIncompleteFullReloadRetainsViewAndBlocksMutationUntilRepaired(t *testing.T) {
	m, messages, path := newActionTestApp(t, "")
	selected := m.ctx.ParsedProjects[0]
	broken := filepath.Join(filepath.Dir(path), "New.csproj")
	mustWriteFile(t, broken, `<Project`)
	m.requestReload(reloadRequestedMsg{reason: "manual reload"})
	finishActionReload(t, m, messages)
	if !m.workspaceStale || m.canMutate() || m.ctx.ParsedProjects[0] != selected {
		t.Fatal("incomplete discovery did not preserve a read-only prior view")
	}
	if cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})); cmd != nil {
		t.Fatal("mutation used incomplete discovery")
	}
	executeActionCmd(t, m, m.handleKey(ui.KeyPress(ui.Key{Code: '?'})))
	if !m.help.active {
		t.Fatal("help was blocked with stale workspace")
	}
	m.help.closeOverlay()
	mustWriteFile(t, broken, `<Project/>`)
	m.requestReload(reloadRequestedMsg{reason: "manual reload"})
	finishActionReload(t, m, messages)
	if m.workspaceStale || !m.canMutate() || len(m.ctx.ParsedProjects) != 2 {
		t.Fatal("successful full reload did not restore mutations")
	}
}

func TestUnrelatedFullReloadPreservesSelectedDetailScroll(t *testing.T) {
	m, messages, path := newActionTestApp(t, "")
	m.ctx.Results["A"].pkg.Description = strings.Repeat("package description ", 500)
	m.refreshDetail()
	m.detail.vp.SetYOffset(5)
	if m.detail.vp.YOffset() != 5 {
		t.Fatal("fixture has insufficient detail content")
	}
	if err := UpdatePackageVersion(path, "B", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	m.requestReload(reloadRequestedMsg{automatic: true, paths: []string{path}})
	finishActionReload(t, m, messages)
	if selectedActionPackage(m) != "A" || m.detail.vp.YOffset() != 5 {
		t.Fatalf("selection=%s offset=%d", selectedActionPackage(m), m.detail.vp.YOffset())
	}
}

func TestMutationReconcileRejectsConsumedRevisionAfterDiskRestore(t *testing.T) {
	m, _, path := newActionTestApp(t, "")
	previous := &workspaceSnapshot{ParsedProjects: m.ctx.ParsedProjects, PropsProjects: m.ctx.PropsProjects}
	stable, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	intermediate := strings.Replace(string(stable), `Version="1.0.0"`, `Version="9.0.0"`, 1)
	snapshot, _, err := reconcileMutationSnapshot(previous, []string{path}, func(file string) (*ParsedProject, error) {
		if err := os.WriteFile(file, []byte(intermediate), 0o644); err != nil {
			return nil, err
		}
		parsed, parseErr := ParseCsproj(file)
		restoreErr := os.WriteFile(file, stable, 0o644)
		if parseErr != nil {
			return nil, parseErr
		}
		return parsed, restoreErr
	})
	if err == nil || snapshot != nil {
		t.Fatal("local reconciliation accepted bytes the parser did not consume")
	}
}

func TestMutationRejectsExternalEditBeforePlanningAndReloads(t *testing.T) {
	for _, change := range []string{"version", "framework"} {
		t.Run(change, func(t *testing.T) {
			m, messages, path := newActionTestApp(t, "")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			external := strings.Replace(string(data), `Version="1.0.0"`, `Version="9.0.0"`, 1)
			if change == "framework" {
				external = strings.Replace(string(data), "net8.0", "net9.0", 1)
			}
			if err := os.WriteFile(path, []byte(external), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'}))
			if cmd == nil {
				t.Fatal("fixture did not schedule update")
			}
			result := cmd()
			completion := actionWriteResult(t, result)
			if completion.err == nil || !completion.reload || completion.written != 0 {
				t.Fatalf("completion=%+v", completion)
			}
			after, _ := os.ReadFile(path)
			if string(after) != external {
				t.Fatal("stale recommendation overwrote external edit")
			}
			m.HandleEvent(result)
			if !m.ctx.Reloading {
				t.Fatal("stale-input refusal did not reload")
			}
			finishActionReload(t, m, messages)
			if m.ctx.Saving || m.ctx.Reloading || m.workspaceStale {
				t.Fatal("valid disk revision did not reconcile after refusal")
			}
		})
	}
}

func TestMutationRejectsSourceConfigurationChangedAfterRecommendation(t *testing.T) {
	m, _, path := newActionTestApp(t, "")
	config := filepath.Join(filepath.Dir(path), "NuGet.Config")
	mustWriteFile(t, config, `<configuration><packageSources><clear/><add key="changed" value="./other-packages"/></packageSources></configuration>`)
	cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'}))
	result := actionWriteResult(t, cmd())
	if result.err == nil || !result.reload || result.written != 0 {
		t.Fatalf("stale source result=%+v", result)
	}
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	for ref := range project.Packages {
		if ref.Version.String() != "1.0.0" {
			t.Fatal("stale source recommendation changed package")
		}
	}
}

func TestConsecutiveSharedBuildPropsUpdatesKeepSourceRevisionsCurrent(t *testing.T) {
	m, _, path := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
	root := filepath.Dir(path)
	owner := filepath.Join(root, "Directory.Build.props")
	mustWriteFile(t, owner, `<Project><ItemGroup><PackageReference Include="A" Version="1.0.0"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
	snapshot, err := scanWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	m.applyWorkspaceSnapshot(snapshot)
	m.rebuildPackageRows()
	for _, name := range []string{"A", "B"} {
		m.selectPackageByName(name)
		cmd := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'}))
		if cmd == nil {
			t.Fatalf("%s update was unavailable", name)
		}
		result := cmd()
		delta, ok := result.(mutationReconciledMsg)
		if !ok || delta.err != nil || delta.reload {
			t.Fatalf("%s fell back to full reload: %+v", name, result)
		}
		m.HandleEvent(result)
		if m.ctx.Saving || m.ctx.Reloading {
			t.Fatalf("%s left update blocked", name)
		}
		state, exists, err := readWatchedFileState(owner)
		if err != nil || !exists || m.sourceInputRevisions[owner] != state.Hash {
			t.Fatalf("%s source input revision was not advanced", name)
		}
	}
	final, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	for ref := range final.Packages {
		if ref.Version.String() != "2.0.0" {
			t.Fatalf("%s=%s", ref.Name, ref.Version.String())
		}
	}
}
