package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestListShowsPackageDownloadsAsReadOnly(t *testing.T) {
	root := t.TempDir()
	writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
  <ItemGroup><PackageDownload Include="Tool.Package" Version="[2.0.0]" /></ItemGroup>
</Project>
`)
	mustWriteFile(t, filepath.Join(root, "App", "packages.App.config"), `<packages />`)
	_, stdout, stderr := runCLIForTest(t, "list", "--project", root, "--format", "json")
	var document listDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(document.Packages) != 1 {
		t.Fatalf("packages = %+v", document.Packages)
	}
	row := document.Packages[0]
	if !row.Download || row.Direct || row.EditSupported || row.RequestedExpression == nil || *row.RequestedExpression != "[2.0.0]" || row.EditReason == nil || *row.EditReason != downloadEditReason {
		t.Fatalf("download row = %+v", row)
	}
	if !strings.Contains(stderr, "packages.App.config: packages.config mutation is not supported") {
		t.Fatalf("project-named packages.config was not reported: %s", stderr)
	}
}

func TestProjectNamedPackagesConfigNeedsItsProject(t *testing.T) {
	root := t.TempDir()
	writeCLIProject(t, root, "App", `<Project />`)
	for name, want := range map[string]bool{"packages.App.config": true, "packages.Other.config": false, "packages..config": false} {
		if got := projectPackagesConfig(filepath.Join(root, "App", name)); got != want {
			t.Errorf("projectPackagesConfig(%s) = %v, want %v", name, got, want)
		}
	}
}
