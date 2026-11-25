//go:build integration

package main

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// packageOracle runs dotnet in an isolated home against a local folder feed
// built with dotnet pack, so the oracle never uses the network or the
// developer's NuGet configuration and caches.
type packageOracle struct {
	base string
	feed string
}

func newPackageOracle(t *testing.T) *packageOracle {
	t.Helper()
	requireIsolatedDotnet(t)
	base := t.TempDir()
	oracle := &packageOracle{base: base, feed: filepath.Join(base, "feed")}
	env := isolatedNuGetEnv(base)
	env["DOTNET_NOLOGO"] = "1"
	env["DOTNET_CLI_TELEMETRY_OPTOUT"] = "1"
	env["DOTNET_SKIP_FIRST_TIME_EXPERIENCE"] = "1"
	env["DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE"] = "1"
	for name, value := range env {
		t.Setenv(name, value)
	}
	for _, name := range []string{"ContinuousIntegrationBuild", "RestoreLockedMode", "ManagePackageVersionsCentrally", "DirectoryPackagesPropsPath"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	pack := filepath.Join(base, "pack")
	mustWriteFile(t, filepath.Join(pack, "nuget.config"), `<configuration><packageSources><clear /></packageSources></configuration>`)
	mustWriteFile(t, filepath.Join(pack, "readme.txt"), "oracle")
	mustWriteFile(t, filepath.Join(pack, "Pack.csproj"), `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework>`+
		`<EnableTargetingPackDownload>false</EnableTargetingPackDownload><IncludeBuildOutput>false</IncludeBuildOutput><NoWarn>$(NoWarn);NU5128</NoWarn>`+
		`<PackageId>Oracle.A</PackageId><Version>1.0.0</Version><Authors>guget</Authors><Description>oracle</Description></PropertyGroup>`+
		`<ItemGroup><None Include="readme.txt" Pack="true" PackagePath="docs/" /></ItemGroup></Project>`)
	oracle.mustRun(t, pack, "restore", "Pack.csproj")
	for _, spec := range []string{"Oracle.A/1.0.0", "Oracle.A/2.0.0", "Oracle.A/3.0.0", "Oracle.B/1.0.0", "Oracle.B/2.0.0", "Oracle.B/3.0.0"} {
		id, version, _ := strings.Cut(spec, "/")
		oracle.mustRun(t, pack, "pack", "Pack.csproj", "--no-build", "--no-restore", "-o", oracle.feed, "-p:PackageId="+id, "-p:Version="+version)
	}
	return oracle
}

func (o *packageOracle) run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "dotnet", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("dotnet %s timed out:\n%s", strings.Join(args, " "), output)
	}
	return string(output), err
}

func (o *packageOracle) mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := o.run(t, dir, args...)
	if err != nil {
		t.Fatalf("dotnet %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

// workspace writes files under a new repository whose nuget.config uses only
// the local feed. {{TFM}} expands to the properties every project needs.
func (o *packageOracle) workspace(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	mustWriteFile(t, filepath.Join(repo, "nuget.config"), `<configuration><packageSources><clear /><add key="feed" value="`+o.feed+`" /></packageSources></configuration>`)
	for name, content := range files {
		content = strings.ReplaceAll(content, "{{TFM}}", `<PropertyGroup><TargetFramework>net10.0</TargetFramework><EnableTargetingPackDownload>false</EnableTargetingPackDownload></PropertyGroup>`)
		mustWriteFile(t, filepath.Join(repo, filepath.FromSlash(name)), content)
	}
	return repo
}

// msbuildView is what restore sees for a project: whether CPM is on, the
// central file, and each direct package's requested version, computed from
// dotnet msbuild items with NuGet's rules (first item wins; VersionOverride,
// then Version, then the central version).
type msbuildView struct {
	CPM      bool
	Central  string
	Versions map[string]string
}

func (o *packageOracle) evaluate(t *testing.T, project string) msbuildView {
	t.Helper()
	output := o.mustRun(t, filepath.Dir(project), "msbuild", project,
		"-getProperty:_CentralPackageVersionsEnabled,DirectoryPackagesPropsPath",
		"-getItem:PackageReference,PackageVersion")
	var result struct {
		Properties map[string]string
		Items      map[string][]map[string]string
	}
	if err := decodeOracleJSON(output, &result); err != nil {
		t.Fatalf("decode msbuild output: %v\n%s", err, output)
	}
	view := msbuildView{CPM: strings.EqualFold(result.Properties["_CentralPackageVersionsEnabled"], "true"), Versions: map[string]string{}}
	if view.CPM {
		// MSBuild keeps the separators of the property text, such as
		// repo\eng/Versions.props on Windows, and expands a short (8.3)
		// directory name such as RUNNER~1 that guget keeps as given.
		view.Central = outputPathIdentity(filepath.Clean(result.Properties["DirectoryPackagesPropsPath"]))
	}
	central := map[string]string{}
	for _, item := range result.Items["PackageVersion"] {
		if _, seen := central[strings.ToLower(item["Identity"])]; !seen {
			central[strings.ToLower(item["Identity"])] = item["Version"]
		}
	}
	for _, item := range result.Items["PackageReference"] {
		id := strings.ToLower(item["Identity"])
		if _, seen := view.Versions[id]; seen || strings.EqualFold(item["IsImplicitlyDefined"], "true") {
			continue
		}
		version := item["VersionOverride"]
		if version == "" {
			version = item["Version"]
		}
		if version == "" && view.CPM {
			version = central[id]
		}
		view.Versions[id] = version
	}
	return view
}

func decodeOracleJSON(output string, target any) error {
	if start := strings.Index(output, "{"); start >= 0 {
		output = output[start:]
	}
	return json.Unmarshal([]byte(output), target)
}

// gugetView is the same view from guget's static evaluation.
func gugetView(t *testing.T, project string) (msbuildView, *ParsedProject) {
	t.Helper()
	parsed, err := ParseCsproj(project)
	if err != nil {
		t.Fatal(err)
	}
	view := msbuildView{Central: projectCPMTarget(parsed), Versions: map[string]string{}}
	view.CPM = view.Central != ""
	if view.CPM {
		view.Central = outputPathIdentity(view.Central)
	}
	for ref := range parsed.Packages {
		view.Versions[strings.ToLower(ref.Name)] = ref.Requested
	}
	return view, parsed
}

func requireSameView(t *testing.T, oracle *packageOracle, project string) *ParsedProject {
	t.Helper()
	want := oracle.evaluate(t, project)
	got, parsed := gugetView(t, project)
	if got.CPM != want.CPM || got.Central != want.Central || !maps.Equal(got.Versions, want.Versions) {
		t.Fatalf("guget view %+v\nMSBuild view %+v", got, want)
	}
	return parsed
}

const oracleCPM = `<PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup>`

func oracleSDKProject(items string) string {
	return `<Project Sdk="Microsoft.NET.Sdk">{{TFM}}<ItemGroup>` + items + `</ItemGroup></Project>`
}

// gugetIn runs a guget command in repo and returns its exit code and output.
func gugetIn(t *testing.T, repo string, args ...string) (ExitCode, string) {
	t.Helper()
	code, stdout, stderr := runCLIForTest(t, append(args, "--project", repo)...)
	return code, stdout + stderr
}

// TestPackageDeclarationsMatchDotnet compares guget's static view, edits and
// restores with dotnet msbuild, dotnet add/remove package and dotnet restore.
func TestPackageDeclarationsMatchDotnet(t *testing.T) {
	oracle := newPackageOracle(t)

	nested := func(importExpression string) map[string]string {
		return map[string]string{
			"Directory.Packages.props":     `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /><PackageVersion Include="Oracle.B" Version="1.0.0" /></ItemGroup></Project>`,
			"src/Directory.Packages.props": `<Project><Import Project="` + importExpression + `" /><ItemGroup><PackageVersion Update="Oracle.B" Version="2.0.0" /></ItemGroup></Project>`,
			"src/App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" /><PackageReference Include="Oracle.B" />`),
		}
	}

	t.Run("static view", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			files   map[string]string
			project string
			owners  map[string]string // package → version owner relative to the repository
			problem map[string]string // package → NuGet code
		}{
			{name: "nested central file, unquoted GetPathOfFileAbove", project: "src/App/App.csproj",
				files:  nested(`$([MSBuild]::GetPathOfFileAbove(Directory.Packages.props, $(MSBuildThisFileDirectory)..))`),
				owners: map[string]string{"Oracle.A": "Directory.Packages.props", "Oracle.B": "src/Directory.Packages.props"}},
			{name: "nested central file, quoted GetPathOfFileAbove", project: "src/App/App.csproj",
				files:  nested(`$([MSBuild]::GetPathOfFileAbove('Directory.Packages.props', '$(MSBuildThisFileDirectory)../'))`),
				owners: map[string]string{"Oracle.A": "Directory.Packages.props", "Oracle.B": "src/Directory.Packages.props"}},
			{name: "one-argument GetPathOfFileAbove starts beside the declaring file", project: "src/App/App.csproj", files: map[string]string{
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"src/Versions.props":       `<Project><PropertyGroup><OracleAVersion>3.0.0</OracleAVersion></PropertyGroup></Project>`,
				"src/App/App.csproj": `<Project Sdk="Microsoft.NET.Sdk">{{TFM}}<Import Project="$([MSBuild]::GetPathOfFileAbove('Versions.props'))" />` +
					`<ItemGroup><PackageReference Include="Oracle.A" VersionOverride="$(OracleAVersion)" /></ItemGroup></Project>`,
			}},
			{name: "duplicate PackageVersion keeps the first", project: "App/App.csproj", files: map[string]string{
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /><PackageVersion Include="Oracle.A" Version="2.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" />`),
			}, owners: map[string]string{"Oracle.A": "Directory.Packages.props"}},
			{name: "GlobalPackageReference", project: "App/App.csproj", files: map[string]string{
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><GlobalPackageReference Include="Oracle.B" Version="1.0.0" /><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"Directory.Build.targets":  `<Project><ItemGroup><GlobalPackageReference Include="Oracle.TooLate" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" />`),
			}, owners: map[string]string{"Oracle.B": "Directory.Packages.props"}},
			{name: "DirectoryPackagesPropsPath override", project: "App/App.csproj", files: map[string]string{
				"Directory.Build.props":    `<Project><PropertyGroup><DirectoryPackagesPropsPath>$(MSBuildThisFileDirectory)eng/Versions.props</DirectoryPackagesPropsPath></PropertyGroup></Project>`,
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="3.0.0" /></ItemGroup></Project>`,
				"eng/Versions.props":       `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" />`),
			}, owners: map[string]string{"Oracle.A": "eng/Versions.props"}},
			{name: "central file overrides Directory.Build.props switch", project: "App/App.csproj", files: map[string]string{
				"Directory.Build.props":    `<Project>` + oracleCPM + `</Project>`,
				"Directory.Packages.props": `<Project><PropertyGroup><ManagePackageVersionsCentrally>false</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" Version="2.0.0" />`),
			}},
			{name: "project switch overrides central file", project: "App/App.csproj", files: map[string]string{
				"Directory.Packages.props": `<Project><PropertyGroup><ManagePackageVersionsCentrally>false</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           `<Project Sdk="Microsoft.NET.Sdk">{{TFM}}` + oracleCPM + `<ItemGroup><PackageReference Include="Oracle.A" /></ItemGroup></Project>`,
			}},
			{name: "Directory.Build.targets switch is too late", project: "App/App.csproj", files: map[string]string{
				"Directory.Packages.props": `<Project><ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"Directory.Build.targets":  `<Project>` + oracleCPM + `</Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" Version="2.0.0" />`),
			}},
			{name: "ImportDirectoryPackagesProps false", project: "App/App.csproj", files: map[string]string{
				"Directory.Build.props":    `<Project><PropertyGroup><ImportDirectoryPackagesProps>false</ImportDirectoryPackagesProps></PropertyGroup></Project>`,
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" Version="2.0.0" />`),
			}},
			{name: "Version on a centrally managed reference", project: "App/App.csproj", files: map[string]string{
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" Version="2.0.0" />`),
			}, problem: map[string]string{"Oracle.A": "NU1008"}},
			{name: "Update without Include and Directory.Build.targets declarations", project: "App/App.csproj", files: map[string]string{
				"Directory.Build.props":   `<Project><ItemGroup><PackageReference Update="Oracle.Ghost" Version="1.0.0" /><PackageReference Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"Directory.Build.targets": `<Project><ItemGroup><PackageReference Include="Oracle.B" Version="1.0.0" /><PackageReference Update="Oracle.A" Version="2.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":          oracleSDKProject(`<PackageReference Include="Oracle.A" Version="3.0.0" />`),
			}, owners: map[string]string{"Oracle.A": "Directory.Build.targets", "Oracle.B": "Directory.Build.targets"}},
		} {
			t.Run(test.name, func(t *testing.T) {
				repo := oracle.workspace(t, test.files)
				project := filepath.Join(repo, filepath.FromSlash(test.project))
				parsed := requireSameView(t, oracle, project)
				for id, owner := range test.owners {
					if got := parsed.SourceFileForPackage(id); got != filepath.Join(repo, filepath.FromSlash(owner)) {
						t.Errorf("%s version owner = %s, want %s", id, got, owner)
					}
				}
				for id, code := range test.problem {
					if problem := parsed.Declarations[strings.ToLower(id)].Problem; !strings.HasPrefix(problem, code) {
						t.Errorf("%s problem = %q, want %s", id, problem, code)
					}
					if output, err := oracle.run(t, repo, "restore", project); err == nil || !strings.Contains(output, code) {
						t.Errorf("dotnet restore should fail with %s: %v\n%s", code, err, output)
					}
				}
			})
		}
	})

	t.Run("edits", func(t *testing.T) {
		// Each case applies the same change with dotnet and with guget to two
		// copies of a workspace, then compares what MSBuild evaluates.

		dotnetPackage := func(t *testing.T, repo, verb, project string, args ...string) (string, error) {
			t.Helper()
			return oracle.run(t, repo, append([]string{verb, project, "package"}, args...)...)
		}
		sameResult := func(t *testing.T, dotnetProject, gugetProject string) {
			t.Helper()
			want := oracle.evaluate(t, dotnetProject)
			requireSameView(t, oracle, gugetProject)
			got := oracle.evaluate(t, gugetProject)
			if got.CPM != want.CPM || !maps.Equal(got.Versions, want.Versions) {
				t.Fatalf("after guget %+v\nafter dotnet %+v", got, want)
			}
		}

		t.Run("add without central package management", func(t *testing.T) {
			files := map[string]string{"App/App.csproj": oracleSDKProject(``)}
			dotnetRepo, gugetRepo := oracle.workspace(t, files), oracle.workspace(t, files)
			dotnetProject, gugetProject := filepath.Join(dotnetRepo, "App", "App.csproj"), filepath.Join(gugetRepo, "App", "App.csproj")
			if output, err := dotnetPackage(t, dotnetRepo, "add", dotnetProject, "Oracle.A", "--version", "1.0.0"); err != nil {
				t.Fatalf("dotnet add: %v\n%s", err, output)
			}
			if code, output := gugetIn(t, gugetRepo, "add", "Oracle.A", "--version", "1.0.0", "--file", gugetProject); code != ExitSuccess {
				t.Fatalf("guget add: %d\n%s", code, output)
			}
			sameResult(t, dotnetProject, gugetProject)
		})

		t.Run("add a new central package in a nested tree", func(t *testing.T) {
			files := nested(`$([MSBuild]::GetPathOfFileAbove(Directory.Packages.props, $(MSBuildThisFileDirectory)..))`)
			files["src/App/App.csproj"] = oracleSDKProject(`<PackageReference Include="Oracle.A" />`)
			files["Directory.Packages.props"] = `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`
			files["src/Directory.Packages.props"] = `<Project><Import Project="$([MSBuild]::GetPathOfFileAbove(Directory.Packages.props, $(MSBuildThisFileDirectory)..))" /></Project>`
			dotnetRepo, gugetRepo := oracle.workspace(t, files), oracle.workspace(t, files)
			dotnetProject, gugetProject := filepath.Join(dotnetRepo, "src", "App", "App.csproj"), filepath.Join(gugetRepo, "src", "App", "App.csproj")
			if output, err := dotnetPackage(t, dotnetRepo, "add", dotnetProject, "Oracle.B", "--version", "2.0.0"); err != nil {
				t.Fatalf("dotnet add: %v\n%s", err, output)
			}
			if code, output := gugetIn(t, gugetRepo, "add", "Oracle.B", "--version", "2.0.0", "--file", gugetProject); code != ExitSuccess {
				t.Fatalf("guget add: %d\n%s", code, output)
			}
			sameResult(t, dotnetProject, gugetProject)
			for _, repo := range []string{dotnetRepo, gugetRepo} {
				if !strings.Contains(readFile(t, filepath.Join(repo, "src", "Directory.Packages.props")), "Oracle.B") {
					t.Fatalf("PackageVersion must go to DirectoryPackagesPropsPath in %s", repo)
				}
			}
		})

		t.Run("add a package whose central version is in an imported file", func(t *testing.T) {
			files := nested(`$([MSBuild]::GetPathOfFileAbove(Directory.Packages.props, $(MSBuildThisFileDirectory)..))`)
			files["src/App/App.csproj"] = oracleSDKProject(``)
			repo := oracle.workspace(t, files)
			project := filepath.Join(repo, "src", "App", "App.csproj")
			before := readFile(t, project)
			// dotnet refuses a PackageVersion outside DirectoryPackagesPropsPath
			// (NuGet MSBuildAPIUtility.cs:251-257).
			if output, err := dotnetPackage(t, repo, "add", project, "Oracle.A", "--version", "1.0.0"); err == nil {
				t.Fatalf("dotnet add accepted a central version in an imported file:\n%s", output)
			}
			mustWriteFile(t, project, before)
			code, output := gugetIn(t, repo, "add", "Oracle.A", "--version", "2.0.0", "--file", project)
			if code != ExitRefused || !strings.Contains(output, "guget update Oracle.A --version 2.0.0") {
				t.Fatalf("guget add with a different version: %d\n%s", code, output)
			}
			// Guget adds only the reference when the shared version already matches.
			if code, output := gugetIn(t, repo, "add", "Oracle.A", "--version", "1.0.0", "--file", project); code != ExitSuccess {
				t.Fatalf("guget add with the shared version: %d\n%s", code, output)
			}
			if view := oracle.evaluate(t, project); view.Versions["oracle.a"] != "1.0.0" {
				t.Fatalf("view = %+v", view)
			}
			requireSameView(t, oracle, project)
			if strings.Count(readFile(t, filepath.Join(repo, "src", "Directory.Packages.props"))+readFile(t, filepath.Join(repo, "Directory.Packages.props")), `"Oracle.A"`) != 1 {
				t.Fatal("guget wrote a second PackageVersion for Oracle.A")
			}
		})

		t.Run("remove a centrally managed reference", func(t *testing.T) {
			files := map[string]string{
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /><PackageVersion Include="Oracle.B" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" /><PackageReference Include="Oracle.B" />`),
			}
			dotnetRepo, gugetRepo := oracle.workspace(t, files), oracle.workspace(t, files)
			dotnetProject, gugetProject := filepath.Join(dotnetRepo, "App", "App.csproj"), filepath.Join(gugetRepo, "App", "App.csproj")
			if output, err := dotnetPackage(t, dotnetRepo, "remove", dotnetProject, "Oracle.A"); err != nil {
				t.Fatalf("dotnet remove: %v\n%s", err, output)
			}
			if code, output := gugetIn(t, gugetRepo, "remove", "Oracle.A", "--file", gugetProject); code != ExitSuccess {
				t.Fatalf("guget remove: %d\n%s", code, output)
			}
			sameResult(t, dotnetProject, gugetProject)
			if readFile(t, filepath.Join(dotnetRepo, "Directory.Packages.props")) != readFile(t, filepath.Join(gugetRepo, "Directory.Packages.props")) {
				t.Fatal("both tools must keep the central PackageVersion")
			}
		})

		t.Run("update a nested PackageVersion Update", func(t *testing.T) {
			repo := oracle.workspace(t, nested(`$([MSBuild]::GetPathOfFileAbove('Directory.Packages.props', '$(MSBuildThisFileDirectory)../'))`))
			project := filepath.Join(repo, "src", "App", "App.csproj")
			parent := readFile(t, filepath.Join(repo, "Directory.Packages.props"))
			if code, output := gugetIn(t, repo, "update", "Oracle.B", "--version", "3.0.0", "--file", project); code != ExitSuccess {
				t.Fatalf("guget update: %d\n%s", code, output)
			}
			if view := oracle.evaluate(t, project); view.Versions["oracle.b"] != "3.0.0" || view.Versions["oracle.a"] != "1.0.0" {
				t.Fatalf("view = %+v", view)
			}
			if readFile(t, filepath.Join(repo, "Directory.Packages.props")) != parent {
				t.Fatal("the parent central file must not change")
			}
		})

		t.Run("update a GlobalPackageReference", func(t *testing.T) {
			repo := oracle.workspace(t, map[string]string{
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><GlobalPackageReference Include="Oracle.B" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(``),
			})
			project := filepath.Join(repo, "App", "App.csproj")
			if code, output := gugetIn(t, repo, "update", "Oracle.B", "--version", "2.0.0", "--file", project); code != ExitSuccess {
				t.Fatalf("guget update: %d\n%s", code, output)
			}
			if view := oracle.evaluate(t, project); view.Versions["oracle.b"] != "2.0.0" {
				t.Fatalf("view = %+v", view)
			}
			requireSameView(t, oracle, project)
			if code, output := gugetIn(t, repo, "remove", "Oracle.B", "--file", project); code != ExitRefused || !strings.Contains(output, "GlobalPackageReference") {
				t.Fatalf("project removal of a global package: %d\n%s", code, output)
			}
		})

		t.Run("Version on a centrally managed reference is refused", func(t *testing.T) {
			repo := oracle.workspace(t, map[string]string{
				"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
				"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" Version="2.0.0" />`),
			})
			project := filepath.Join(repo, "App", "App.csproj")
			if output, err := dotnetPackage(t, repo, "add", project, "Oracle.B", "--version", "1.0.0"); err == nil {
				t.Fatalf("dotnet add accepted a project with NU1008:\n%s", output)
			}
			if code, output := gugetIn(t, repo, "update", "Oracle.A", "--version", "3.0.0", "--file", project); code != ExitRefused || !strings.Contains(output, "NU1008") {
				t.Fatalf("guget update: %d\n%s", code, output)
			}
		})
	})

	t.Run("restore", func(t *testing.T) {
		lockFileVersion := func(t *testing.T, lockFile, id string) string {
			t.Helper()
			var lock struct {
				Dependencies map[string]map[string]struct {
					Resolved string `json:"resolved"`
				} `json:"dependencies"`
			}
			if err := json.Unmarshal([]byte(readFile(t, lockFile)), &lock); err != nil {
				t.Fatal(err)
			}
			for _, dependencies := range lock.Dependencies {
				for name, dependency := range dependencies {
					if strings.EqualFold(name, id) {
						return dependency.Resolved
					}
				}
			}
			return ""
		}
		files := map[string]string{
			"Directory.Build.props":    `<Project><PropertyGroup><RestorePackagesWithLockFile>true</RestorePackagesWithLockFile><RestoreLockedMode>true</RestoreLockedMode></PropertyGroup></Project>`,
			"Directory.Packages.props": `<Project>` + oracleCPM + `<ItemGroup><PackageVersion Include="Oracle.A" Version="1.0.0" /></ItemGroup></Project>`,
			"App/App.csproj":           oracleSDKProject(`<PackageReference Include="Oracle.A" />`),
			"Other/Other.csproj":       oracleSDKProject(`<PackageReference Include="Oracle.A" />`),
			"Host/Host.csproj":         oracleSDKProject(`<ProjectReference Include="../Other/Other.csproj" />`),
		}
		names := []string{"App", "Other", "Host"}
		prepare := func(t *testing.T) string {
			t.Helper()
			repo := oracle.workspace(t, files)
			for _, name := range names {
				oracle.mustRun(t, repo, "restore", filepath.Join(repo, name, name+".csproj"))
			}
			return repo
		}

		t.Run("plain restore fails in locked mode after an edit", func(t *testing.T) {
			repo := prepare(t)
			central := filepath.Join(repo, "Directory.Packages.props")
			mustWriteFile(t, central, strings.Replace(readFile(t, central), `Version="1.0.0"`, `Version="2.0.0"`, 1))
			if output, err := oracle.run(t, repo, "restore", filepath.Join(repo, "App", "App.csproj")); err == nil || !strings.Contains(output, "NU1004") {
				t.Fatalf("expected NU1004 from locked-mode restore: %v\n%s", err, output)
			}
		})

		t.Run("shared-file edit restores every consumer and regenerates lock files", func(t *testing.T) {
			repo := prepare(t)
			code, output := gugetIn(t, repo, "update", "Oracle.A", "--version", "2.0.0", "--file", filepath.Join(repo, "App", "App.csproj"), "--restore")
			if code != ExitSuccess {
				t.Fatalf("guget update --restore: %d\n%s", code, output)
			}
			for _, name := range names {
				if version := lockFileVersion(t, filepath.Join(repo, name, "packages.lock.json"), "Oracle.A"); version != "2.0.0" {
					t.Errorf("%s lock file resolves Oracle.A %q, want 2.0.0\n%s", name, version, output)
				}
				// The regenerated lock files satisfy locked mode again.
				oracle.mustRun(t, repo, "restore", filepath.Join(repo, name, name+".csproj"))
			}
		})
	})
}
