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
	// Starting from a subdirectory, should find Directory.Build.props at test-dotnet root
	subDir := filepath.Join(td, "Scryfall")
	got := findDirectoryBuildProps(subDir)
	if got == "" {
		t.Fatal("expected to find Directory.Build.props, got empty string")
	}
	if filepath.Base(got) != "Directory.Build.props" {
		t.Fatalf("expected Directory.Build.props, got %s", filepath.Base(got))
	}
}

func TestFindDirectoryBuildProps_NotFound(t *testing.T) {
	// Use the OS temp dir root — no Directory.Build.props there
	got := findDirectoryBuildProps(os.TempDir())
	if got != "" {
		t.Fatalf("expected empty string, got %s", got)
	}
}

func TestResolveImportPath_Relative(t *testing.T) {
	got, err := resolveImportPath("build_info.props", "/proj/src", "/proj/src")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean("/proj/src/build_info.props")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveImportPath_ProjectDir(t *testing.T) {
	got, err := resolveImportPath("$(ProjectDir)\\build_info.props", "/proj/src", "/proj/src")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "build_info.props") {
		t.Fatalf("expected path ending in build_info.props, got %s", got)
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
	// HttpHelper has only Newtonsoft.Json in its csproj.
	// Directory.Build.props at test-dotnet root adds Serilog.
	// shared_versions.props (imported by Directory.Build.props) adds Microsoft.Extensions.Logging.Abstractions.
	proj, err := ParseCsproj(filepath.Join(td, "HttpHelper", "HttpHelper.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	// Should have its own package + the two from props
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
	if filepath.Base(njSource) != "HttpHelper.csproj" {
		t.Fatalf("Newtonsoft.Json source should be HttpHelper.csproj, got %s", njSource)
	}
}

func TestParseCsproj_NestedPropsImport(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "Serialization", "Serialization.csproj"))
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
	proj, err := ParseCsproj(filepath.Join(td, "Scryfall", "Scryfall.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	// Own packages
	assertContains(t, pkgNames, "Newtonsoft.Json")
	assertContains(t, pkgNames, "StackExchange.Redis")
	// From explicit Import of build_info.props
	assertContains(t, pkgNames, "Polly")
	// From implicit Directory.Build.props
	assertContains(t, pkgNames, "Serilog")

	// Polly should be sourced from build_info.props
	pollySource := proj.SourceFileForPackage("Polly")
	if filepath.Base(pollySource) != "build_info.props" {
		t.Fatalf("Polly source should be build_info.props, got %s", pollySource)
	}
}

func TestParseCsproj_FSharpProject(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "FSharpLib", "FSharpLib.fsproj"))
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

	if proj.FileName != "FSharpLib.fsproj" {
		t.Fatalf("expected FileName FSharpLib.fsproj, got %s", proj.FileName)
	}
}

func TestParseCsproj_CsprojTakesPrecedence(t *testing.T) {
	td := testDataDir(t)
	// CapchaValidator.csproj has its own packages.
	// Directory.Build.props also defines packages.
	// The csproj's own packages should have the csproj as their source.
	proj, err := ParseCsproj(filepath.Join(td, "CapchaValidator", "CapchaValidator.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	loggingSource := proj.SourceFileForPackage("Microsoft.Extensions.Logging")
	if filepath.Base(loggingSource) != "CapchaValidator.csproj" {
		t.Fatalf("Microsoft.Extensions.Logging source should be CapchaValidator.csproj, got %s", loggingSource)
	}
}

func TestParseCsproj_CircularRefServiceA(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ServiceA", "ServiceA.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	// Own packages
	assertContains(t, pkgNames, "MediatR")
	assertContains(t, pkgNames, "FluentValidation")
	// From Directory.Build.props
	assertContains(t, pkgNames, "Serilog")
	// From nested shared_versions.props
	assertContains(t, pkgNames, "Microsoft.Extensions.Logging.Abstractions")

	// FluentValidation is defined in its own csproj, not a props file
	fvSource := proj.SourceFileForPackage("FluentValidation")
	if filepath.Base(fvSource) != "ServiceA.csproj" {
		t.Fatalf("FluentValidation source should be ServiceA.csproj, got %s", fvSource)
	}
}

func TestParseCsproj_CircularRefServiceB(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "ServiceB", "ServiceB.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	// Own packages
	assertContains(t, pkgNames, "AutoMapper")
	assertContains(t, pkgNames, "FluentValidation")
	// From Directory.Build.props
	assertContains(t, pkgNames, "Serilog")

	// AutoMapper is defined in its own csproj
	amSource := proj.SourceFileForPackage("AutoMapper")
	if filepath.Base(amSource) != "ServiceB.csproj" {
		t.Fatalf("AutoMapper source should be ServiceB.csproj, got %s", amSource)
	}
}

func TestParseCsproj_CircularRefSharedPropsSource(t *testing.T) {
	td := testDataDir(t)
	projA, err := ParseCsproj(filepath.Join(td, "ServiceA", "ServiceA.csproj"))
	if err != nil {
		t.Fatal(err)
	}
	projB, err := ParseCsproj(filepath.Join(td, "ServiceB", "ServiceB.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	// Both should source Serilog from the same Directory.Build.props
	srcA := projA.SourceFileForPackage("Serilog")
	srcB := projB.SourceFileForPackage("Serilog")
	if srcA != srcB {
		t.Fatalf("expected same Serilog source for both projects, got %s vs %s", srcA, srcB)
	}
	if filepath.Base(srcA) != "Directory.Build.props" {
		t.Fatalf("expected Directory.Build.props, got %s", srcA)
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

func TestParsePropsAsProject_BuildInfoProps(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParsePropsAsProject(filepath.Join(td, "Scryfall", "build_info.props"))
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

	// Verify versions were resolved from Directory.Packages.props, not left empty.
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

	// Packages resolved via CPM should be sourced from Directory.Packages.props,
	// not the project file — so the TUI directs edits to the right file.
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

	if proj.Packages.Len() != 4 {
		t.Fatalf("expected 4 packages, got %d", proj.Packages.Len())
	}
}

// Multi-project CPM: sub-project in the same tree shares Directory.Packages.props.
func TestParseCsproj_CPMLib(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "CPMProject", "CPMProject.Lib", "CPMProject.Lib.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "Microsoft.Extensions.DependencyInjection")
	assertContains(t, pkgNames, "Newtonsoft.Json")

	// Versions must be resolved from the parent Directory.Packages.props.
	for ref := range proj.Packages {
		if ref.Version.Raw == "" {
			t.Fatalf("package %q has empty version in CPMProject.Lib", ref.Name)
		}
	}

	// Both packages should be sourced from Directory.Packages.props.
	diSource := proj.SourceFileForPackage("Microsoft.Extensions.DependencyInjection")
	if filepath.Base(diSource) != "Directory.Packages.props" {
		t.Fatalf("expected Directory.Packages.props, got %s", diSource)
	}
}

// VersionOverride: a project pins a different version than the central props.
func TestParseCsproj_CPMVersionOverride(t *testing.T) {
	td := testDataDir(t)
	proj, err := ParseCsproj(filepath.Join(td, "CPMProject", "CPMProject.Worker", "CPMProject.Worker.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	pkgNames := pkgNameSet(proj)
	assertContains(t, pkgNames, "Newtonsoft.Json")
	assertContains(t, pkgNames, "Microsoft.Extensions.Hosting")

	// Newtonsoft.Json uses VersionOverride="12.0.3" in the worker csproj.
	var njVersion string
	for ref := range proj.Packages {
		if ref.Name == "Newtonsoft.Json" {
			njVersion = ref.Version.Raw
		}
	}
	if njVersion != "12.0.3" {
		t.Fatalf("expected VersionOverride 12.0.3, got %q", njVersion)
	}

	// The overridden package's source should be the project file, not Directory.Packages.props.
	njSource := proj.SourceFileForPackage("Newtonsoft.Json")
	if filepath.Base(njSource) != "CPMProject.Worker.csproj" {
		t.Fatalf("VersionOverride source should be CPMProject.Worker.csproj, got %s", njSource)
	}

	// Microsoft.Extensions.Hosting has no override — resolves from CPM.
	hostingSource := proj.SourceFileForPackage("Microsoft.Extensions.Hosting")
	if filepath.Base(hostingSource) != "Directory.Packages.props" {
		t.Fatalf("Microsoft.Extensions.Hosting source should be Directory.Packages.props, got %s", hostingSource)
	}
}

// TestLatestStableForFramework_UnknownTargetDoesNotBlock verifies that an
// unresolved MSBuild property reference (e.g. $(TargetFrameworksForLibraries))
// parsed as FamilyUnknown does not cause LatestStableForFramework to return nil
// for packages that are otherwise compatible with the known targets.
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

	// Mix of a resolved (net8.0) and an unresolved ($(Property)) target.
	targets := NewSet[TargetFramework]()
	targets.Add(ParseTargetFramework("net8.0"))
	targets.Add(ParseTargetFramework("$(TargetFrameworksForLibraries)")) // FamilyUnknown

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
	proj, err := ParseCsproj(filepath.Join(td, "PinnedPackages", "PinnedPackages.csproj"))
	if err != nil {
		t.Fatal(err)
	}

	locked := make(map[string]bool)
	versions := make(map[string]string)
	for ref := range proj.Packages {
		locked[ref.Name] = ref.Locked
		versions[ref.Name] = ref.Version.Raw
	}

	// [13.0.1] — exact lock, no comma
	if !locked["Newtonsoft.Json"] {
		t.Error("Newtonsoft.Json should be Locked=true (specified as [13.0.1])")
	}
	if versions["Newtonsoft.Json"] != "13.0.1" {
		t.Errorf("Newtonsoft.Json version: got %q, want 13.0.1", versions["Newtonsoft.Json"])
	}

	// 3.1.1 — plain version, not locked
	if locked["Serilog"] {
		t.Error("Serilog should be Locked=false (plain version)")
	}

	// [7.0.0,) — range with comma, not an exact lock
	if locked["Microsoft.Extensions.Http"] {
		t.Error("Microsoft.Extensions.Http should be Locked=false (version range, not exact lock)")
	}
	if versions["Microsoft.Extensions.Http"] != "7.0.0" {
		t.Errorf("Microsoft.Extensions.Http version: got %q, want 7.0.0 (lower bound of range)", versions["Microsoft.Extensions.Http"])
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

func keys(m map[string]bool) []string {
	var result []string
	for k := range m {
		result = append(result, k)
	}
	return result
}
