package main

import (
	"bytes"
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
