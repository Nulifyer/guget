package msbuildstatic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func noEnv(string) (string, bool) { return "", false }

func evaluate(t *testing.T, root, project string) *Evaluation {
	t.Helper()
	result, err := Evaluate(filepath.Join(root, filepath.FromSlash(project)), Options{LookupEnv: noEnv})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) > 0 {
		t.Fatalf("errors: %v", result.Errors)
	}
	return result
}

func reference(t *testing.T, result *Evaluation, id string) Package {
	t.Helper()
	for _, pkg := range result.References {
		if strings.EqualFold(pkg.ID, id) {
			return pkg
		}
	}
	t.Fatalf("no reference %s in %+v", id, result.References)
	return Package{}
}

func hasReference(result *Evaluation, id string) bool {
	for _, pkg := range result.References {
		if strings.EqualFold(pkg.ID, id) {
			return true
		}
	}
	return false
}

func path(root, name string) string { return filepath.Join(root, filepath.FromSlash(name)) }

const cpmOn = `<PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup>`

func sdkProject(items string) string {
	return `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup>` + items + `</ItemGroup></Project>`
}

func TestNestedCentralFilesFollowBothGetPathOfFileAboveForms(t *testing.T) {
	for name, importExpression := range map[string]string{
		"two arguments unquoted": `$([MSBuild]::GetPathOfFileAbove(Directory.Packages.props, $(MSBuildThisFileDirectory)..))`,
		"two arguments quoted":   `$([MSBuild]::GetPathOfFileAbove('Directory.Packages.props', '$(MSBuildThisFileDirectory)../'))`,
	} {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{
				"Directory.Packages.props":     `<Project>` + cpmOn + `<ItemGroup><PackageVersion Include="Parent.Only" Version="1.0.0" /><PackageVersion Include="Shared" Version="1.0.0" /></ItemGroup></Project>`,
				"src/Directory.Packages.props": `<Project><Import Project="` + importExpression + `" /><ItemGroup><PackageVersion Update="Shared" Version="2.0.0" /><PackageVersion Include="Nested.Only" Version="3.0.0" /></ItemGroup></Project>`,
				"src/App/App.csproj":           sdkProject(`<PackageReference Include="Parent.Only" /><PackageReference Include="Shared" /><PackageReference Include="Nested.Only" />`),
			})
			result := evaluate(t, root, "src/App/App.csproj")
			if !result.CentralPackageManagement || result.DirectoryPackagesProps != path(root, "src/Directory.Packages.props") {
				t.Fatalf("cpm=%v central=%s", result.CentralPackageManagement, result.DirectoryPackagesProps)
			}
			for id, want := range map[string][2]string{
				"Parent.Only": {"1.0.0", path(root, "Directory.Packages.props")},
				"Shared":      {"2.0.0", path(root, "src/Directory.Packages.props")},
				"Nested.Only": {"3.0.0", path(root, "src/Directory.Packages.props")},
			} {
				pkg := reference(t, result, id)
				if pkg.Version != want[0] || pkg.VersionOwner != want[1] || pkg.VersionSource != VersionCentral || pkg.Problem != "" {
					t.Errorf("%s = %+v, want %v", id, pkg, want)
				}
			}
		})
	}
}

func TestOneArgumentGetPathOfFileAboveStartsInDeclaringDirectory(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.props": `<Project><PropertyGroup><FromRoot>yes</FromRoot></PropertyGroup></Project>`,
		"build/Versions.props":  `<Project><Import Project="$([MSBuild]::GetPathOfFileAbove('Directory.Build.props'))" /></Project>`,
		"src/App/Directory.Build.props": `<Project><Import Project="$(MSBuildThisFileDirectory)../../build/Versions.props" />` +
			// The one-argument form here finds this file itself, which MSBuild skips.
			`<Import Project="$([MSBuild]::GetPathOfFileAbove(Directory.Build.props))" /></Project>`,
		"src/App/App.csproj": sdkProject(``),
	})
	result := evaluate(t, root, "src/App/App.csproj")
	if text, known, _ := result.Property("FromRoot"); text != "yes" || !known {
		t.Fatalf("FromRoot = %q known=%v; imports %+v", text, known, result.Imports)
	}
}

func TestDuplicatePackageVersionFirstWins(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Packages.props": `<Project>` + cpmOn + `<ItemGroup><PackageVersion Include="Dup" Version="1.0.0" /><PackageVersion Include="dup" Version="2.0.0" /></ItemGroup></Project>`,
		"App/App.csproj":           sdkProject(`<PackageReference Include="Dup" />`),
	})
	result := evaluate(t, root, "App/App.csproj")
	pkg := reference(t, result, "Dup")
	if pkg.Version != "1.0.0" || !result.CentralVersions[0].Duplicate {
		t.Fatalf("pkg = %+v central = %+v", pkg, result.CentralVersions)
	}
}

