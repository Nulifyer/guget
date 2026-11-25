package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
)

func TestUnmodeledImportsFromTheEnvironment(t *testing.T) {
	for _, test := range []struct {
		name    string
		environ []string
		want    string
	}{
		{name: "common targets", environ: []string{"CustomAfterMicrosoftCommonTargets=/x/custom.targets"}, want: "CustomAfterMicrosoftCommonTargets"},
		{name: "common props", environ: []string{"CustomBeforeMicrosoftCommonProps=/x/custom.props"}, want: "CustomBeforeMicrosoftCommonProps"},
		{name: "cross-targeting", environ: []string{"CustomAfterMicrosoftCommonCrossTargetingTargets=/x/a.targets"}, want: "CustomAfterMicrosoftCommonCrossTargetingTargets"},
		{name: "language targets", environ: []string{"CustomBeforeMicrosoftCSharpTargets=/x/a.targets"}, want: "CustomBeforeMicrosoftCSharpTargets"},
		{name: "names ignore case", environ: []string{"customaftermicrosoftcommontargets=/x/a.targets"}, want: "customaftermicrosoftcommontargets"},
		{name: "empty value", environ: []string{"CustomAfterMicrosoftCommonTargets="}},
		{name: "Directory.Build hooks are modeled", environ: []string{"CustomAfterDirectoryBuildTargets=/x/a.targets"}},
		{name: "unrelated", environ: []string{"PATH=/bin", "=C:=C:\\"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := findUnmodeledMSBuildImports(test.environ, "linux", t.TempDir())
			if test.want == "" && got != "" || test.want != "" && !strings.Contains(got, test.want) {
				t.Fatalf("reason = %q, want %q", got, test.want)
			}
		})
	}
}

// MSBuildUserExtensionsPath is $(LocalAppData)/Microsoft/MSBuild, where
// LocalAppData is .NET's SpecialFolder.LocalApplicationData.
func TestMSBuildUserExtensionsPathsPerSystem(t *testing.T) {
	home := filepath.Join("h", "user")
	for _, test := range []struct {
		goos    string
		environ []string
		want    []string
	}{
		{goos: "linux", want: []string{filepath.Join(home, ".local", "share", "Microsoft", "MSBuild")}},
		{goos: "linux", environ: []string{"XDG_DATA_HOME=/data"}, want: []string{filepath.Join("/data", "Microsoft", "MSBuild")}},
		{goos: "linux", environ: []string{"XDG_DATA_HOME=relative"}, want: []string{filepath.Join(home, ".local", "share", "Microsoft", "MSBuild")}},
		{goos: "linux", environ: []string{"xdg_data_home=/data"}, want: []string{filepath.Join(home, ".local", "share", "Microsoft", "MSBuild")}},
		{goos: "darwin", want: []string{filepath.Join(home, ".local", "share", "Microsoft", "MSBuild"), filepath.Join(home, "Library", "Application Support", "Microsoft", "MSBuild")}},
		{goos: "windows", environ: []string{`LocalAppData=C:\Users\u\AppData\Local`}, want: []string{filepath.Join(`C:\Users\u\AppData\Local`, "Microsoft", "MSBuild")}},
		{goos: "windows", want: []string{filepath.Join(home, "AppData", "Local", "Microsoft", "MSBuild")}},
		{goos: "linux", environ: []string{"MSBuildUserExtensionsPath=/ext"}, want: []string{"/ext", filepath.Join(home, ".local", "share", "Microsoft", "MSBuild")}},
	} {
		if got := msbuildUserExtensionsPaths(test.environ, test.goos, home); !slices.Equal(got, test.want) {
			t.Errorf("%s %v: %v, want %v", test.goos, test.environ, got, test.want)
		}
	}
}

func TestUnmodeledImportsFromUserExtensionDirectories(t *testing.T) {
	for _, test := range []struct {
		name, file string
		found      bool
	}{
		{name: "common targets after", file: "Current/Microsoft.Common.targets/ImportAfter/a.targets", found: true},
		{name: "common targets before", file: "Current/Microsoft.Common.targets/ImportBefore/a.targets", found: true},
		{name: "common props", file: "Current/Imports/Microsoft.Common.props/ImportBefore/a.props", found: true},
		{name: "csharp targets", file: "Current/Microsoft.CSharp.targets/ImportAfter/a.targets", found: true},
		{name: "empty import directory", file: "Current/Microsoft.Common.targets/ImportAfter/"},
		{name: "only a subdirectory", file: "Current/Microsoft.Common.targets/ImportAfter/nested/"},
		{name: "another tools version", file: "v4.0/Microsoft.Common.targets/ImportAfter/a.targets"},
		{name: "nothing", file: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := t.TempDir()
			base := filepath.Join(data, "Microsoft", "MSBuild")
			if test.file != "" {
				path := filepath.Join(base, filepath.FromSlash(test.file))
				if strings.HasSuffix(test.file, "/") {
					if err := os.MkdirAll(path, 0o755); err != nil {
						t.Fatal(err)
					}
				} else {
					mustWriteFile(t, path, `<Project/>`)
				}
			}
			got := findUnmodeledMSBuildImports([]string{"XDG_DATA_HOME=" + data, "LOCALAPPDATA=" + data}, runtime.GOOS, t.TempDir())
			if (got != "") != test.found {
				t.Fatalf("reason = %q, want found=%v", got, test.found)
			}
		})
	}
}

// A file outside the workspace that MSBuild imports can Update the package,
// so the TUI's static owner is not trustworthy and the save needs MSBuild
// evaluation. Without dotnet on PATH that evaluation fails, and nothing is
// written.
func TestTUIStaticOwnershipYieldsToUnmodeledImports(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T)
	}{
		{name: "none configured"},
		{name: "environment property", setup: func(t *testing.T) {
			t.Setenv("CustomAfterMicrosoftCommonTargets", filepath.Join(t.TempDir(), "custom.targets"))
		}},
		{name: "user extension ImportAfter", setup: func(t *testing.T) {
			data := t.TempDir()
			t.Setenv("XDG_DATA_HOME", data)
			t.Setenv("LOCALAPPDATA", data)
			mustWriteFile(t, filepath.Join(data, "Microsoft", "MSBuild", "Current", "Microsoft.Common.targets", "ImportAfter", "custom.targets"),
				`<Project><ItemGroup><PackageReference Update="A" Version="9.0.0"/></ItemGroup></Project>`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := t.TempDir()
			t.Setenv("XDG_DATA_HOME", data)
			t.Setenv("LOCALAPPDATA", data)
			m, _, path := newActionTestApp(t, "")
			project := m.selectedProject()
			if project == nil || !project.RequiresEvaluation || !canEditLiteralDeclaration(project, "A") {
				t.Fatal("fixture must use the static owner")
			}
			before := readFile(t, path)
			t.Setenv("PATH", t.TempDir())
			if test.setup == nil {
				// The control: the same save takes the static owner.
				cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'}))
				if reconciled, ok := cmd().(mutationReconciledMsg); !ok || reconciled.err != nil || reconciled.written != 1 {
					t.Fatal("the static owner was not used")
				}
				return
			}
			test.setup(t)
			cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'}))
			if cmd == nil {
				t.Fatalf("update did not start: %s", m.ctx.StatusLine)
			}
			msg, ok := cmd().(writeResultMsg)
			if !ok || msg.err == nil || !strings.Contains(msg.err.Error(), "requires MSBuild evaluation") {
				t.Fatalf("update did not need MSBuild evaluation: %+v", msg)
			}
			if readFile(t, path) != before {
				t.Fatal("refused update changed the project")
			}
		})
	}
}
