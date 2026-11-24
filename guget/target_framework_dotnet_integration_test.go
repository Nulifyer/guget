//go:build integration

package main

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestFrameworkCompatibilityMatchesDotnetRestore restores a project against a
// local folder feed holding one package built for a single framework, and
// compares restore's verdict (error NU1202, warning NU1701, or success) with
// guget's CompatibilityWith. Packages are written in-process and every NuGet
// location is isolated, so the test needs no network.
func TestFrameworkCompatibilityMatchesDotnetRestore(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("machine-wide NuGet configuration can only be isolated on Linux")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK is not installed")
	}
	cases := []struct{ project, pkg string }{
		{"net48", "netstandard2.0"},
		{"net461", "netstandard2.0"},
		{"net47", "netstandard2.0"},
		{"net48", "net472"},
		{"net472", "net48"},
		{"net451", "net46"},
		{"net46", "net451"},
		{"net40", "netstandard1.0"},
		{"net45", "netstandard1.3"},
		{"net462", "netstandard2.1"},
		{"netcoreapp2.1", "netstandard2.1"},
		{"netcoreapp1.0", "netstandard2.0"},
		{"net8.0", "netcoreapp3.1"},
		{"net8.0", "netstandard2.1"},
		{"net8.0", "net472"},
		{"net8.0", "net45"},
		{"netstandard2.0", "net48"},
		{"netstandard1.6", "net461"},
		{"net8.0", "net8.0-windows7.0"},
		{"net8.0-windows", "net8.0"},
		{"net8.0-windows", "net8.0-windows7.0"},
		{"net8.0-windows", "net8.0-windows10.0.19041"},
		{"net8.0-windows10.0.19041", "net8.0-windows7.0"},
		{"net8.0", "portable-net45+win8+wpa81+wp8"},
	}
	base := t.TempDir()
	feed := filepath.Join(base, "feed")
	for i, tc := range cases {
		writeProbePackage(t, feed, probeID(i), tc.pkg)
	}
	mustWriteFile(t, filepath.Join(base, "nuget.config"), oracleConfig(`<packageSources><clear/><add key="local" value="`+feed+`"/></packageSources>`))
	for i, tc := range cases {
		i, tc := i, tc
		t.Run(tc.project+" uses "+tc.pkg, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(base, fmt.Sprintf("case%d", i))
			project := filepath.Join(dir, "App.csproj")
			mustWriteFile(t, project, fmt.Sprintf(`<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup>
<TargetFramework>%s</TargetFramework>
<DisableImplicitFrameworkReferences>true</DisableImplicitFrameworkReferences>
<AutomaticallyUseReferenceAssemblyPackages>false</AutomaticallyUseReferenceAssemblyPackages>
<EnableWindowsTargeting>true</EnableWindowsTargeting>
<CheckEolTargetFramework>false</CheckEolTargetFramework>
</PropertyGroup><ItemGroup><PackageReference Include="%s" Version="1.0.0" /></ItemGroup></Project>`, tc.project, probeID(i)))
			output := runCompatibilityRestore(t, project, filepath.Join(base, "nuget.config"), map[string]string{
				"HOME":                          filepath.Join(dir, "home"),
				"DOTNET_CLI_HOME":               filepath.Join(dir, "home"),
				"NUGET_COMMON_APPLICATION_DATA": filepath.Join(dir, "machine"),
				"NUGET_PACKAGES":                filepath.Join(dir, "packages"),
				"NUGET_HTTP_CACHE_PATH":         filepath.Join(dir, "http-cache"),
			})
			var restore FrameworkCompatibility
			switch {
			case strings.Contains(output, "error NU1202"):
				restore = FrameworkIncompatible
			case strings.Contains(output, "error"):
				t.Fatalf("restore failed for another reason:\n%s", output)
			case !strings.Contains(output, "Restored "):
				t.Fatalf("restore did not report restoring the project:\n%s", output)
			case strings.Contains(output, "warning NU1701"):
				restore = FrameworkFallback
			default:
				restore = FrameworkCompatible
			}
			t.Logf("restore: %v", restore)
			got := ParseTargetFramework(tc.project).CompatibilityWith([]TargetFramework{ParseTargetFramework(tc.pkg)})
			if got != restore {
				t.Fatalf("restore: %v; guget: %v\n%s", restore, got, output)
			}
		})
	}
}

func probeID(i int) string { return fmt.Sprintf("Oracle.Compat%d", i) }

// writeProbePackage writes a minimal .nupkg whose only asset is
// lib/<framework>/<id>.dll. Restore checks asset folders, not assembly bytes.
func writeProbePackage(t *testing.T, feed, id, framework string) {
	t.Helper()
	if err := os.MkdirAll(feed, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(feed, strings.ToLower(id)+".1.0.0.nupkg"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	entries := map[string]string{
		id + ".nuspec": `<?xml version="1.0" encoding="utf-8"?><package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd"><metadata><id>` + id +
			`</id><version>1.0.0</version><authors>guget</authors><description>compatibility probe</description></metadata></package>`,
		"lib/" + framework + "/" + id + ".dll": "probe",
	}
	for name, content := range entries {
		w, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}

func runCompatibilityRestore(t *testing.T, project, config string, env map[string]string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "dotnet", "restore", project, "--configfile", config, "-nologo")
	command.Env = append(os.Environ(), "DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1", "MSBUILDDISABLENODEREUSE=1")
	for name, value := range env {
		command.Env = append(command.Env, name+"="+value)
	}
	output, _ := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("dotnet restore timed out:\n%s", output)
	}
	return string(output)
}
