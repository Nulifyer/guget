package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testDataDir returns the absolute path to the test-dotnet directory.
func testDataDir(t *testing.T) string {
	t.Helper()
	// guget/ is the module dir; test-dotnet/ is a sibling
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(filepath.Dir(wd), "test-dotnet")
}

func TestFindDirectoryBuildProps_Found(t *testing.T) {
	td := testDataDir(t)
	subDir := filepath.Join(td, "ProjectA")
	got := findDirectoryBuildProps(subDir)
	if got == "" {
		t.Fatal("expected to find Directory.Build.props, got empty string")
	}
	if filepath.Base(got) != "Directory.Build.props" {
		t.Fatalf("expected Directory.Build.props, got %s", filepath.Base(got))
	}
}

func TestFindDirectoryBuildProps_NotFound(t *testing.T) {
	got := findDirectoryBuildProps(os.TempDir())
	if got != "" {
		t.Fatalf("expected empty string, got %s", got)
	}
}

func TestResolveImportPath_Relative(t *testing.T) {
	got, err := resolveImportPath("imported.props", "/proj/src", "/proj/src")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean("/proj/src/imported.props")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveImportPath_ProjectDir(t *testing.T) {
	got, err := resolveImportPath("$(ProjectDir)\\imported.props", "/proj/src", "/proj/src")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "imported.props") {
		t.Fatalf("expected path ending in imported.props, got %s", got)
	}
}

func TestResolveImportPath_MSBuildThisFileDirectory(t *testing.T) {
	got, err := resolveImportPath("$(MSBuildThisFileDirectory)\\common.props", "/a/b", "/proj")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "common.props") {
		t.Fatalf("expected path ending in common.props, got %s", got)
	}
}

func TestResolveImportPath_UnresolvedVariable(t *testing.T) {
	_, err := resolveImportPath("$(SomeCustomVar)\\file.props", "/a", "/b")
	if err == nil {
		t.Fatal("expected error for unresolved variable")
	}
}

