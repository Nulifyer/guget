package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMutationPlanReportsOnlyActualChanges(t *testing.T) {
	root := t.TempDir()
	unchanged := writeCLIProject(t, root, "Current", `<Project><ItemGroup><PackageReference Include="Example" Version="2.0.0" /></ItemGroup></Project>`)
	changed := writeCLIProject(t, root, "Old", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	snapshot, err := scanWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, rows, err := buildMutationPlan("update", "Example", "2.0.0", snapshot.ParsedProjects)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Len() != 1 || len(rows) != 1 || rows[0].Path != changed {
		t.Fatalf("rows=%+v plan len=%d", rows, plan.Len())
	}
	before, _ := os.ReadFile(unchanged)
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(unchanged)
	if string(after) != string(before) {
		t.Fatal("rewrote up-to-date project")
	}
}

func TestBulkMutationPlanningFailureWritesNothing(t *testing.T) {
	root := t.TempDir()
	valid := writeCLIProject(t, root, "Valid", `<Project />`)
	invalid := filepath.Join(root, "Invalid.csproj")
	mustWriteFile(t, invalid, `<Project><ItemGroup></Project>`)
	project, err := ParseCsproj(valid)
	if err != nil {
		t.Fatal(err)
	}
	missing := &ParsedProject{FilePath: invalid, Packages: NewSet[PackageReference]()}
	m := NewApp(root, &workspaceSnapshot{ParsedProjects: []*ParsedProject{project, missing}}, nil, BuiltFlags{})
	m.ctx.Width = 120
	before, _ := os.ReadFile(valid)
	cmd := m.addPackageToTargets("Example", "1.0.0", []mutationTarget{{project: project}, {project: missing}})
	result := cmd().(writeResultMsg)
	if result.err == nil || result.reload || result.written != 0 {
		t.Fatalf("result=%+v", result)
	}
	m.Update(result)
	after, _ := os.ReadFile(valid)
	if string(after) != string(before) {
		t.Fatal("partially applied bulk add")
	}
	if m.ctx.Saving {
		t.Fatal("failed planning left mutation busy")
	}
}

func TestInheritedReferenceScopePreservesCentralVersions(t *testing.T) {
	root := t.TempDir()
	props := filepath.Join(root, "Shared.props")
	mustWriteFile(t, props, `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /><PackageVersion Include="Keep" Version="2.0.0" /></ItemGroup></Project>`)
	path := writeCLIProject(t, root, "App", `<Project><Import Project="../Shared.props" /></Project>`)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildMutationPlan("remove", "Example", "", []*ParsedProject{project}); err == nil {
		t.Fatal("selected-project removal accepted inherited reference")
	}
	plan, _, _, err := planPackageMutation("remove", "Example", "", []mutationTarget{{project: project}}, mutationOptions{removeInherited: true})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Len() != 1 {
		t.Fatalf("len=%d", plan.Len())
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	defined, err := fileDefinesPackage(props, "Keep")
	if err != nil || !defined {
		t.Fatalf("lost central version: defined=%v err=%v", defined, err)
	}
	exists, err := fileHasPackageReference(props, "Example")
	if err != nil || exists {
		t.Fatalf("inherited reference remains: exists=%v err=%v", exists, err)
	}
}
