package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// restoreWorkspace has two projects consuming one central file, a library
// referenced by one of them, and an unrelated project with its own props.
func restoreWorkspace(t *testing.T) (root string, projects map[string]*ParsedProject) {
	t.Helper()
	root = t.TempDir()
	mustWriteFile(t, filepath.Join(root, "Directory.Packages.props"), `<Project>`+testCPMOn+`<ItemGroup><PackageVersion Include="Shared" Version="1.0.0" /></ItemGroup></Project>`)
	mustWriteFile(t, filepath.Join(root, "Directory.Build.props"), `<Project><PropertyGroup><RestorePackagesWithLockFile>true</RestorePackagesWithLockFile><RestoreLockedMode>true</RestoreLockedMode></PropertyGroup></Project>`)
	paths := map[string]string{
		"Lib":  writeCLIProject(t, root, "Lib", `<Project><ItemGroup><PackageReference Include="Shared" /></ItemGroup></Project>`),
		"App":  writeCLIProject(t, root, "App", `<Project><ItemGroup><ProjectReference Include="../Lib/Lib.csproj" /></ItemGroup></Project>`),
		"Tool": writeCLIProject(t, root, "Tool", `<Project><ItemGroup><PackageReference Include="Shared" /></ItemGroup></Project>`),
	}
	mustWriteFile(t, filepath.Join(root, "Lib", "packages.lock.json"), `{}`)
	mustWriteFile(t, filepath.Join(root, "App", "packages.lock.json"), `{}`)
	projects = map[string]*ParsedProject{}
	for name, path := range paths {
		project, err := ParseCsproj(path)
		if err != nil {
			t.Fatal(err)
		}
		projects[name] = project
	}
	return root, projects
}

func stepNames(steps []restoreStep) []string {
	var names []string
	for _, step := range steps {
		name := strings.TrimSuffix(step.Project.FileName, ".csproj")
		if step.ForceEvaluate {
			name += "!"
		}
		names = append(names, name)
	}
	return names
}

func TestRestorePlanCoversConsumersOfChangedSharedFile(t *testing.T) {
	root, projects := restoreWorkspace(t)
	workspace := []*ParsedProject{projects["App"], projects["Tool"], projects["Lib"]}
	steps := planRestore(workspace, []*ParsedProject{projects["Tool"]}, []string{filepath.Join(root, "Directory.Packages.props")})
	// Lib before App, which references it; Tool has no lock file.
	if got := stepNames(steps); !slices.Equal(got, []string{"Lib!", "App!", "Tool"}) {
		t.Fatalf("steps = %v", got)
	}
	if !slices.Contains(steps[0].Args, "--force-evaluate") || steps[0].Args[1] != projects["Lib"].FilePath {
		t.Fatalf("args = %v", steps[0].Args)
	}
	var out bytes.Buffer
	describeRestorePlan(&out, steps)
	if !strings.Contains(out.String(), "uses changed") || !strings.Contains(out.String(), "--force-evaluate") || strings.Contains(out.String(), "Tool") {
		t.Fatalf("description = %s", out.String())
	}
}

func TestRestorePlanFollowsProjectReferencesFromEditedProject(t *testing.T) {
	_, projects := restoreWorkspace(t)
	workspace := []*ParsedProject{projects["App"], projects["Tool"], projects["Lib"]}
	steps := planRestore(workspace, []*ParsedProject{projects["Lib"]}, []string{projects["Lib"].FilePath})
	if got := stepNames(steps); !slices.Equal(got, []string{"Lib!", "App!"}) {
		t.Fatalf("steps = %v", got)
	}
}

func TestRestorePlanWithoutEditsRestoresOnlyRequestedProjects(t *testing.T) {
	_, projects := restoreWorkspace(t)
	workspace := []*ParsedProject{projects["App"], projects["Tool"], projects["Lib"]}
	// A plain restore must not regenerate a valid locked lock file.
	if got := stepNames(planRestore(workspace, []*ParsedProject{projects["App"]}, nil)); !slices.Equal(got, []string{"App"}) {
		t.Fatalf("steps = %v", got)
	}
}

func TestRestorePlanForceEvaluateFollowsLockedMode(t *testing.T) {
	for _, test := range []struct {
		name     string
		props    string
		lockFile bool
		want     bool
	}{
		{name: "locked with lock file", props: `<RestoreLockedMode>true</RestoreLockedMode>`, lockFile: true, want: true},
		{name: "locked without lock file", props: `<RestoreLockedMode>true</RestoreLockedMode>`},
		{name: "not locked", props: `<RestorePackagesWithLockFile>true</RestorePackagesWithLockFile>`, lockFile: true},
		{name: "locked mode undecidable", props: `<RestoreLockedMode Condition="'$(ContinuousIntegrationBuild)' == 'true'">true</RestoreLockedMode>`, lockFile: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := writeCLIProject(t, root, "App", `<Project><PropertyGroup>`+test.props+`</PropertyGroup></Project>`)
			if test.lockFile {
				mustWriteFile(t, filepath.Join(root, "App", "packages.lock.json"), `{}`)
			}
			t.Setenv("ContinuousIntegrationBuild", "")
			os.Unsetenv("ContinuousIntegrationBuild")
			project, err := ParseCsproj(path)
			if err != nil {
				t.Fatal(err)
			}
			steps := planRestore([]*ParsedProject{project}, []*ParsedProject{project}, []string{path})
			if len(steps) != 1 || steps[0].ForceEvaluate != test.want {
				t.Fatalf("steps = %+v", steps)
			}
		})
	}
}

