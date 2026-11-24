package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/msbuildstatic"
)

const testCPMOn = `<PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup>`

func requireDotnet(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK required for evaluated ownership")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func findPackage(project *ParsedProject, id string) (PackageReference, bool) {
	for ref := range project.Packages {
		if strings.EqualFold(ref.Name, id) {
			return ref, true
		}
	}
	return PackageReference{}, false
}

// nestedCentralWorkspace has a parent central file imported by a nested one.
func nestedCentralWorkspace(t *testing.T) (root, parent, nested, project string) {
	t.Helper()
	root = t.TempDir()
	parent = filepath.Join(root, "Directory.Packages.props")
	nested = filepath.Join(root, "src", "Directory.Packages.props")
	mustWriteFile(t, parent, `<Project>`+testCPMOn+`<ItemGroup><PackageVersion Include="Parent.Pkg" Version="1.0.0" /><PackageVersion Include="Shared" Version="1.0.0" /></ItemGroup></Project>`)
	mustWriteFile(t, nested, `<Project><Import Project="$([MSBuild]::GetPathOfFileAbove(Directory.Packages.props, $(MSBuildThisFileDirectory)..))" />`+
		`<ItemGroup><PackageVersion Update="Shared" Version="2.0.0" /></ItemGroup></Project>`)
	project = writeCLIProject(t, filepath.Join(root, "src"), "App", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Shared" /></ItemGroup></Project>`)
	return root, parent, nested, project
}

func TestParseCsprojResolvesNestedCentralVersionOwners(t *testing.T) {
	_, parent, nested, path := nestedCentralWorkspace(t)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := project.SourceFileForPackage("Shared"); got != nested {
		t.Fatalf("Shared version owner = %s, want nested file", got)
	}
	if ref, _ := findPackage(project, "Shared"); ref.Requested != "2.0.0" {
		t.Fatalf("Shared = %+v", ref)
	}
	if entry := project.CentralVersions["parent.pkg"]; entry.Owner != parent || entry.Version != "1.0.0" {
		t.Fatalf("Parent.Pkg entry = %+v", entry)
	}
	if projectCPMTarget(project) != nested {
		t.Fatalf("add target = %s", projectCPMTarget(project))
	}
	for _, target := range project.AddTargets {
		if target.FilePath == parent {
			t.Fatalf("a file imported by the central file is not an add location: %+v", project.AddTargets)
		}
	}
}

func TestCPMAddUsesExistingCentralVersionFromImportedFile(t *testing.T) {
	_, parent, nested, path := nestedCentralWorkspace(t)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	// Static planning is enough for these checks; keep MSBuild out of it.
	project.RequiresEvaluation = false
	before := readFile(t, parent) + readFile(t, nested)
	_, _, err = buildMutationPlan("add", "Parent.Pkg", "2.0.0", []*ParsedProject{project})
	if err == nil || !strings.Contains(err.Error(), "guget update Parent.Pkg --version 2.0.0") || !strings.Contains(err.Error(), parent) {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, parent)+readFile(t, nested) != before {
		t.Fatal("refused add changed a central file")
	}
	plan, rows, err := buildMutationPlan("add", "Parent.Pkg", "1.0.0", []*ParsedProject{project})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Len() != 1 || len(rows) != 1 || rows[0].Path != canonicalTestPath(t, path) {
		t.Fatalf("plan len=%d rows=%+v; want only the project reference", plan.Len(), rows)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := canonicalMutationPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func TestUpdateEditsNestedCentralOverride(t *testing.T) {
	requireDotnet(t)
	_, parent, nested, path := nestedCentralWorkspace(t)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := buildMutationPlan("update", "Shared", "3.0.0", []*ParsedProject{project})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, nested), `Update="Shared" Version="3.0.0"`) || strings.Contains(readFile(t, parent), "3.0.0") {
		t.Fatalf("nested=%s\nparent=%s", readFile(t, nested), readFile(t, parent))
	}
}

func TestUpdateRefusesVersionOnCentrallyManagedReference(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "Directory.Packages.props"), `<Project>`+testCPMOn+`<ItemGroup><PackageVersion Include="Pkg" Version="1.0.0" /></ItemGroup></Project>`)
	path := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Pkg" Version="1.5.0" /></ItemGroup></Project>`)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(project.Declarations["pkg"].Problem, "NU1008") {
		t.Fatalf("declaration = %+v", project.Declarations["pkg"])
	}
	for _, options := range []mutationOptions{{}, {allowDeclaredEdit: true}} {
		_, _, _, err := planPackageMutation("update", "Pkg", "2.0.0", []mutationTarget{{project: project}}, options)
		if err == nil || !strings.Contains(err.Error(), "NU1008") || !strings.Contains(err.Error(), "VersionOverride") {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestCentralFloatingVersionsNeedOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		root := t.TempDir()
		optIn := ""
		if enabled {
			optIn = `<PropertyGroup><CentralPackageFloatingVersionsEnabled>true</CentralPackageFloatingVersionsEnabled></PropertyGroup>`
		}
		mustWriteFile(t, filepath.Join(root, "Directory.Packages.props"), `<Project>`+testCPMOn+optIn+`<ItemGroup><PackageVersion Include="Existing" Version="1.0.0" /></ItemGroup></Project>`)
		path := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Existing" /></ItemGroup></Project>`)
		project, err := ParseCsproj(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, verb := range []string{"add", "update"} {
			id := map[string]string{"add": "New", "update": "Existing"}[verb]
			_, _, err := buildMutationPlan(verb, id, "1.*", []*ParsedProject{project})
			if enabled && err != nil {
				t.Fatalf("%s with floating versions enabled: %v", verb, err)
			}
			if !enabled && (err == nil || !strings.Contains(err.Error(), "NU1011")) {
				t.Fatalf("%s floating central version: err = %v", verb, err)
			}
		}
	}
}

func globalWorkspace(t *testing.T) (central, project string) {
	t.Helper()
	root := t.TempDir()
	central = filepath.Join(root, "Directory.Packages.props")
	mustWriteFile(t, central, `<Project>`+testCPMOn+"<ItemGroup>\n    <GlobalPackageReference Include=\"Analyzer\" Version=\"1.0.0\" />\n    <PackageVersion Include=\"Other\" Version=\"1.0.0\" />\n  </ItemGroup></Project>")
	project = writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Other" /></ItemGroup></Project>`)
	return central, project
}

func TestGlobalPackageReferenceIsReadAndEditedInItsFile(t *testing.T) {
	central, path := globalWorkspace(t)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := findPackage(project, "Analyzer")
	if !ok || !ref.Global || ref.PrivateAssets != "All" || ref.Requested != "1.0.0" {
		t.Fatalf("global = %+v (found %v)", ref, ok)
	}
	declaration := project.Declarations["analyzer"]
	if declaration.ReferenceOwner != central || declaration.VersionOwner != central || declaration.VersionSource != msbuildstatic.VersionGlobal {
		t.Fatalf("declaration = %+v", declaration)
	}
	if !canEditLiteralDeclaration(project, "Analyzer") {
		t.Fatal("a literal GlobalPackageReference should allow the TUI's literal edit")
	}
	if _, _, err := buildMutationPlan("add", "Analyzer", "1.0.0", []*ParsedProject{project}); err != nil {
		t.Fatalf("add of a global package should be a no-op: %v", err)
	}
	plan, _, err := buildMutationPlan("update", "Analyzer", "2.0.0", []*ParsedProject{project})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, central), `<GlobalPackageReference Include="Analyzer" Version="2.0.0" />`) {
		t.Fatalf("central = %s", readFile(t, central))
	}
}

