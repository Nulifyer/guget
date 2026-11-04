package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
)

func TestReadySDKQuickUpdateNeedsNoSDKProcess(t *testing.T) {
	for _, extension := range []string{".csproj", ".fsproj", ".vbproj"} {
		for _, state := range []string{"fresh", "restored"} {
			t.Run(extension+"/"+state, func(t *testing.T) {
				m, _, path := newActionTestApp(t, "")
				if extension != ".csproj" {
					renamed := strings.TrimSuffix(path, ".csproj") + extension
					if err := os.Rename(path, renamed); err != nil {
						t.Fatal(err)
					}
					path = renamed
				}
				root := filepath.Dir(path)
				mustWriteFile(t, filepath.Join(root, "Directory.Build.props"), `<Project><Import Project="Shared.props"/></Project>`)
				mustWriteFile(t, filepath.Join(root, "Shared.props"), `<Project><ItemGroup><PackageReference Include="Other" Version="1.0.0"/></ItemGroup></Project>`)
				if state == "restored" {
					writeRestoredPackageImports(t, path)
				}
				project, err := ParseCsproj(path)
				if err != nil {
					t.Fatal(err)
				}
				if !project.RequiresEvaluation {
					t.Fatal("fixture must exercise the SDK/import evaluation policy")
				}
				snapshot, err := scanWorkspace(root)
				if err != nil {
					t.Fatal(err)
				}
				m.applyWorkspaceSnapshot(snapshot)
				m.selectProjectByPath(path)
				m.rebuildPackageRows()
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				// No dotnet executable is available. A regression to SDK evaluation
				// must fail this actual key -> plan -> apply -> reconcile action.
				t.Setenv("PATH", t.TempDir())
				cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'}))
				if cmd == nil {
					t.Fatal("ready update did not start")
				}
				msg := cmd()
				reconciled, ok := msg.(mutationReconciledMsg)
				if !ok || reconciled.err != nil || reconciled.written != 1 {
					t.Fatalf("update did not reconcile one file: %+v", msg)
				}
				m.HandleEvent(msg)
				after, err := os.ReadFile(path)
				want := strings.Replace(string(before), `Include="A" Version="1.0.0"`, `Include="A" Version="2.0.0"`, 1)
				if err != nil || string(after) != want {
					t.Fatalf("unexpected source edit: %s, %v", after, err)
				}
				found := false
				for _, row := range m.packages.rows {
					if row.ref.Name == "A" {
						found = row.ref.Requested == "2.0.0"
					}
				}
				if !found || m.ctx.Saving || m.ctx.Reloading {
					t.Fatal("committed requested version did not reach the idle row")
				}
				if _, _, err := buildMutationPlan("update", "A", "3.0.0", m.ctx.ParsedProjects); err == nil || !strings.Contains(err.Error(), "requires MSBuild evaluation") {
					t.Fatalf("CLI evaluation policy changed: %v", err)
				}
			})
		}
	}
}