func TestParseCsproj_ImplicitBuildProps(t *testing.T) {
	td := testDataDir(t)
	// ProjectA has only Newtonsoft.Json in its csproj.
	// Directory.Build.props at test-dotnet root adds Serilog.
	// shared_versions.props (imported by Directory.Build.props) adds Microsoft.Extensions.Logging.Abstractions.
	proj, err := ParseCsproj(filepath.Join(td, "ProjectA", "ProjectA.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "Newtonsoft.Json")
	assertContains(t, pkgNames, "Serilog")
	assertContains(t, pkgNames, "Microsoft.Extensions.Logging.Abstractions")

	// Serilog should be sourced from Directory.Build.props, not the csproj
	serilogSource := proj.SourceFileForPackage("Serilog")
	if filepath.Base(serilogSource) != "Directory.Build.props" {
		t.Fatalf("Serilog source should be Directory.Build.props, got %s", serilogSource)
	}

	// Newtonsoft.Json should be sourced from the csproj itself
	njSource := proj.SourceFileForPackage("Newtonsoft.Json")
	if filepath.Base(njSource) != "ProjectA.csproj" {
		t.Fatalf("Newtonsoft.Json source should be ProjectA.csproj, got %s", njSource)
	}
}

func TestParseCsproj_NestedPropsImport(t *testing.T) {
	td := testDataDir(t)
	// Any project under test-dotnet inherits Directory.Build.props which imports shared_versions.props.
	proj, err := ParseCsproj(filepath.Join(td, "ProjectA", "ProjectA.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "Microsoft.Extensions.Logging.Abstractions")

	// Should trace back to shared_versions.props
	source := proj.SourceFileForPackage("Microsoft.Extensions.Logging.Abstractions")
	if filepath.Base(source) != "shared_versions.props" {
		t.Fatalf("expected source shared_versions.props, got %s", source)
	}
}

func TestParseCsproj_ExplicitImport(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ProjectB", "ProjectB.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	// Own packages
	assertContains(t, pkgNames, "Newtonsoft.Json")
	assertContains(t, pkgNames, "StackExchange.Redis")
	// From explicit Import of imported.props
	assertContains(t, pkgNames, "Polly")
	// From implicit Directory.Build.props
	assertContains(t, pkgNames, "Serilog")

	// Polly should be sourced from imported.props
	pollySource := proj.SourceFileForPackage("Polly")
	if filepath.Base(pollySource) != "imported.props" {
		t.Fatalf("Polly source should be imported.props, got %s", pollySource)
	}
}

func TestParseCsproj_FSharpProject(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ProjectC", "ProjectC.fsproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	// Own packages
	assertContains(t, pkgNames, "FSharp.Core")
	assertContains(t, pkgNames, "Thoth.Json.Net")
	// From Directory.Build.props
	assertContains(t, pkgNames, "Serilog")
	// From nested shared_versions.props
	assertContains(t, pkgNames, "Microsoft.Extensions.Logging.Abstractions")

	if proj.FileName != "ProjectC.fsproj" {
		t.Fatalf("expected FileName ProjectC.fsproj, got %s", proj.FileName)
	}
}

func TestParseCsproj_CsprojTakesPrecedence(t *testing.T) {
	td := testDataDir(t)
	// ProjectD has its own packages.
	// Directory.Build.props also defines packages.
	// The csproj's own packages should have the csproj as their source.
	proj, err := ParseCsproj(filepath.Join(td, "ProjectD", "ProjectD.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	loggingSource := proj.SourceFileForPackage("Microsoft.Extensions.Logging")
	if filepath.Base(loggingSource) != "ProjectD.csproj" {
		t.Fatalf("Microsoft.Extensions.Logging source should be ProjectD.csproj, got %s", loggingSource)
	}
}

func TestParseCsproj_CircularRefProjectE(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ProjectE", "ProjectE.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "MediatR")
	assertContains(t, pkgNames, "FluentValidation")
	assertContains(t, pkgNames, "Serilog")
	assertContains(t, pkgNames, "Microsoft.Extensions.Logging.Abstractions")

	fvSource := proj.SourceFileForPackage("FluentValidation")
	if filepath.Base(fvSource) != "ProjectE.csproj" {
		t.Fatalf("FluentValidation source should be ProjectE.csproj, got %s", fvSource)
	}
}

func TestParseCsproj_CircularRefProjectF(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ProjectF", "ProjectF.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "AutoMapper")
	assertContains(t, pkgNames, "FluentValidation")
	assertContains(t, pkgNames, "Serilog")

	amSource := proj.SourceFileForPackage("AutoMapper")
	if filepath.Base(amSource) != "ProjectF.csproj" {
		t.Fatalf("AutoMapper source should be ProjectF.csproj, got %s", amSource)
	}
}

func TestParseCsproj_CircularRefSharedPropsSource(t *testing.T) {
	td := testDataDir(t)
	projE, err := ParseCsproj(filepath.Join(td, "ProjectE", "ProjectE.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	projF, err := ParseCsproj(filepath.Join(td, "ProjectF", "ProjectF.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	// Both should source Serilog from the same Directory.Build.props
	srcE := projE.SourceFileForPackage("Serilog")
	srcF := projF.SourceFileForPackage("Serilog")
	if srcE != srcF {
		t.Fatalf("expected same Serilog source for both projects, got %s vs %s", srcE, srcF)
	}
	if filepath.Base(srcE) != "Directory.Build.props" {
		t.Fatalf("expected Directory.Build.props, got %s", srcE)
	}
}

func TestSourceFileForPackage_Fallback(t *testing.T) {
	pp := &ParsedProject{
		FilePath:       "/some/project.csproj",
		PackageSources: map[string]string{},
	}
	got := pp.SourceFileForPackage("UnknownPackage")
	if got != "/some/project.csproj" {
		t.Fatalf("expected fallback to FilePath, got %s", got)
	}
}

func TestParsePropsFile_Valid(t *testing.T) {
	td := testDataDir(t)
	refs, imports, _, err := parsePropsFile(filepath.Join(td, "Directory.Build.props"))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].effectiveName() != "Serilog" {
		t.Fatalf("expected 1 ref (Serilog), got %d refs", len(refs))
	}
	if len(imports) != 1 {
		t.Fatalf("expected 1 import, got %d", len(imports))
	}
}

func TestParsePropsFile_NotFound(t *testing.T) {
	_, _, _, err := parsePropsFile("/nonexistent/file.props")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestParsePropsAsProject_DirectoryBuildProps(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParsePropsAsProject(filepath.Join(td, "Directory.Build.props"))
	if err != nil {
		t.Fatal(err)
	}

	if proj.FileName != "Directory.Build.props" {
		t.Fatalf("expected FileName Directory.Build.props, got %s", proj.FileName)
	}

	pkgs := pkgNameSet(proj)
	assertContains(t, pkgs, "Serilog")

	// Only direct packages — not nested imports (shared_versions.props)
	if pkgs["Microsoft.Extensions.Logging.Abstractions"] {
		t.Fatal("ParsePropsAsProject should not include packages from nested imports")
	}

	if proj.Packages.Len() != 1 {
		t.Fatalf("expected 1 package, got %d", proj.Packages.Len())
	}
}

func TestParsePropsAsProject_SharedVersionsProps(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParsePropsAsProject(filepath.Join(td, "shared_versions.props"))
	if err != nil {
		t.Fatal(err)
	}

	pkgs := pkgNameSet(proj)
	assertContains(t, pkgs, "Microsoft.Extensions.Logging.Abstractions")

	if proj.Packages.Len() != 1 {
		t.Fatalf("expected 1 package, got %d", proj.Packages.Len())
	}
}

func TestParsePropsAsProject_ImportedProps(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParsePropsAsProject(filepath.Join(td, "ProjectB", "imported.props"))
	if err != nil {
		t.Fatal(err)
	}

	pkgs := pkgNameSet(proj)
	assertContains(t, pkgs, "Polly")

	if proj.Packages.Len() != 1 {
		t.Fatalf("expected 1 package, got %d", proj.Packages.Len())
	}
}

func TestParsePropsAsProject_SourceMapping(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParsePropsAsProject(filepath.Join(td, "Directory.Build.props"))
	if err != nil {
		t.Fatal(err)
	}

	source := proj.SourceFileForPackage("Serilog")
	if filepath.Base(source) != "Directory.Build.props" {
		t.Fatalf("expected source to be Directory.Build.props, got %s", source)
	}
}

func TestParsePropsAsProject_NotFound(t *testing.T) {
	_, err := ParsePropsAsProject("/nonexistent/file.props")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestFindDirectoryPackagesProps_Found(t *testing.T) {
	td := testDataDir(t)
	got := findDirectoryPackagesProps(filepath.Join(td, "CPMProject"))
	if got == "" {
		t.Fatal("expected to find Directory.Packages.props, got empty string")
	}
	if filepath.Base(got) != "Directory.Packages.props" {
		t.Fatalf("expected Directory.Packages.props, got %s", filepath.Base(got))
	}
}

func TestFindDirectoryPackagesProps_NotFound(t *testing.T) {
	got := findDirectoryPackagesProps(os.TempDir())
	if got != "" {
		t.Fatalf("expected empty string, got %s", got)
	}
}

func TestParseCsproj_CPMVersionResolution(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "CPMProject", "CPMProject.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "Newtonsoft.Json")
	assertContains(t, pkgNames, "Polly")

	for ref := range proj.Packages {
		if ref.Version.Raw == "" {
			t.Fatalf("package %q has an empty version; CPM resolution failed", ref.Name)
		}
	}
}

func TestParseCsproj_CPMSource(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "CPMProject", "CPMProject.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	njSource := proj.SourceFileForPackage("Newtonsoft.Json")
	if filepath.Base(njSource) != "Directory.Packages.props" {
		t.Fatalf("Newtonsoft.Json source should be Directory.Packages.props, got %s", njSource)
	}
}

func TestParsePropsAsProject_CPMDirectoryPackagesProps(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParsePropsAsProject(filepath.Join(td, "CPMProject", "Directory.Packages.props"))
	if err != nil {
		t.Fatal(err)
	}

	pkgs := pkgNameSet(proj)
	assertContains(t, pkgs, "Newtonsoft.Json")
	assertContains(t, pkgs, "Polly")
	assertContains(t, pkgs, "Microsoft.Extensions.DependencyInjection")
	assertContains(t, pkgs, "Microsoft.Extensions.Hosting")

	if proj.Packages.Len() != 6 {
		t.Fatalf("expected 6 packages, got %d", proj.Packages.Len())
	}
}

func TestParseCsproj_CPMLib(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "CPMProject", "CPMProject.Lib", "CPMProject.Lib.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "Microsoft.Extensions.DependencyInjection")
	assertContains(t, pkgNames, "Newtonsoft.Json")

	for ref := range proj.Packages {
		if ref.Version.Raw == "" {
			t.Fatalf("package %q has empty version in CPMProject.Lib", ref.Name)
		}
	}

	diSource := proj.SourceFileForPackage("Microsoft.Extensions.DependencyInjection")
	if filepath.Base(diSource) != "Directory.Packages.props" {
		t.Fatalf("expected Directory.Packages.props, got %s", diSource)
	}
}

func TestParseCsproj_CPMVersionOverride(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "CPMProject", "CPMProject.Worker", "CPMProject.Worker.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "Newtonsoft.Json")
	assertContains(t, pkgNames, "Microsoft.Extensions.Hosting")

	var njVersion string
	for ref := range proj.Packages {
		if ref.Name == "Newtonsoft.Json" {
			njVersion = ref.Version.Raw
		}
	}
	if njVersion != "12.0.3" {
		t.Fatalf("expected VersionOverride 12.0.3, got %q", njVersion)
	}

	njSource := proj.SourceFileForPackage("Newtonsoft.Json")
	if filepath.Base(njSource) != "CPMProject.Worker.csproj" {
		t.Fatalf("VersionOverride source should be CPMProject.Worker.csproj, got %s", njSource)
	}

	hostingSource := proj.SourceFileForPackage("Microsoft.Extensions.Hosting")
	if filepath.Base(hostingSource) != "Directory.Packages.props" {
		t.Fatalf("Microsoft.Extensions.Hosting source should be Directory.Packages.props, got %s", hostingSource)
	}
}

func TestLatestStableForFramework_UnknownTargetDoesNotBlock(t *testing.T) {
	pkg := &PackageInfo{
		ID: "Some.Package",
		Versions: []PackageVersion{
			{
				SemVer: ParseSemVer("2.0.0"),
				Frameworks: []TargetFramework{
					ParseTargetFramework("netstandard2.0"),
					ParseTargetFramework("net8.0"),
				},
			},
		},
	}

	targets := NewSet[TargetFramework]()
	targets.Add(ParseTargetFramework("net8.0"))
	targets.Add(ParseTargetFramework("$(TargetFrameworksForLibraries)"))

	v := pkg.LatestStableForFramework(targets)
	if v == nil {
		t.Fatal("LatestStableForFramework returned nil with a FamilyUnknown target; want a compatible version")
	}
	if v.SemVer.String() != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %s", v.SemVer.String())
	}
}

func TestParseCsproj_ExactVersionLock(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ProjectG", "ProjectG.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	locked := make(map[string]bool)
	versions := make(map[string]string)
	for ref := range proj.Packages {
		locked[ref.Name] = ref.Locked
		versions[ref.Name] = ref.Version.Raw
	}

	if !locked["Newtonsoft.Json"] {
		t.Error("Newtonsoft.Json should be Locked=true (specified as [13.0.1])")
	}
	if versions["Newtonsoft.Json"] != "13.0.1" {
		t.Errorf("Newtonsoft.Json version: got %q, want 13.0.1", versions["Newtonsoft.Json"])
	}

	if locked["AutoMapper"] {
		t.Error("AutoMapper should be Locked=false (plain version)")
	}

	if locked["Polly"] {
		t.Error("Polly should be Locked=false (version range, not exact lock)")
	}
	if versions["Polly"] != "8.0.0" {
		t.Errorf("Polly version: got %q, want 8.0.0 (lower bound of range)", versions["Polly"])
	}
}

func pkgNameSet(proj *ParsedProject) map[string]bool {
	names := make(map[string]bool)
	for ref := range proj.Packages {
		names[ref.Name] = true
	}
	return names
}

func assertContains(t *testing.T, set map[string]bool, name string) {
	t.Helper()
	if !set[name] {
		t.Fatalf("expected package %q in set, got: %v", name, keys(set))
	}
}

func TestAddPackageVersion(t *testing.T) {
	content := `<Project>
  <PropertyGroup>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
  </PropertyGroup>
  <ItemGroup>
    <PackageVersion Include="Newtonsoft.Json" Version="13.0.4" />
  </ItemGroup>
</Project>`
	tmp := filepath.Join(t.TempDir(), "Directory.Packages.props")
	os.WriteFile(tmp, []byte(content), 0644)

	if err := AddPackageVersion(tmp, "Polly", "8.5.2"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(tmp)
	result := string(data)
	if !strings.Contains(result, `<PackageVersion Include="Polly" Version="8.5.2" />`) {
		t.Fatalf("expected PackageVersion element, got:\n%s", result)
	}
	if !strings.Contains(result, `<PackageVersion Include="Newtonsoft.Json" Version="13.0.4" />`) {
		t.Fatalf("original PackageVersion missing:\n%s", result)
	}
}

func TestAddPackageReference_NoVersion(t *testing.T) {
	content := `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup>
    <PackageReference Include="Newtonsoft.Json" />
  </ItemGroup>
</Project>`
	tmp := filepath.Join(t.TempDir(), "Test.csproj")
	os.WriteFile(tmp, []byte(content), 0644)

	if err := AddPackageReference(tmp, "Polly", ""); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(tmp)
	result := string(data)
	if !strings.Contains(result, `<PackageReference Include="Polly" />`) {
		t.Fatalf("expected version-less PackageReference, got:\n%s", result)
	}
	if strings.Contains(result, `Include="Polly" Version=`) {
		t.Fatalf("should not have Version attribute for CPM package:\n%s", result)
	}
}

func TestParseCsproj_AddTargets_Simple(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ProjectA", "ProjectA.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	if len(proj.AddTargets) < 3 {
		t.Fatalf("expected at least 3 AddTargets (project + Directory.Build.props + shared_versions.props), got %d", len(proj.AddTargets))
	}
	if proj.AddTargets[0].Kind != AddTargetProject {
		t.Fatalf("expected first target to be AddTargetProject, got %v", proj.AddTargets[0].Kind)
	}
	foundBuildProps := false
	foundImportedProps := false
	for _, at := range proj.AddTargets {
		switch at.Kind {
		case AddTargetBuildProps:
			foundBuildProps = true
		case AddTargetImportedProps:
			if strings.HasSuffix(at.FilePath, "shared_versions.props") {
				foundImportedProps = true
			}
		}
	}
	if !foundBuildProps {
		t.Fatal("expected AddTargetBuildProps target")
	}
	if !foundImportedProps {
		t.Fatal("expected AddTargetImportedProps for shared_versions.props (transitively imported via Directory.Build.props)")
	}
}

func TestParseCsproj_AddTargets_CPM(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "CPMProject", "CPMProject.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[AddTargetKind]bool)
	for _, at := range proj.AddTargets {
		kinds[at.Kind] = true
	}
	if !kinds[AddTargetProject] {
		t.Fatal("missing AddTargetProject")
	}
	if !kinds[AddTargetCPM] {
		t.Fatal("missing AddTargetCPM")
	}
}

func TestParseCsproj_AddTargets_ImportedProps(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ProjectB", "ProjectB.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, at := range proj.AddTargets {
		if at.Kind == AddTargetImportedProps {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected AddTargetImportedProps for ProjectB (imported.props)")
	}
}

func keys(m map[string]bool) []string {
	var result []string
	for k := range m {
		result = append(result, k)
	}
	return result
}