// A lock file created after the project was read, such as by a restore, is
// found by the restore worker just before it runs, so the affected
// locked-mode project regenerates it instead of failing with NU1004. Planning
// itself still uses the existence recorded by the scan.
func TestRestoreWorkersRecheckLockFilesCreatedAfterTheScan(t *testing.T) {
	root, projects := restoreWorkspace(t)
	tool := projects["Tool"]
	if tool.LockFile.Exists {
		t.Fatal("Tool must have no lock file when read")
	}
	mustWriteFile(t, filepath.Join(root, "Tool", "packages.lock.json"), `{}`)
	workspace := []*ParsedProject{projects["App"], projects["Lib"], tool}
	changed := []string{filepath.Join(root, "Directory.Packages.props")}
	steps := planRestore(workspace, nil, changed)
	if got := stepNames(steps); !slices.Equal(got, []string{"Lib!", "App!", "Tool"}) {
		t.Fatalf("planning consulted the disk: %v", got)
	}

	t.Run("CLI", func(t *testing.T) {
		var stderr bytes.Buffer
		var commands []string
		runtime := cliRuntime{stdout: io.Discard, stderr: &stderr, runCommand: func(_ context.Context, _ string, args []string, _ io.Reader, _, _ io.Writer) error {
			commands = append(commands, strings.Join(args, " "))
			return nil
		}}
		if _, code := runtime.restoreProjects(context.Background(), steps, false); code != ExitSuccess {
			t.Fatalf("code = %d", code)
		}
		if len(commands) != 3 || commands[2] != "restore "+tool.FilePath+" --force-evaluate" {
			t.Fatalf("commands = %q", commands)
		}
		if !strings.Contains(stderr.String(), tool.FilePath+": locked-mode lock file is regenerated") {
			t.Fatalf("stderr = %s", stderr.String())
		}
	})
	t.Run("TUI", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir()) // the restores fail; the arguments are counted first
		result, ok := runDotnetRestore(context.Background(), steps, changed)().(restoreResultMsg)
		if !ok || result.projects != 3 || result.regenerated != 3 {
			t.Fatalf("result = %+v", result)
		}
	})
	if steps[2].ForceEvaluate || slices.Contains(steps[2].Args, "--force-evaluate") {
		t.Fatal("the worker changed the planned step")
	}
	// Not in locked mode, or not affected by an edit: no change.
	unaffected := restoreStep{Project: tool, Args: []string{"restore", tool.FilePath}}
	if got := unaffected.withCurrentLockFile(); got.ForceEvaluate {
		t.Fatal("an unaffected project regenerated its lock file")
	}
	unlocked := *tool
	unlocked.LockFile.LockedMode = LockSettingFalse
	if got := (restoreStep{Project: &unlocked, Args: []string{"restore"}, Reason: "changed"}).withCurrentLockFile(); got.ForceEvaluate {
		t.Fatal("a project outside locked mode regenerated its lock file")
	}
}

// The TUI plans restores on its event loop, so planning uses the lock-file
// existence recorded when the project was read instead of asking the disk.
func TestRestorePlanUsesLockFileExistenceFromTheScan(t *testing.T) {
	root, projects := restoreWorkspace(t)
	lib := projects["Lib"]
	if !lib.LockFile.Exists || projects["Tool"].LockFile.Exists {
		t.Fatalf("scan recorded Lib=%v Tool=%v", lib.LockFile.Exists, projects["Tool"].LockFile.Exists)
	}
	if err := os.Remove(filepath.Join(root, "Lib", "packages.lock.json")); err != nil {
		t.Fatal(err)
	}
	m := NewApp(t.TempDir(), &workspaceSnapshot{ParsedProjects: []*ParsedProject{projects["App"], lib, projects["Tool"]}}, nil, BuiltFlags{})
	m.pendingRestoreChanges = []string{lib.FilePath}
	if effect := m.startRestore([]*ParsedProject{lib}); effect == nil || m.restoringProjects != 2 {
		t.Fatalf("restore planned %d projects", m.restoringProjects)
	}
	steps := planRestore(m.ctx.ParsedProjects, []*ParsedProject{lib}, m.pendingRestoreChanges)
	if len(steps) == 0 || steps[0].Project != lib || !steps[0].ForceEvaluate {
		t.Fatalf("planning consulted the disk instead of the scan: %+v", steps)
	}
}

func TestTUIRestoreKeepsUnrestoredEditsUntilARestoreSucceeds(t *testing.T) {
	_, projects := restoreWorkspace(t)
	m := NewApp(t.TempDir(), &workspaceSnapshot{ParsedProjects: []*ParsedProject{projects["App"], projects["Lib"], projects["Tool"]}}, nil, BuiltFlags{})
	changed := []string{projects["Lib"].FilePath}
	m.ctx.Saving = true
	m.HandleEvent(writeResultMsg{changed: changed, written: 1})
	if !slices.Equal(m.pendingRestoreChanges, changed) {
		t.Fatalf("pending = %v", m.pendingRestoreChanges)
	}
	m.HandleEvent(restoreResultMsg{err: errTestRestore, covered: changed})
	if len(m.pendingRestoreChanges) != 1 {
		t.Fatal("a failed restore must keep the edits pending")
	}
	m.HandleEvent(restoreResultMsg{covered: changed})
	if len(m.pendingRestoreChanges) != 0 {
		t.Fatalf("pending after success = %v", m.pendingRestoreChanges)
	}
}

var errTestRestore = &testError{"restore failed"}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }
