package packageops

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner func(context.Context, string, ...string) ([]byte, error)

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

func TestMSBuildInspectorSeparatesReferenceAndVersionOwners(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "App.csproj")
	central := filepath.Join(root, "Directory.Packages.props")
	if err := os.WriteFile(project, []byte(`<Project><ItemGroup><PackageReference Include="Example.Core" /></ItemGroup></Project>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(central, []byte(`<Project><ItemGroup><PackageVersion Include="Example.Core" Version="[1.2.0,2.0.0)" /></ItemGroup></Project>`), 0o644); err != nil {
		t.Fatal(err)
	}
	escapePath := func(path string) string { data, _ := json.Marshal(path); return string(data[1 : len(data)-1]) }
	json := strings.NewReplacer("PROJECT", escapePath(project), "CENTRAL", escapePath(central)).Replace(`{
  "Properties":{"TargetFramework":"net8.0","TargetFrameworks":"","ManagePackageVersionsCentrally":"true"},
  "Items":{
    "PackageReference":[{"Identity":"Example.Core","DefiningProjectFullPath":"PROJECT"}],
    "PackageVersion":[{"Identity":"Example.Core","Version":"[1.2.0,2.0.0)","DefiningProjectFullPath":"CENTRAL"}]
  }
}`)
	inspector := MSBuildInspector{Runner: fakeRunner(func(context.Context, string, ...string) ([]byte, error) {
		return []byte(json), nil
	})}
	snapshot, err := inspector.InspectProject(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PackageUses) != 1 {
		t.Fatalf("uses = %#v", snapshot.PackageUses)
	}
	use := snapshot.PackageUses[0]
	if use.ReferenceOwner != project || use.VersionOwner != central {
		t.Fatalf("owners = %q, %q", use.ReferenceOwner, use.VersionOwner)
	}
	if use.EvaluatedExpression != "[1.2.0,2.0.0)" || !use.Edit.Supported {
		t.Fatalf("use = %#v", use)
	}
}

func TestMSBuildInspectorPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	inspector := MSBuildInspector{Runner: fakeRunner(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, ctx.Err()
	})}
	_, err := inspector.InspectProject(ctx, "App.csproj")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestMSBuildInspectorIncludesImportedPackageWithEvaluatedOwner(t *testing.T) {
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK required for imported ownership")
	}
	root := t.TempDir()
	project := filepath.Join(root, "App.csproj")
	owner := filepath.Join(root, "Imported.props")
	for path, data := range map[string]string{
		project: `<Project Sdk="Microsoft.NET.Sdk"><Import Project="$(MSBuildProjectDirectory)/Imported.props"/><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
		owner:   `<Project><ItemGroup><PackageReference Include="Polly" Version="8.5.2"/></ItemGroup></Project>`,
	} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	snapshot, err := (MSBuildInspector{}).InspectProject(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	for _, use := range snapshot.PackageUses {
		if use.PackageID != "Polly" {
			continue
		}
		// Windows runners can give Go an 8.3 temp path while MSBuild returns
		// its long form. Compare the actual files, not their path spelling.
		expected, err := os.Stat(owner)
		if err != nil {
			t.Fatal(err)
		}
		for _, actual := range []string{use.ReferenceOwner, use.VersionOwner} {
			info, err := os.Stat(actual)
			if err != nil || !os.SameFile(expected, info) {
				t.Fatalf("imported owner = %q, want file %q: %v", actual, owner, err)
			}
		}
		if use.EvaluatedExpression != "8.5.2" || !use.Edit.Supported {
			t.Fatalf("imported package ownership = %+v", use)
		}
		return
	}
	t.Fatalf("imported Polly reference is missing: %+v", snapshot.PackageUses)
}

func TestEvaluatedOwnershipRefusesSourceMetadataOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.csproj")
	if err := os.WriteFile(path, []byte(`<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`), 0o644); err != nil {
		t.Fatal(err)
	}
	output := msbuildOutput{Items: map[string][]msbuildItem{"PackageReference": {{Identity: "Example", Version: "2.0.0", DefiningProjectFullPath: path}}}}
	uses := packageUses(path, "net8.0", output)
	if len(uses) != 1 || uses[0].Edit.Supported || !strings.Contains(uses[0].Edit.Reason, "differs") {
		t.Fatalf("uses=%+v", uses)
	}
}
