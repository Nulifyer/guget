//go:build integration

package main

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// definingFile is the file MSBuild reports as declaring the first
// PackageReference item for id (DefiningProjectFullPath).
func (o *packageOracle) definingFile(t *testing.T, project, id string) string {
	t.Helper()
	output := o.mustRun(t, filepath.Dir(project), "msbuild", project, "-getItem:PackageReference")
	var result struct {
		Items map[string][]map[string]string
	}
	if err := decodeOracleJSON(output, &result); err != nil {
		t.Fatalf("decode msbuild output: %v\n%s", err, output)
	}
	for _, item := range result.Items["PackageReference"] {
		if strings.EqualFold(item["Identity"], id) {
			return item["DefiningProjectFullPath"]
		}
	}
	t.Fatalf("MSBuild reports no PackageReference %s:\n%s", id, output)
	return ""
}

// restoredVersion restores project and returns the version of id in its
// assets file, or the restore output when restore fails.
func (o *packageOracle) restoredVersion(t *testing.T, repo, project, id string) (string, string, error) {
	t.Helper()
	output, err := o.run(t, repo, "restore", project)
	if err != nil {
		return "", output, err
	}
	var assets struct {
		Libraries map[string]json.RawMessage `json:"libraries"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(filepath.Dir(project), "obj", "project.assets.json"))), &assets); err != nil {
		t.Fatal(err)
	}
	for key := range assets.Libraries {
		if name, version, ok := strings.Cut(key, "/"); ok && strings.EqualFold(name, id) {
			return version, output, nil
		}
	}
	return "", output, nil
}

// withoutDotnet runs fn with no dotnet on PATH, so a regression to MSBuild
// evaluation fails instead of silently taking the slow path.
func withoutDotnet(t *testing.T, fn func()) {
	t.Helper()
	path := os.Getenv("PATH")
	os.Setenv("PATH", t.TempDir())
	defer os.Setenv("PATH", path)
	fn()
}

// snapshotFiles reads every file of the workspace except restore output.
func snapshotFiles(t *testing.T, repo string) map[string]string {
	t.Helper()
	files := map[string]string{}
	filepath.WalkDir(repo, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "obj" || entry.Name() == "bin") {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			rel, _ := filepath.Rel(repo, path)
			files[filepath.ToSlash(rel)] = readFile(t, path)
		}
		return nil
	})
	return files
}

// TestVersionOverrideEditsMatchDotnet checks the static edit decision for
// VersionOverride declarations against MSBuild, dotnet add package, and
// restore: the owner static evaluation chooses is the file MSBuild reports,
// the TUI's static path edits it without starting the SDK, the CLI, which
// evaluates with MSBuild, makes the same edit, and the result restores the
// same version as dotnet's own edit. Declarations static evaluation must not
// decide keep MSBuild evaluation or are refused like restore refuses them.
func TestVersionOverrideEditsMatchDotnet(t *testing.T) {
	oracle := newPackageOracle(t)
	const central = `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /><PackageVersion Include="Oracle.B" Version="1.0.0" /></ItemGroup>`
	cpm := func(extra string) string { return `<Project>` + oracleCPM + extra + central + `</Project>` }

	for _, test := range []struct {
		name  string
		files map[string]string
		owner string // repository-relative file that declares the override
		// compareDotnet applies dotnet add package to a copy and requires the
		// same evaluated result; dotnet adds a separate Version without CPM.
		compareDotnet bool
	}{
		{name: "attribute", owner: "App/App.csproj", compareDotnet: true, files: map[string]string{
			"Directory.Packages.props": cpm(""),
			"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" VersionOverride="2.0.0" /><PackageReference Include="Oracle.B" />`),
		}},
		{name: "child element", owner: "App/App.csproj", compareDotnet: true, files: map[string]string{
			"Directory.Packages.props": cpm(""),
			"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A"><VersionOverride>2.0.0</VersionOverride></PackageReference><PackageReference Include="Oracle.B" />`),
		}},
		{name: "overrides explicitly enabled", owner: "App/App.csproj", compareDotnet: true, files: map[string]string{
			"Directory.Packages.props": cpm(`<PropertyGroup><CentralPackageVersionOverrideEnabled>true</CentralPackageVersionOverrideEnabled></PropertyGroup>`),
			"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" VersionOverride="2.0.0" />`),
		}},
		{name: "declared in an imported file", owner: "App/Overrides.props", compareDotnet: true, files: map[string]string{
			"Directory.Packages.props": cpm(""),
			"App/Overrides.props":      `<Project><ItemGroup><PackageReference Include="Oracle.A" VersionOverride="2.0.0" /></ItemGroup></Project>`,
			"App/App.csproj":           `<Project Sdk="Microsoft.NET.Sdk">{{TFM}}<Import Project="Overrides.props" /><ItemGroup><PackageReference Include="Oracle.B" /></ItemGroup></Project>`,
		}},
		{name: "without central package management", owner: "App/App.csproj", files: map[string]string{
			"App/App.csproj": oracleSDKProject(`<PackageReference Include="Oracle.A" VersionOverride="2.0.0" />`),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := oracle.workspace(t, test.files)
			project := filepath.Join(repo, "App", "App.csproj")
			parsed := requireSameView(t, oracle, project)
			wantOwner := filepath.Join(repo, filepath.FromSlash(test.owner))
			// MSBuild expands a short (8.3) directory name such as RUNNER~1.
			if got := oracle.definingFile(t, project, "Oracle.A"); outputPathIdentity(got) != outputPathIdentity(wantOwner) {
				t.Fatalf("MSBuild defines Oracle.A in %s, want %s", got, wantOwner)
			}
			if got := parsed.SourceFileForPackage("Oracle.A"); got != wantOwner {
				t.Fatalf("static version owner = %s, want %s", got, wantOwner)
			}
			if !canEditLiteralDeclaration(parsed, "Oracle.A") {
				t.Fatal("static evaluation should decide this VersionOverride edit")
			}
			if version, output, err := oracle.restoredVersion(t, repo, project, "Oracle.A"); err != nil || version != "2.0.0" {
				t.Fatalf("restore before the edit resolved %q: %v\n%s", version, err, output)
			}
			cliRepo := oracle.workspace(t, test.files)
			before := snapshotFiles(t, repo)
			withoutDotnet(t, func() {
				plan, _, _, err := planPackageMutation("update", "Oracle.A", "3.0.0", []mutationTarget{{project: parsed}}, mutationOptions{staticOwnership: true})
				if err != nil {
					t.Fatalf("TUI plan without the SDK: %v", err)
				}
				if _, err := plan.ApplyContext(context.Background()); err != nil {
					t.Fatal(err)
				}
			})
			after := snapshotFiles(t, repo)
			if code, output := gugetIn(t, cliRepo, "update", "Oracle.A", "--version", "3.0.0", "--file", filepath.Join(cliRepo, "App", "App.csproj")); code != ExitSuccess {
				t.Fatalf("CLI update with MSBuild evaluation: %d\n%s", code, output)
			}
			if cliAfter := snapshotFiles(t, cliRepo); !maps.Equal(cliAfter, after) {
				t.Fatalf("CLI edit %v differs from the static edit %v", cliAfter, after)
			}
			for name, content := range before {
				owned := name == test.owner
				if changed := after[name] != content; changed != owned {
					t.Errorf("%s changed=%v, want %v:\n%s", name, changed, owned, after[name])
				}
			}
			// Only the override value changes, in the form it was written.
			want := strings.NewReplacer(`VersionOverride="2.0.0"`, `VersionOverride="3.0.0"`, `<VersionOverride>2.0.0<`, `<VersionOverride>3.0.0<`).Replace(before[test.owner])
			if after[test.owner] != want {
				t.Fatalf("owner edit:\n%s\nwant\n%s", after[test.owner], want)
			}
			requireSameView(t, oracle, project)
			if version, output, err := oracle.restoredVersion(t, repo, project, "Oracle.A"); err != nil || version != "3.0.0" {
				t.Fatalf("restore after guget resolved %q: %v\n%s", version, err, output)
			}
			if !test.compareDotnet {
				return
			}
			dotnetRepo := oracle.workspace(t, test.files)
			dotnetProject := filepath.Join(dotnetRepo, "App", "App.csproj")
			if output, err := oracle.run(t, dotnetRepo, "add", dotnetProject, "package", "Oracle.A", "--version", "3.0.0"); err != nil {
				t.Fatalf("dotnet add: %v\n%s", err, output)
			}
			if got := oracle.definingFile(t, dotnetProject, "Oracle.A"); outputPathIdentity(got) != outputPathIdentity(filepath.Join(dotnetRepo, filepath.FromSlash(test.owner))) {
				t.Fatalf("dotnet moved the declaration to %s", got)
			}
			dotnetView, gugetView := oracle.evaluate(t, dotnetProject), oracle.evaluate(t, project)
			if dotnetView.CPM != gugetView.CPM || !maps.Equal(dotnetView.Versions, gugetView.Versions) {
				t.Fatalf("after guget %+v\nafter dotnet %+v", gugetView, dotnetView)
			}
			if version, output, err := oracle.restoredVersion(t, dotnetRepo, dotnetProject, "Oracle.A"); err != nil || version != "3.0.0" {
				t.Fatalf("restore after dotnet resolved %q: %v\n%s", version, err, output)
			}
		})
	}

	t.Run("overrides disabled are refused like NU1013", func(t *testing.T) {
		repo := oracle.workspace(t, map[string]string{
			"Directory.Packages.props": cpm(`<PropertyGroup><CentralPackageVersionOverrideEnabled>false</CentralPackageVersionOverrideEnabled></PropertyGroup>`),
			"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" VersionOverride="2.0.0" />`),
		})
		project := filepath.Join(repo, "App", "App.csproj")
		parsed, err := ParseCsproj(project)
		if err != nil {
			t.Fatal(err)
		}
		if problem := parsed.Declarations["oracle.a"].Problem; !strings.HasPrefix(problem, "NU1013") {
			t.Fatalf("problem = %q, want NU1013", problem)
		}
		if _, output, err := oracle.restoredVersion(t, repo, project, "Oracle.A"); err == nil || !strings.Contains(output, "NU1013") {
			t.Fatalf("dotnet restore should fail with NU1013: %v\n%s", err, output)
		}
		before := snapshotFiles(t, repo)
		if output, err := oracle.run(t, repo, "add", project, "package", "Oracle.A", "--version", "3.0.0"); err == nil {
			t.Fatalf("dotnet add accepted a disabled VersionOverride:\n%s", output)
		}
		if !maps.Equal(snapshotFiles(t, repo), before) {
			t.Fatal("dotnet add changed files while refusing")
		}
		withoutDotnet(t, func() {
			if code, output := gugetIn(t, repo, "update", "Oracle.A", "--version", "3.0.0", "--file", project); code != ExitRefused || !strings.Contains(output, "NU1013") {
				t.Fatalf("guget update: %d\n%s", code, output)
			}
		})
		if !maps.Equal(snapshotFiles(t, repo), before) {
			t.Fatal("refused update changed files")
		}
	})

	t.Run("an undecidable override switch keeps MSBuild evaluation", func(t *testing.T) {
		repo := oracle.workspace(t, map[string]string{
			"Directory.Packages.props": cpm(`<PropertyGroup><CentralPackageVersionOverrideEnabled>$(OverridesFromSdk)</CentralPackageVersionOverrideEnabled></PropertyGroup>`),
			"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" VersionOverride="2.0.0" />`),
		})
		parsed, err := ParseCsproj(filepath.Join(repo, "App", "App.csproj"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(parsed.StaticGaps, "\n"), "CentralPackageVersionOverrideEnabled") || canEditLiteralDeclaration(parsed, "Oracle.A") {
			t.Fatalf("gaps=%v; static evaluation must not decide this edit", parsed.StaticGaps)
		}
	})

	t.Run("conditional VersionOverride keeps MSBuild evaluation", func(t *testing.T) {
		files := map[string]string{
			"Directory.Packages.props": cpm(""),
			"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" VersionOverride="2.0.0" Condition="'$(TargetFramework)' == 'net10.0'" />`),
		}
		repo := oracle.workspace(t, files)
		project := filepath.Join(repo, "App", "App.csproj")
		parsed := requireSameView(t, oracle, project)
		if canEditLiteralDeclaration(parsed, "Oracle.A") {
			t.Fatal("a conditional VersionOverride bypassed MSBuild evaluation")
		}
		before := snapshotFiles(t, repo)
		withoutDotnet(t, func() {
			if code, output := gugetIn(t, repo, "update", "Oracle.A", "--version", "3.0.0", "--file", project); code == ExitSuccess || !strings.Contains(output, "MSBuild evaluation") {
				t.Fatalf("guget update without the SDK must need MSBuild evaluation: %d\n%s", code, output)
			}
		})
		if !maps.Equal(snapshotFiles(t, repo), before) {
			t.Fatal("update without evaluation changed files")
		}
		// With the SDK, MSBuild evaluation reports the ownership as conditional,
		// and guget refuses without writing.
		code, output := gugetIn(t, repo, "update", "Oracle.A", "--version", "3.0.0", "--file", project)
		if code != ExitRefused || !strings.Contains(output, "conditional ownership") {
			t.Fatalf("guget update with MSBuild evaluation: %d\n%s", code, output)
		}
		if !maps.Equal(snapshotFiles(t, repo), before) {
			t.Fatal("refused update changed files")
		}
	})
}