func TestGlobalPackageReferenceIsOwnedByDeclaringFile(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Packages.props": `<Project>` + cpmOn + `<ItemGroup><GlobalPackageReference Include="Analyzer" Version="1.2.3" /></ItemGroup></Project>`,
		"Directory.Build.targets":  `<Project><ItemGroup><GlobalPackageReference Include="TooLate" Version="1.0.0" /></ItemGroup></Project>`,
		"App/App.csproj":           sdkProject(``),
	})
	result := evaluate(t, root, "App/App.csproj")
	pkg := reference(t, result, "Analyzer")
	central := path(root, "Directory.Packages.props")
	if !pkg.Global || pkg.Version != "1.2.3" || pkg.ReferenceOwner != central || pkg.VersionOwner != central || pkg.PrivateAssets != "All" || pkg.VersionSource != VersionGlobal {
		t.Fatalf("global = %+v", pkg)
	}
	if hasReference(result, "TooLate") {
		t.Fatal("GlobalPackageReference in Directory.Build.targets is evaluated after NuGet.targets")
	}
}

func TestGlobalPackageReferenceNeedsCentralPackageManagement(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Packages.props": `<Project><ItemGroup><GlobalPackageReference Include="Analyzer" Version="1.2.3" /></ItemGroup></Project>`,
		"App/App.csproj":           sdkProject(``),
	})
	if hasReference(evaluate(t, root, "App/App.csproj"), "Analyzer") {
		t.Fatal("GlobalPackageReference expanded without CPM")
	}
}

func TestDirectoryPackagesPropsPathOverride(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.props":    `<Project><PropertyGroup><DirectoryPackagesPropsPath>$(MSBuildThisFileDirectory)eng/Versions.props</DirectoryPackagesPropsPath></PropertyGroup></Project>`,
		"Directory.Packages.props": `<Project>` + cpmOn + `<ItemGroup><PackageVersion Include="Pkg" Version="9.9.9" /></ItemGroup></Project>`,
		"eng/Versions.props":       `<Project>` + cpmOn + `<ItemGroup><PackageVersion Include="Pkg" Version="1.0.0" /></ItemGroup></Project>`,
		"App/App.csproj":           sdkProject(`<PackageReference Include="Pkg" />`),
	})
	result := evaluate(t, root, "App/App.csproj")
	if result.DirectoryPackagesProps != path(root, "eng/Versions.props") || reference(t, result, "Pkg").Version != "1.0.0" {
		t.Fatalf("central=%s pkg=%+v", result.DirectoryPackagesProps, reference(t, result, "Pkg"))
	}
}

func TestImportDirectoryPackagesPropsFalse(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.props":    `<Project><PropertyGroup><ImportDirectoryPackagesProps>false</ImportDirectoryPackagesProps></PropertyGroup></Project>`,
		"Directory.Packages.props": `<Project>` + cpmOn + `</Project>`,
		"App/App.csproj":           sdkProject(``),
	})
	result := evaluate(t, root, "App/App.csproj")
	if result.DirectoryPackagesProps != "" || result.CentralPackageManagement || !result.CentralPackageManagementKnown {
		t.Fatalf("central=%q cpm=%v", result.DirectoryPackagesProps, result.CentralPackageManagement)
	}
}

func TestCentralSwitchFollowsEvaluationOrder(t *testing.T) {
	setting := func(value string) string {
		return `<PropertyGroup><ManagePackageVersionsCentrally>` + value + `</ManagePackageVersionsCentrally></PropertyGroup>`
	}
	for _, test := range []struct {
		name, buildProps, packagesProps, project, buildTargets string
		want                                                   bool
	}{
		{name: "central file overrides build props", buildProps: setting("true"), packagesProps: setting("false"), want: false},
		{name: "build props alone", buildProps: setting("true"), want: true},
		{name: "project overrides central file", packagesProps: setting("false"), project: setting("true"), want: true},
		{name: "build targets is too late", buildTargets: setting("true"), want: false},
		{name: "self default", packagesProps: `<PropertyGroup><ManagePackageVersionsCentrally Condition="'$(ManagePackageVersionsCentrally)' == ''">true</ManagePackageVersionsCentrally></PropertyGroup>`, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := map[string]string{
				"Directory.Packages.props": `<Project>` + test.packagesProps + `</Project>`,
				"App/App.csproj":           `<Project Sdk="Microsoft.NET.Sdk">` + test.project + `</Project>`,
			}
			if test.buildProps != "" {
				files["Directory.Build.props"] = `<Project>` + test.buildProps + `</Project>`
			}
			if test.buildTargets != "" {
				files["Directory.Build.targets"] = `<Project>` + test.buildTargets + `</Project>`
			}
			result := evaluate(t, writeTree(t, files), "App/App.csproj")
			if result.CentralPackageManagement != test.want || !result.CentralPackageManagementKnown {
				t.Fatalf("cpm = %v known=%v, want %v", result.CentralPackageManagement, result.CentralPackageManagementKnown, test.want)
			}
		})
	}
}

func TestUnknownCentralSwitchIsReported(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Packages.props": `<Project><PropertyGroup Condition="'$(UseCPM)' == 'true'"><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup></Project>`,
		"App/App.csproj":           sdkProject(``),
	})
	result := evaluate(t, root, "App/App.csproj")
	if result.CentralPackageManagement || result.CentralPackageManagementKnown {
		t.Fatalf("cpm=%v known=%v", result.CentralPackageManagement, result.CentralPackageManagementKnown)
	}
}

func TestEnvironmentPropertiesAreVisible(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Packages.props": `<Project><PropertyGroup Condition="'$(UseCPM)' == 'true'"><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup></Project>`,
		"App/App.csproj":           sdkProject(``),
	})
	result, err := Evaluate(path(root, "App/App.csproj"), Options{LookupEnv: func(name string) (string, bool) {
		if name == "UseCPM" {
			return "true", true
		}
		return "", false
	}})
	if err != nil || !result.CentralPackageManagement {
		t.Fatalf("err=%v cpm=%v", err, result.CentralPackageManagement)
	}
}

func TestVersionOnCentrallyManagedReferenceIsNU1008(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Packages.props": `<Project>` + cpmOn + `<ItemGroup><PackageVersion Include="Pkg" Version="1.0.0" /></ItemGroup></Project>`,
		"App/App.csproj":           sdkProject(`<PackageReference Include="Pkg" Version="2.0.0" /><PackageReference Include="Over" VersionOverride="3.0.0" />`),
	})
	result := evaluate(t, root, "App/App.csproj")
	if pkg := reference(t, result, "Pkg"); !strings.HasPrefix(pkg.Problem, "NU1008") || pkg.Version != "2.0.0" {
		t.Fatalf("pkg = %+v", pkg)
	}
	if pkg := reference(t, result, "Over"); pkg.Problem != "" || pkg.VersionSource != VersionOverride || pkg.Version != "3.0.0" {
		t.Fatalf("override = %+v", pkg)
	}
}

func TestCentralVersionProblems(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Packages.props": `<Project>` + cpmOn + `<PropertyGroup><CentralPackageVersionOverrideEnabled>false</CentralPackageVersionOverrideEnabled></PropertyGroup><ItemGroup><PackageVersion Include="Float" Version="1.*" /></ItemGroup></Project>`,
		"App/App.csproj":           sdkProject(`<PackageReference Include="Float" /><PackageReference Include="Missing" /><PackageReference Include="Over" VersionOverride="1.0.0" />`),
	})
	result := evaluate(t, root, "App/App.csproj")
	for id, code := range map[string]string{"Float": "NU1011", "Missing": "NU1010", "Over": "NU1013"} {
		if pkg := reference(t, result, id); !strings.HasPrefix(pkg.Problem, code) {
			t.Errorf("%s problem = %q, want %s", id, pkg.Problem, code)
		}
	}
}

func TestUpdateWithoutIncludeDeclaresNothing(t *testing.T) {
	root := writeTree(t, map[string]string{
		"App/App.csproj": sdkProject(`<PackageReference Update="Ghost" Version="1.0.0" /><PackageReference Include="Real" Version="1.0.0" /><PackageReference Update="Real" Version="2.0.0" />`),
	})
	result := evaluate(t, root, "App/App.csproj")
	if hasReference(result, "Ghost") {
		t.Fatal("Update without an earlier Include became a declaration")
	}
	if pkg := reference(t, result, "Real"); pkg.Version != "2.0.0" {
		t.Fatalf("Real = %+v", pkg)
	}
}

func TestBuildPropsItemsPrecedeProjectItems(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.props": `<Project><ItemGroup><PackageReference Include="Both" Version="1.0.0" /></ItemGroup></Project>`,
		"App/App.csproj":        sdkProject(`<PackageReference Include="Both" Version="2.0.0" />`),
	})
	result := evaluate(t, root, "App/App.csproj")
	pkg := reference(t, result, "Both")
	if pkg.Version != "1.0.0" || pkg.ReferenceOwner != path(root, "Directory.Build.props") || !pkg.Duplicate {
		t.Fatalf("pkg = %+v", pkg)
	}
}

func TestDirectoryBuildTargetsDeclarationsAreRead(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.targets": `<Project><ItemGroup><PackageReference Include="FromTargets" Version="1.0.0" /><PackageReference Update="Local" Version="5.0.0" /></ItemGroup></Project>`,
		"App/App.csproj":          sdkProject(`<PackageReference Include="Local" Version="1.0.0" />`),
	})
	result := evaluate(t, root, "App/App.csproj")
	targets := path(root, "Directory.Build.targets")
	if result.DirectoryBuildTargets != targets {
		t.Fatalf("targets = %q", result.DirectoryBuildTargets)
	}
	if pkg := reference(t, result, "FromTargets"); pkg.ReferenceOwner != targets {
		t.Fatalf("FromTargets = %+v", pkg)
	}
	if pkg := reference(t, result, "Local"); pkg.Version != "5.0.0" || pkg.VersionOwner != targets || pkg.ReferenceOwner != path(root, "App/App.csproj") {
		t.Fatalf("Local = %+v", pkg)
	}
}

func TestPackageDownloadsAndProjectReferences(t *testing.T) {
	root := writeTree(t, map[string]string{
		"App/App.csproj": sdkProject(`<PackageDownload Include="Tool.A;Tool.B" Version="[1.0.0]" /><ProjectReference Include="..\Lib\Lib.csproj" />`),
		"Lib/Lib.csproj": sdkProject(``),
	})
	result := evaluate(t, root, "App/App.csproj")
	if len(result.Downloads) != 2 || result.Downloads[1].ID != "Tool.B" || result.Downloads[0].Version != "[1.0.0]" {
		t.Fatalf("downloads = %+v", result.Downloads)
	}
	if hasReference(result, "Tool.A") {
		t.Fatal("PackageDownload is not a dependency")
	}
	if len(result.ProjectReferences) != 1 || result.ProjectReferences[0] != path(root, "Lib/Lib.csproj") {
		t.Fatalf("project references = %v", result.ProjectReferences)
	}
}

func TestPropertiesEvaluateInOrderWithSelfReferences(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.props": `<Project><PropertyGroup><Ver>1.0.0</Ver><Sources>a</Sources></PropertyGroup></Project>`,
		"App/App.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><Sources>$(Sources);b</Sources></PropertyGroup>` +
			`<ItemGroup><PackageReference Include="Pkg" Version="$(Ver)" /></ItemGroup><PropertyGroup><Ver>2.0.0</Ver></PropertyGroup></Project>`,
	})
	result := evaluate(t, root, "App/App.csproj")
	if text, _, _ := result.Property("Sources"); text != "a;b" {
		t.Fatalf("Sources = %q", text)
	}
	// Items see the final property values.
	if pkg := reference(t, result, "Pkg"); pkg.Version != "2.0.0" || !pkg.VersionKnown {
		t.Fatalf("Pkg = %+v", pkg)
	}
}

