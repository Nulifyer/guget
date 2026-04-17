package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPlanPackageReload_ReusesCachedResults(t *testing.T) {
	snapshot := &workspaceSnapshot{
		ParsedProjects: []*ParsedProject{
			testProjectWithPackages("ProjectA.csproj", "Newtonsoft.Json", "Serilog"),
		},
		PropsProjects: []*ParsedProject{
			testProjectWithPackages("Directory.Packages.props", "Polly"),
		},
	}

	current := map[string]nugetResult{
		"Newtonsoft.Json": {pkg: &PackageInfo{ID: "Newtonsoft.Json"}},
		"Serilog":         {err: os.ErrNotExist},
		"Polly":           {pkg: &PackageInfo{ID: "Polly"}},
	}

	reused, toFetch := planPackageReload(snapshot, current, false)

	if len(reused) != 2 {
		t.Fatalf("expected 2 reused results, got %d", len(reused))
	}
	if reused["Newtonsoft.Json"].pkg == nil || reused["Polly"].pkg == nil {
		t.Fatalf("expected cached package info to be retained, got %#v", reused)
	}
	if !slices.Equal(toFetch, []string{"Serilog"}) {
		t.Fatalf("expected only Serilog to refetch, got %v", toFetch)
	}

	reused, toFetch = planPackageReload(snapshot, current, true)
	if len(reused) != 0 {
		t.Fatalf("expected source invalidation to clear reused results, got %d", len(reused))
	}
	if !slices.Equal(toFetch, []string{"Newtonsoft.Json", "Polly", "Serilog"}) {
		t.Fatalf("expected all packages to refetch after invalidation, got %v", toFetch)
	}
}

func TestScanWatchedWorkspaceFiles_IgnoresBuildOutput(t *testing.T) {
	root := t.TempDir()

	mustWriteFile(t, filepath.Join(root, "ProjectA", "ProjectA.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "shared.props"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "nuget.config"), "<configuration />")
	mustWriteFile(t, filepath.Join(root, "obj", "ignored.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "bin", "ignored.props"), "<Project />")

	files, err := scanWatchedWorkspaceFiles(root)
	if err != nil {
		t.Fatalf("scanWatchedWorkspaceFiles: %v", err)
	}

	if len(files) != 3 {
		t.Fatalf("expected 3 watched files, got %d: %v", len(files), files)
	}
	if _, ok := files[filepath.Join(root, "ProjectA", "ProjectA.csproj")]; !ok {
		t.Fatal("expected project file to be watched")
	}
	if _, ok := files[filepath.Join(root, "shared.props")]; !ok {
		t.Fatal("expected props file to be watched")
	}
	if _, ok := files[filepath.Join(root, "nuget.config")]; !ok {
		t.Fatal("expected nuget.config to be watched")
	}
	if _, ok := files[filepath.Join(root, "obj", "ignored.csproj")]; ok {
		t.Fatal("obj directory should be ignored")
	}
	if _, ok := files[filepath.Join(root, "bin", "ignored.props")]; ok {
		t.Fatal("bin directory should be ignored")
	}
}

func TestDiffWatchedWorkspaceFiles_DetectsAddRemoveAndChange(t *testing.T) {
	prev := map[string]watchedFileState{
		"a.csproj": {Size: 10, ModTime: 100},
		"b.props":  {Size: 20, ModTime: 200},
	}
	next := map[string]watchedFileState{
		"a.csproj": {Size: 11, ModTime: 100},
		"c.props":  {Size: 30, ModTime: 300},
	}

	changed := diffWatchedWorkspaceFiles(prev, next)
	expected := []string{"a.csproj", "b.props", "c.props"}
	if !slices.Equal(changed, expected) {
		t.Fatalf("expected %v, got %v", expected, changed)
	}
}

func TestRequestReload_QueuesWhileReloading(t *testing.T) {
	app := &App{
		ctx:        &AppContext{Reloading: true},
		send:       func(tea.Msg) {},
		projectDir: t.TempDir(),
	}

	req := reloadRequestedMsg{reason: "second"}
	app.requestReload(req)

	if !app.hasPendingReload {
		t.Fatal("expected hasPendingReload=true while already reloading")
	}
	if app.pendingReload.reason != "second" {
		t.Fatalf("expected queued request to be retained, got %+v", app.pendingReload)
	}
	if app.workspaceGeneration != 0 {
		t.Fatalf("expected generation unchanged while queuing, got %d", app.workspaceGeneration)
	}
	if app.ctx.StatusLine == "" {
		t.Fatal("expected queue status to be set when reloading")
	}
}

func TestRequestReload_QueuesWhileLoading(t *testing.T) {
	app := &App{
		ctx:        &AppContext{Loading: true},
		send:       func(tea.Msg) {},
		projectDir: t.TempDir(),
	}

	req := reloadRequestedMsg{reason: "during initial load"}
	app.requestReload(req)

	if !app.hasPendingReload {
		t.Fatal("expected hasPendingReload=true while initial load in flight")
	}
	if app.ctx.StatusLine != "" {
		t.Fatalf("expected no queued-status during initial load, got %q", app.ctx.StatusLine)
	}
}

func TestRequestReload_QueuedBurstCoalesces(t *testing.T) {
	app := &App{
		ctx:        &AppContext{Reloading: true},
		send:       func(tea.Msg) {},
		projectDir: t.TempDir(),
	}

	app.requestReload(reloadRequestedMsg{reason: "first"})
	app.requestReload(reloadRequestedMsg{reason: "second"})
	app.requestReload(reloadRequestedMsg{reason: "third"})

	if !app.hasPendingReload {
		t.Fatal("expected a queued reload to remain")
	}
	if app.pendingReload.reason != "third" {
		t.Fatalf("expected newest queued request to win, got %q", app.pendingReload.reason)
	}
}

func TestHandleWorkspaceReloaded_IgnoresStaleGeneration(t *testing.T) {
	app := &App{
		ctx:                 &AppContext{Reloading: true},
		send:                func(tea.Msg) {},
		workspaceGeneration: 5,
	}

	// Stale reply (gen 3) arriving after a newer reload (gen 5) must not
	// touch state. snapshot=nil would panic in applyWorkspaceSnapshot if
	// the gate failed.
	app.handleWorkspaceReloaded(workspaceReloadedMsg{generation: 3, snapshot: nil})

	if !app.ctx.Reloading {
		t.Fatal("expected Reloading to stay true when stale reply is ignored")
	}
	if app.sourceSignature != "" {
		t.Fatalf("expected sourceSignature untouched, got %q", app.sourceSignature)
	}
}

func testProjectWithPackages(path string, packages ...string) *ParsedProject {
	project := &ParsedProject{
		FileName:         filepath.Base(path),
		FilePath:         path,
		TargetFrameworks: NewSet[TargetFramework](),
		Packages:         NewSet[PackageReference](),
		PackageSources:   make(map[string]string),
	}
	for _, pkg := range packages {
		project.Packages.Add(PackageReference{Name: pkg, Version: ParseSemVer("1.0.0")})
		project.PackageSources[pkg] = path
	}
	return project
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
