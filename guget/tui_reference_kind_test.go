package main

import (
	"path/filepath"
	"strings"
	"testing"

	text "github.com/nulifyer/guget/internal/ui/text"
)

func TestDetailNamesGlobalAndPrivateAssetReferences(t *testing.T) {
	m, _, path := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0" PrivateAssets="all"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
	m.selectPackageByName("A")
	if detail := text.Strip(m.renderDetail(selectedRow(m))); !strings.Contains(detail, "Private assets\nall") {
		t.Fatalf("PrivateAssets is not shown:\n%s", detail)
	}
	m.selectPackageByName("B")
	if detail := text.Strip(m.renderDetail(selectedRow(m))); strings.Contains(detail, "Private assets") || strings.Contains(detail, "Global reference") {
		t.Fatalf("plain reference gained a reference note:\n%s", detail)
	}

	// A GlobalPackageReference comes from a shared file and applies to every
	// project importing it.
	project := m.selectedProject()
	owner := filepath.Join(filepath.Dir(path), "Directory.Packages.props")
	project.Packages = Set[PackageReference]{{Name: "A", Version: ParseSemVer("1.0.0"), Requested: "1.0.0", Global: true, PrivateAssets: "All"}: {}}
	project.PackageSources = map[string]string{"a": owner}
	m.rebuildPackageRows()
	m.selectPackageByName("A")
	detail := strings.Join(strings.Fields(text.Strip(m.renderDetail(selectedRow(m)))), " ")
	if !strings.Contains(detail, "Global reference Every project importing Directory.Packages.props gets it, with PrivateAssets=All.") || strings.Contains(detail, "Private assets") {
		t.Fatalf("global reference is not explained: %s", detail)
	}
	m.projects.cursor = 0
	m.rebuildPackageRows()
	m.selectPackageByName("A")
	if versions := text.Strip(m.renderDetailProjectVersions(selectedRow(m))); !strings.Contains(versions, "1.0.0 global") {
		t.Fatalf("All Projects hides the global reference: %s", versions)
	}
}