func TestGlobalPackageReferenceRemovalIsSharedFileEdit(t *testing.T) {
	central, path := globalWorkspace(t)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildMutationPlan("remove", "Analyzer", "", []*ParsedProject{project}); err == nil || !strings.Contains(err.Error(), "GlobalPackageReference") {
		t.Fatalf("selected-project removal of a global package: err = %v", err)
	}
	plan, _, _, err := planPackageMutation("remove", "Analyzer", "", []mutationTarget{{project: project}}, mutationOptions{removeInherited: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, central); strings.Contains(after, "Analyzer") || !strings.Contains(after, `<PackageVersion Include="Other"`) {
		t.Fatalf("central = %s", after)
	}
}

func TestInheritedCentralReferenceIsRemovedFromItsReferenceOwner(t *testing.T) {
	root := t.TempDir()
	central := filepath.Join(root, "Directory.Packages.props")
	buildProps := filepath.Join(root, "Directory.Build.props")
	mustWriteFile(t, central, `<Project>`+testCPMOn+`<ItemGroup><PackageVersion Include="Shared" Version="1.0.0" /></ItemGroup></Project>`)
	mustWriteFile(t, buildProps, `<Project><ItemGroup><PackageReference Include="Shared" /></ItemGroup></Project>`)
	path := writeCLIProject(t, root, "App", `<Project />`)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if project.ReferenceFileForPackage("Shared") != buildProps || project.SourceFileForPackage("Shared") != central {
		t.Fatalf("owners = %s, %s", project.ReferenceFileForPackage("Shared"), project.SourceFileForPackage("Shared"))
	}
	plan, _, _, err := planPackageMutation("remove", "Shared", "", []mutationTarget{{project: project}}, mutationOptions{removeInherited: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, buildProps), "Shared") || !strings.Contains(readFile(t, central), "Shared") {
		t.Fatal("removal must delete the inherited reference and keep the central version")
	}
}

func TestRemoveIgnoresProjectUpdateOfInheritedReference(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "Directory.Build.props"), `<Project><ItemGroup><PackageReference Include="Shared" Version="1.0.0" /></ItemGroup></Project>`)
	path := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Update="Shared" PrivateAssets="All" /></ItemGroup></Project>`)
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	project.RequiresEvaluation = false
	if _, _, err := buildMutationPlan("remove", "Shared", "", []*ParsedProject{project}); err == nil || !strings.Contains(err.Error(), "inherited") {
		t.Fatalf("an Update in the project does not make the reference project-owned: err = %v", err)
	}
}

func TestParseCsprojReportsDownloadsReferencesAndLockFile(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "Directory.Build.props"), `<Project><PropertyGroup><RestorePackagesWithLockFile>true</RestorePackagesWithLockFile></PropertyGroup></Project>`)
	path := writeCLIProject(t, root, "App", `<Project><PropertyGroup><RestoreLockedMode Condition="'$(CI)' == 'true'">true</RestoreLockedMode></PropertyGroup>`+
		`<ItemGroup><PackageDownload Include="Tool" Version="[1.0.0]" /><ProjectReference Include="../Lib/Lib.csproj" /></ItemGroup></Project>`)
	mustWriteFile(t, filepath.Join(root, "App", "packages.App.lock.json"), `{}`)
	t.Setenv("CI", "")
	os.Unsetenv("CI")
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(project.Downloads) != 1 || project.Downloads[0].Name != "Tool" || project.Downloads[0].Requested != "[1.0.0]" {
		t.Fatalf("downloads = %+v", project.Downloads)
	}
	if _, ok := findPackage(project, "Tool"); ok {
		t.Fatal("PackageDownload must not appear as a package reference")
	}
	if len(project.ProjectReferences) != 1 || project.ProjectReferences[0] != filepath.Join(root, "Lib", "Lib.csproj") {
		t.Fatalf("project references = %v", project.ProjectReferences)
	}
	want := LockFileSettings{Path: filepath.Join(root, "App", "packages.App.lock.json"), WithLockFile: LockSettingTrue, LockedMode: LockSettingUnknown}
	if project.LockFile != want {
		t.Fatalf("lock file = %+v, want %+v", project.LockFile, want)
	}
}
