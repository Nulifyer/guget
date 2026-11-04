package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestSnapshotOwnerScopeUsesRecordedIdentityWithoutCurrentFiles(t *testing.T) {
	// None of these files exists. Snapshot identities remain usable even when a
	// remote mount is unavailable or a symlink is replaced after discovery.
	owner := filepath.Join("absent", "shared.props")
	alias := filepath.Join("absent", "alias.props")
	selected := &ParsedProject{FilePath: "First.csproj", PackageSources: map[string]string{"a": owner}, PackageOwnerIdentities: map[string]string{owner: "recorded-owner"}}
	consumer := &ParsedProject{FilePath: "Second.csproj", PackageSources: map[string]string{"a": alias}, PackageOwnerIdentities: map[string]string{alias: "recorded-owner"}}
	unrelated := &ParsedProject{FilePath: "Other.csproj", PackageSources: map[string]string{"a": "unrelated.props"}}
	got := snapshotAffectedProjects([]*ParsedProject{selected, consumer, unrelated}, []*ParsedProject{selected}, "A")
	if len(got) != 2 || got[0] != selected || got[1] != consumer {
		t.Fatalf("consumers=%v", got)
	}
}

func TestSnapshotOwnerScopeHasPureLexicalFallback(t *testing.T) {
	owner := filepath.Join("absent", "shared.props")
	selected := &ParsedProject{FilePath: "First.csproj", PackageSources: map[string]string{"a": owner}}
	consumer := &ParsedProject{FilePath: "Second.csproj", PackageSources: map[string]string{"a": owner}}
	got := snapshotAffectedProjects([]*ParsedProject{selected, consumer}, []*ParsedProject{selected}, "A")
	if len(got) != 2 {
		t.Fatalf("synthetic consumers=%v", got)
	}
}

func TestSnapshotOwnerIdentityDoesNotFollowReplacedSourceMapping(t *testing.T) {
	project := &ParsedProject{FilePath: "First.csproj", PackageSources: map[string]string{"a": "new.props"}, PackageOwnerIdentities: map[string]string{"old.props": "old-owner"}}
	if got := snapshotPackageOwner(project, "A"); got != "new.props" {
		t.Fatalf("owner=%s", got)
	}
}

func TestSnapshotOwnerScopeHandlesPlatformPathCase(t *testing.T) {
	selected := &ParsedProject{FilePath: "First.csproj", PackageSources: map[string]string{"a": "owner"}, PackageOwnerIdentities: map[string]string{"owner": "/workspace/Shared.props"}}
	consumer := &ParsedProject{FilePath: "Second.csproj", PackageSources: map[string]string{"a": "alias"}, PackageOwnerIdentities: map[string]string{"alias": "/workspace/SHARED.props"}}
	consumers := snapshotAffectedProjects([]*ParsedProject{selected, consumer}, []*ParsedProject{selected}, "A")
	want := 1
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		want = 2
	}
	if len(consumers) != want {
		t.Fatalf("case aliases: got %d consumers, want %d on %s", len(consumers), want, runtime.GOOS)
	}
}
