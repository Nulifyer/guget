package packageops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner func(context.Context, string, ...string) ([]byte, error)

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

func TestMSBuildInspectorSeparatesReferenceAndVersionOwners(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "App.csproj")
	central := filepath.Join(root, "Directory.Packages.props")
	for _, path := range []string{project, central} {
		if err := os.WriteFile(path, []byte("<Project />"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	json := strings.NewReplacer("PROJECT", project, "CENTRAL", central).Replace(`{
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