func TestConditionsOnKnownAndUnknownValues(t *testing.T) {
	root := writeTree(t, map[string]string{
		"App/App.csproj": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>` +
			`<ItemGroup Condition="'$(TargetFramework)' == 'net8.0'"><PackageReference Include="Known" Version="1.0.0" /></ItemGroup>` +
			`<ItemGroup Condition="'$(TargetFramework)' == 'net9.0'"><PackageReference Include="Never" Version="1.0.0" /></ItemGroup>` +
			`<ItemGroup Condition="'$(Configuration)' == 'Debug'"><PackageReference Include="Maybe" Version="1.0.0" /></ItemGroup>` +
			`<ItemGroup Condition="Exists('missing.props') Or !Exists('App.csproj')"><PackageReference Include="Absent" Version="1.0.0" /></ItemGroup></Project>`,
	})
	result := evaluate(t, root, "App/App.csproj")
	if pkg := reference(t, result, "Known"); pkg.Conditional {
		t.Fatalf("Known = %+v", pkg)
	}
	if hasReference(result, "Never") || hasReference(result, "Absent") {
		t.Fatal("false conditions were included")
	}
	if pkg := reference(t, result, "Maybe"); !pkg.Conditional {
		t.Fatalf("Maybe = %+v", pkg)
	}
}

func TestLockFileProperties(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.props": `<Project><PropertyGroup><RestorePackagesWithLockFile>true</RestorePackagesWithLockFile><RestoreLockedMode Condition="'$(ContinuousIntegrationBuild)' == 'true'">true</RestoreLockedMode></PropertyGroup></Project>`,
		"App/App.csproj":        sdkProject(``),
	})
	result := evaluate(t, root, "App/App.csproj")
	if text, known, _ := result.Property("RestorePackagesWithLockFile"); text != "true" || !known {
		t.Fatalf("RestorePackagesWithLockFile = %q %v", text, known)
	}
	if _, known, _ := result.Property("RestoreLockedMode"); known {
		t.Fatal("conditional RestoreLockedMode should be unknown")
	}
}

func TestMissingUnconditionalImportIsAnError(t *testing.T) {
	root := writeTree(t, map[string]string{
		"App/App.csproj": `<Project Sdk="Microsoft.NET.Sdk"><Import Project="missing.props" /><Import Project="optional.props" Condition="Exists('optional.props')" /></Project>`,
	})
	result, err := Evaluate(path(root, "App/App.csproj"), Options{LookupEnv: noEnv})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Error(), "missing.props") {
		t.Fatalf("errors = %v", result.Errors)
	}
}

func TestInputsRecordEveryFileRead(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.props":    `<Project />`,
		"Directory.Packages.props": `<Project />`,
		"Directory.Build.targets":  `<Project />`,
		"App/App.csproj":           sdkProject(``),
	})
	result := evaluate(t, root, "App/App.csproj")
	for _, name := range []string{"Directory.Build.props", "Directory.Packages.props", "Directory.Build.targets", "App/App.csproj"} {
		if _, ok := result.Inputs[path(root, name)]; !ok {
			t.Errorf("missing input %s", name)
		}
	}
}

func TestParseCacheReusesOnlyIdenticalBytes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Directory.Build.props": `<Project><ItemGroup><PackageReference Include="Shared" Version="1.0.0"/></ItemGroup></Project>`,
		"A/A.csproj":            sdkProject(``),
		"B/B.csproj":            sdkProject(``),
	})
	cache := NewParseCache()
	run := func(project string) *Evaluation {
		t.Helper()
		result, err := Evaluate(path(root, project), Options{LookupEnv: noEnv, Cache: cache})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	props := path(root, "Directory.Build.props")
	first := run("A/A.csproj")
	if got := reference(t, first, "Shared").Version; got != "1.0.0" {
		t.Fatalf("first version %q", got)
	}
	if again := run("A/A.csproj"); reference(t, again, "Shared") != reference(t, evaluate(t, root, "A/A.csproj"), "Shared") {
		t.Fatal("cached evaluation differs from an uncached one")
	}
	if err := os.WriteFile(props, []byte(`<Project><ItemGroup><PackageReference Include="Shared" Version="2.0.0"/></ItemGroup></Project>`), 0o644); err != nil {
		t.Fatal(err)
	}
	second := run("B/B.csproj")
	if got := reference(t, second, "Shared").Version; got != "2.0.0" {
		t.Fatalf("changed import served from the cache: version %q", got)
	}
	if first.Inputs[props] == second.Inputs[props] {
		t.Fatal("evaluations did not record the revision each consumed")
	}
}

func TestUndecidableVersionOverrideSwitchIsAGap(t *testing.T) {
	for _, test := range []struct {
		value string
		gap   bool
	}{{"", false}, {"true", false}, {"false", false}, {"$(FromSdk)", true}} {
		props := `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>`
		if test.value != "" {
			props += `<CentralPackageVersionOverrideEnabled>` + test.value + `</CentralPackageVersionOverrideEnabled>`
		}
		root := writeTree(t, map[string]string{
			"Directory.Packages.props": props + `</PropertyGroup><ItemGroup><PackageVersion Include="A" Version="1.0.0"/></ItemGroup></Project>`,
			"App/App.csproj":           sdkProject(`<PackageReference Include="A" VersionOverride="2.0.0"/>`),
		})
		result := evaluate(t, root, "App/App.csproj")
		gap := strings.Contains(strings.Join(result.Gaps, "\n"), "CentralPackageVersionOverrideEnabled")
		if gap != test.gap {
			t.Errorf("CentralPackageVersionOverrideEnabled=%q: gap=%v, want %v (%v)", test.value, gap, test.gap, result.Gaps)
		}
		if owner := reference(t, result, "A").VersionOwner; owner != path(root, "App/App.csproj") {
			t.Errorf("override owner = %s", owner)
		}
	}
}