func TestLiteralUpdateRetainsEvaluationForUncertainOwnership(t *testing.T) {
	const reference = `<ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup>`
	for _, test := range []struct {
		name, body, props, targets string
	}{
		{"conditional reference", `<ItemGroup><PackageReference Include="A" Version="1.0.0" Condition="false"/></ItemGroup>`, "", ""},
		{"conditional metadata", `<ItemGroup><PackageReference Include="A"><Version Condition="false">1.0.0</Version></PackageReference></ItemGroup>`, "", ""},
		{"implicit metadata", `<ItemGroup><PackageReference Include="A" Version="1.0.0"><IsImplicitlyDefined>true</IsImplicitlyDefined></PackageReference></ItemGroup>`, "", ""},
		{"duplicate reference", reference + reference, "", ""},
		{"override attribute", `<ItemGroup><PackageReference Include="A" VersionOverride="1.0.0"/></ItemGroup>`, "", ""},
		{"override metadata", `<ItemGroup><PackageReference Include="A"><VersionOverride>1.0.0</VersionOverride></PackageReference></ItemGroup>`, "", ""},
		{"property version", `<PropertyGroup><V>1.0.0</V></PropertyGroup><ItemGroup><PackageReference Include="A" Version="$(V)"/></ItemGroup>`, "", ""},
		{"conditional import", reference + `<Import Project="Shared.props" Condition="false"/>`, reference, ""},
		{"competing imported owner", reference + `<Import Project="Shared.props"/>`, reference, ""},
		{"unresolved import", reference + `<Import Project="$(Unknown)/Shared.props"/>`, "", ""},
		{"choose", reference + `<Choose><When Condition="true"><ItemGroup><PackageReference Update="A" Version="9.0.0"/></ItemGroup></When></Choose>`, "", ""},
		{"remove", reference + `<ItemGroup><PackageReference Remove="A"/></ItemGroup>`, "", ""},
		{"update", reference + `<ItemGroup><PackageReference Update="A" Version="9.0.0"/></ItemGroup>`, "", ""},
		{"directory targets", reference, "", `<Project><ItemGroup><PackageReference Update="A" Version="9.0.0"/></ItemGroup></Project>`},
		{"custom targets path", `<PropertyGroup><DirectoryBuildTargetsPath>Custom.targets</DirectoryBuildTargetsPath></PropertyGroup>` + reference, "", ""},
		{"custom restore output", `<PropertyGroup><BaseIntermediateOutputPath>custom-obj/</BaseIntermediateOutputPath></PropertyGroup>` + reference, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "App.csproj")
			before := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>` + test.body + `</Project>`
			mustWriteFile(t, path, before)
			if test.props != "" {
				mustWriteFile(t, filepath.Join(root, "Shared.props"), `<Project>`+test.props+`</Project>`)
			}
			if test.targets != "" {
				mustWriteFile(t, filepath.Join(root, "Directory.Build.targets"), test.targets)
			}
			project, err := ParseCsproj(path)
			if err != nil {
				t.Fatal(err)
			}
			if canEditLiteralDeclaration(project, "A") {
				t.Fatal("uncertain ownership bypassed evaluation")
			}
			t.Setenv("PATH", t.TempDir())
			_, _, _, err = planPackageMutation("update", "A", "2.0.0", []mutationTarget{{project: project}}, mutationOptions{allowDeclaredEdit: true})
			if err == nil || !strings.Contains(err.Error(), "requires MSBuild evaluation") {
				t.Fatalf("did not retain evaluation: %v", err)
			}
			after, _ := os.ReadFile(path)
			if string(after) != before {
				t.Fatal("refused update changed source")
			}
		})
	}
}

// Match the generated import that previously forced an unrelated ProjectA
// update through MSBuild after restore. These are build inputs, not declaration
// owners for the TUI's literal source edit.
func writeRestoredPackageImports(t *testing.T, projectPath string) {
	t.Helper()
	generated := filepath.Join(filepath.Dir(projectPath), "obj", filepath.Base(projectPath)+".nuget.g")
	if err := os.MkdirAll(filepath.Dir(generated), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, generated+".props", `<Project xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <PropertyGroup Condition=" '$(ExcludeRestorePackageImports)' != 'true' ">
    <RestoreSuccess>True</RestoreSuccess>
    <NuGetPackageRoot>$(UserProfile)/.nuget/packages/</NuGetPackageRoot>
  </PropertyGroup>
</Project>`)
	mustWriteFile(t, generated+".targets", `<Project xmlns="http://schemas.microsoft.com/developer/msbuild/2003">
  <ImportGroup Condition=" '$(ExcludeRestorePackageImports)' != 'true' ">
    <Import Project="$(NuGetPackageRoot)microsoft.extensions.logging.abstractions/9.0.0/buildTransitive/net8.0/Microsoft.Extensions.Logging.Abstractions.targets" Condition="Exists('$(NuGetPackageRoot)microsoft.extensions.logging.abstractions/9.0.0/buildTransitive/net8.0/Microsoft.Extensions.Logging.Abstractions.targets')" />
  </ImportGroup>
</Project>`)
}

func TestReadySharedLiteralUpdateNeedsNoSDKProcess(t *testing.T) {
	for _, kind := range []string{"imported", "central"} {
		for _, all := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/all=%v", kind, all), func(t *testing.T) {
				m, _, _ := newActionTestApp(t, "")
				root := t.TempDir()
				owner := filepath.Join(root, "Shared.props")
				body := `<Import Project="Shared.props"/>`
				declaration := `<Project><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`
				if kind == "central" {
					owner = filepath.Join(root, "Directory.Packages.props")
					body = `<ItemGroup><PackageReference Include="A"/></ItemGroup>`
					declaration = `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="A" Version="1.0.0"/></ItemGroup></Project>`
				}
				mustWriteFile(t, owner, declaration)
				for _, name := range []string{"One", "Two"} {
					path := filepath.Join(root, name+".csproj")
					mustWriteFile(t, path, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>`+body+`</Project>`)
					writeRestoredPackageImports(t, path)
				}
				snapshot, err := scanWorkspace(root)
				if err != nil {
					t.Fatal(err)
				}
				m.applyWorkspaceSnapshot(snapshot)
				m.selectProjectByPath(filepath.Join(root, "One.csproj"))
				m.rebuildPackageRows()
				t.Setenv("PATH", t.TempDir())
				key := 'u'
				if all {
					key = 'U'
				}
				cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: key}))
				if cmd == nil {
					t.Fatal("update did not start")
				}
				msg := cmd()
				result, ok := msg.(mutationReconciledMsg)
				if !ok || result.err != nil || result.written != 1 {
					t.Fatalf("update failed: %+v", msg)
				}
				m.HandleEvent(msg)
				data, err := os.ReadFile(owner)
				if err != nil || string(data) != strings.Replace(declaration, `Version="1.0.0"`, `Version="2.0.0"`, 1) {
					t.Fatalf("owner: %s, %v", data, err)
				}
				for _, project := range m.ctx.ParsedProjects {
					for ref := range project.Packages {
						if ref.Name == "A" && ref.Requested != "2.0.0" {
							t.Fatalf("consumer stale: %s %+v", project.FilePath, ref)
						}
					}
				}
				cmd = m.HandleEvent(ui.KeyPress(ui.Key{Code: key}))
				if cmd != nil || !strings.Contains(m.ctx.StatusLine, "already at 2.0.0") {
					t.Fatalf("next update: %q", m.ctx.StatusLine)
				}
			})
		}
	}
}

func TestCentralLiteralUpdateRejectsAmbiguousDeclarations(t *testing.T) {
	for _, extra := range []string{
		`<ItemGroup><PackageVersion Include="A" Version="3.0.0"/></ItemGroup>`,
		`<ItemGroup><PackageReference Include="A"/></ItemGroup>`,
		`<ItemGroup Condition="false"><PackageVersion Include="A" Version="3.0.0"/></ItemGroup>`,
		`<Choose><When Condition="true"><ItemGroup><PackageVersion Update="A" Version="3.0.0"/></ItemGroup></When></Choose>`,
	} {
		root := t.TempDir()
		path := filepath.Join(root, "App.csproj")
		mustWriteFile(t, path, `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="A"/></ItemGroup></Project>`)
		mustWriteFile(t, filepath.Join(root, "Directory.Packages.props"), `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="A" Version="1.0.0"/></ItemGroup>`+extra+`</Project>`)
		project, err := ParseCsproj(path)
		if err != nil {
			t.Fatal(err)
		}
		if canEditLiteralDeclaration(project, "A") {
			t.Fatalf("ambiguous CPM accepted: %s", extra)
		}
	}
}
