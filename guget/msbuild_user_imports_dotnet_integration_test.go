//go:build integration

package main

import (
	"maps"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUnmodeledImportsMatchDotnet checks, against MSBuild, the imports outside
// the workspace that static evaluation cannot read: a
// CustomAfterMicrosoftCommonTargets file named by the environment and a file
// in the user's ImportAfter directory. MSBuild applies the Update they hold,
// so the static owner would edit a version restore does not use. The CLI
// evaluates with MSBuild and refuses; the TUI's static path yields to MSBuild
// evaluation too.
func TestUnmodeledImportsMatchDotnet(t *testing.T) {
	oracle := newPackageOracle(t)
	const update = `<Project><ItemGroup><PackageReference Update="Oracle.A" Version="2.0.0" /></ItemGroup></Project>`
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, outside string)
	}{
		{name: "CustomAfterMicrosoftCommonTargets", setup: func(t *testing.T, outside string) {
			path := filepath.Join(outside, "custom.targets")
			mustWriteFile(t, path, update)
			t.Setenv("CustomAfterMicrosoftCommonTargets", path)
		}},
		{name: "user ImportAfter", setup: func(t *testing.T, outside string) {
			mustWriteFile(t, filepath.Join(outside, "Microsoft", "MSBuild", "Current", "Microsoft.Common.targets", "ImportAfter", "custom.targets"), update)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			outside := t.TempDir()
			t.Setenv("XDG_DATA_HOME", outside) // .NET's LocalApplicationData on Linux
			if runtime.GOOS == "windows" {
				// Windows takes LocalApplicationData from the known-folder
				// API, which ignores LOCALAPPDATA; both MSBuild and guget
				// honor this override.
				t.Setenv("MSBuildUserExtensionsPath", filepath.Join(outside, "Microsoft", "MSBuild"))
			}
			repo := oracle.workspace(t, map[string]string{
				"App/App.csproj": oracleSDKProject(`<PackageReference Include="Oracle.A" Version="1.0.0" />`),
			})
			project := filepath.Join(repo, "App", "App.csproj")
			if view := oracle.evaluate(t, project); view.Versions["oracle.a"] != "1.0.0" {
				t.Fatalf("before the outside import MSBuild sees %+v", view)
			}
			test.setup(t, outside)
			if view := oracle.evaluate(t, project); view.Versions["oracle.a"] != "2.0.0" {
				t.Fatalf("MSBuild did not apply the outside import: %+v", view)
			}
			parsed, err := ParseCsproj(project)
			if err != nil {
				t.Fatal(err)
			}
			// Static evaluation cannot see the file, so on its own it would
			// take the literal 1.0.0 in App.csproj as the owner.
			if !canEditLiteralDeclaration(parsed, "Oracle.A") || unmodeledMSBuildImports() == "" {
				t.Fatalf("the fixture must hide the outside import from static evaluation (reason %q)", unmodeledMSBuildImports())
			}
			before := snapshotFiles(t, repo)

			code, output := gugetIn(t, repo, "update", "Oracle.A", "--version", "3.0.0", "--file", project)
			if code != ExitRefused || !strings.Contains(output, "evaluated version differs") {
				t.Fatalf("CLI update: %d\n%s", code, output)
			}
			if !maps.Equal(snapshotFiles(t, repo), before) {
				t.Fatal("the refused CLI update changed files")
			}

			// The TUI's option keeps MSBuild evaluation: without dotnet it
			// cannot plan, and with dotnet it refuses like the CLI.
			targets := []mutationTarget{{project: parsed}}
			withoutDotnet(t, func() {
				if _, _, _, err := planPackageMutation("update", "Oracle.A", "3.0.0", targets, mutationOptions{staticOwnership: true}); err == nil || !strings.Contains(err.Error(), "requires MSBuild evaluation") {
					t.Fatalf("TUI plan without dotnet: %v", err)
				}
			})
			if _, _, _, err := planPackageMutation("update", "Oracle.A", "3.0.0", targets, mutationOptions{staticOwnership: true}); err == nil || !strings.Contains(err.Error(), "evaluated version differs") {
				t.Fatalf("TUI plan with MSBuild evaluation: %v", err)
			}
			if !maps.Equal(snapshotFiles(t, repo), before) {
				t.Fatal("the refused TUI plan changed files")
			}
			if view := oracle.evaluate(t, project); view.Versions["oracle.a"] != "2.0.0" {
				t.Fatalf("MSBuild view changed: %+v", view)
			}
		})
	}
}
