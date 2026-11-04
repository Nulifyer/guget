//go:build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/packageops"
)

// An SDK package-list process can succeed with an empty stale graph after a
// failed restore. Evaluated direct references must still make the read partial.
func TestCLIRecognizesSuccessfulButIncompleteRestoreGraph(t *testing.T) {
	sdk, err := exec.LookPath("dotnet")
	if err != nil {
		t.Skip("dotnet SDK unavailable")
	}
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><EnableTargetingPackDownload>false</EnableTargetingPackDownload></PropertyGroup></Project>`)
	config := filepath.Join(root, "NuGet.Config")
	mustWriteFile(t, config, `<configuration><packageSources><clear /></packageSources></configuration>`)
	restore := exec.Command(sdk, "restore", project, "--configfile", config)
	restore.Env = append(os.Environ(), "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1")
	if output, err := restore.CombinedOutput(); err != nil {
		t.Fatalf("restore local SDK fixture: %v\n%s", err, output)
	}
	mustWriteFile(t, project, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><EnableTargetingPackDownload>false</EnableTargetingPackDownload></PropertyGroup><ItemGroup><PackageReference Include="Guget.Nonexistent.Offline.Test.Package" Version="1.0.0" /></ItemGroup></Project>`)
	// A failed offline restore leaves enough stale assets for SDK 10 to emit an
	// empty successful package list. No public feed is used by this fixture.
	failed := exec.Command(sdk, "restore", project, "--configfile", config)
	failed.Env = restore.Env
	if output, err := failed.CombinedOutput(); err == nil {
		t.Fatalf("expected offline restore refusal: %s", output)
	}
	uses, err := (packageops.NuGetGraphInspector{}).ResolveProject(context.Background(), project, false)
	if err != nil || len(uses) != 0 {
		t.Skipf("SDK did not reproduce successful empty graph: uses=%+v err=%v", uses, err)
	}
	code, output, diagnostic := runCLIForTest(t, "list", "--project", root, "--format", "json")
	if code != ExitPartial || !strings.Contains(output, "Guget.Nonexistent.Offline.Test.Package") || !strings.Contains(diagnostic, "restore metadata is incomplete") {
		t.Fatalf("code=%d output=%s diagnostic=%s", code, output, diagnostic)
	}
}
