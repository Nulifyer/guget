package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if plan.Len() != 1 || len(rows) != 1 || rows[0].Path != canonicalTestOwnerPath(t, changed) {
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
	m.HandleEvent(result)
	after, _ := os.ReadFile(valid)
	if string(after) != string(before) {
		t.Fatal("partially applied bulk add")
	}
	if m.ctx.Saving {
		t.Fatal("failed planning left mutation busy")
	}
}

func TestInheritedReferenceScopePreservesCentralVersions(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK required for imported ownership")
	}
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

func TestCPMAddRefusesExistingDifferentCentralVersion(t *testing.T) {
	root := t.TempDir()
	central := filepath.Join(root, "Directory.Packages.props")
	before := `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="Example" Version="1.0.0" /></ItemGroup></Project>`
	mustWriteFile(t, central, before)
	path := writeCLIProject(t, root, "App", `<Project />`)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildMutationPlan("add", "Example", "2.0.0", []*ParsedProject{project}); err == nil {
		t.Fatal("accepted central version mismatch")
	}
	after, _ := os.ReadFile(central)
	if string(after) != before {
		t.Fatal("central version changed after refusal")
	}
	plan, _, err := buildMutationPlan("add", "Example", "1.0.0", []*ParsedProject{project})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Len() != 1 {
		t.Fatalf("same-version add plan len=%d", plan.Len())
	}
}

func TestInactiveImportCannotBeMutated(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK required for evaluated ownership")
	}
	root := t.TempDir()
	props := filepath.Join(root, "Inactive.props")
	before := `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`
	mustWriteFile(t, props, before)
	path := writeCLIProject(t, root, "App", `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><Import Project="../Inactive.props" Condition="false" /></Project>`)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if !project.RequiresEvaluation {
		t.Fatal("conditional import did not require evaluation")
	}
	if _, _, err := buildMutationPlan("update", "Example", "2.0.0", []*ParsedProject{project}); err == nil {
		t.Fatal("inactive imported dependency was editable")
	}
	after, _ := os.ReadFile(props)
	if string(after) != before {
		t.Fatal("inactive file changed")
	}
}

func TestMutationPreviewUsesCanonicalOwnerPaths(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(real, "App.csproj")
	mustWriteFile(t, path, `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	project, err := ParseCsproj(filepath.Join(alias, "App.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	plan, rows, err := buildMutationPlan("update", "Example", "2.0.0", []*ParsedProject{project})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Len() != 1 || len(rows) != 1 || rows[0].Path != canonical {
		t.Fatalf("paths=%v rows=%+v", plan.Paths(), rows)
	}
	if identity := project.PackageOwnerIdentities[project.SourceFileForPackage("Example")]; identity != canonical {
		t.Fatalf("cached owner identity=%q expected=%q", identity, canonical)
	}
}

func TestMutationRefusesEvaluatedOwnerOutsideDeclaredScope(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK required for evaluated ownership")
	}
	root := t.TempDir()
	first := filepath.Join(root, "Inactive.props")
	second := filepath.Join(root, "Active.props")
	mustWriteFile(t, first, `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	mustWriteFile(t, second, `<Project><ItemGroup><PackageReference Include="Example" Version="2.0.0" /></ItemGroup></Project>`)
	path := writeCLIProject(t, root, "App", `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><Import Project="../Inactive.props" Condition="false" /><Import Project="../Active.props" /></Project>`)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if project.SourceFileForPackage("Example") != first {
		t.Fatal("fixture must discover the inactive source first")
	}
	if _, _, err := buildMutationPlan("update", "Example", "3.0.0", []*ParsedProject{project}); err == nil || !strings.Contains(err.Error(), "differs from declared discovery") {
		t.Fatalf("err=%v", err)
	}
	for _, file := range []string{first, second} {
		data, _ := os.ReadFile(file)
		if strings.Contains(string(data), `Version="3.0.0"`) {
			t.Fatal("mutated outside known ownership scope")
		}
	}
}
