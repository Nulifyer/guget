package packageops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageUsesAttributeGlobalReferencesAndKeepFirstItems(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "App.csproj")
	central := filepath.Join(root, "Directory.Packages.props")
	targets := filepath.Join(root, "sdk", "NuGet.targets")
	for path, data := range map[string]string{
		project: `<Project><ItemGroup><PackageReference Include="Dup" /><PackageReference Include="Pinned" Version="1.0.0" /></ItemGroup></Project>`,
		central: `<Project><ItemGroup><GlobalPackageReference Include="Analyzer" Version="1.2.3" /><PackageVersion Include="Dup" Version="1.0.0" /><PackageVersion Include="Dup" Version="2.0.0" /></ItemGroup></Project>`,
	} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output := msbuildOutput{
		Properties: map[string]string{"_CentralPackageVersionsEnabled": "true"},
		Items: map[string][]msbuildItem{
			"PackageReference": {
				{Identity: "Dup", DefiningProjectFullPath: project},
				{Identity: "Pinned", Version: "1.0.0", DefiningProjectFullPath: project},
				{Identity: "Analyzer", PrivateAssets: "All", DefiningProjectFullPath: targets},
			},
			"PackageVersion": {
				{Identity: "Dup", Version: "1.0.0", DefiningProjectFullPath: central},
				{Identity: "Dup", Version: "2.0.0", DefiningProjectFullPath: central},
				{Identity: "Analyzer", Version: "1.2.3", DefiningProjectFullPath: targets},
			},
			"GlobalPackageReference": {{Identity: "Analyzer", Version: "1.2.3", DefiningProjectFullPath: central}},
		},
	}
	uses := map[string]PackageUse{}
	for _, use := range packageUses(project, "net8.0", output, nil) {
		uses[use.PackageID] = use
	}
	if use := uses["Analyzer"]; !use.Global || use.ReferenceOwner != central || use.VersionOwner != central || use.PrivateAssets != "All" || use.EvaluatedExpression != "1.2.3" {
		t.Fatalf("global = %+v", use)
	}
	if use := uses["Dup"]; use.EvaluatedExpression != "1.0.0" {
		t.Fatalf("first PackageVersion must win: %+v", use)
	}
	if use := uses["Pinned"]; use.Edit.Supported || !strings.Contains(use.Edit.Reason, "NU1008") {
		t.Fatalf("Version under CPM = %+v", use)
	}
}

func TestPackageUsesKeepFirstDuplicateReference(t *testing.T) {
	project := filepath.Join(t.TempDir(), "App.csproj")
	if err := os.WriteFile(project, []byte(`<Project />`), 0o644); err != nil {
		t.Fatal(err)
	}
	output := msbuildOutput{Items: map[string][]msbuildItem{"PackageReference": {
		{Identity: "Pkg", Version: "1.0.0", DefiningProjectFullPath: "/first.props"},
		{Identity: "pkg", Version: "2.0.0", DefiningProjectFullPath: project},
	}}}
	uses := packageUses(project, "net8.0", output, nil)
	if len(uses) != 1 || uses[0].EvaluatedExpression != "1.0.0" || uses[0].Edit.Supported || !strings.Contains(uses[0].Edit.Reason, "NU1504") {
		t.Fatalf("uses = %+v", uses)
	}
}
