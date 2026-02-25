package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindProjectFiles_MixedFormats(t *testing.T) {
	td := testDataDir(t)
	files, err := FindProjectFiles(td)
	if err != nil {
		t.Fatal(err)
	}

	hasCsproj := false
	hasFsproj := false
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".csproj" {
			hasCsproj = true
		}
		if ext == ".fsproj" {
			hasFsproj = true
		}
	}
	if !hasCsproj {
		t.Fatal("expected at least one .csproj file")
	}
	if !hasFsproj {
		t.Fatal("expected at least one .fsproj file")
	}
}

func TestFindProjectFiles_IncludesCircularRefProjects(t *testing.T) {
	td := testDataDir(t)
	files, err := FindProjectFiles(td)
	if err != nil {
		t.Fatal(err)
	}

	foundA := false
	foundB := false
	for _, f := range files {
		base := filepath.Base(f)
		if base == "ServiceA.csproj" {
			foundA = true
		}
		if base == "ServiceB.csproj" {
			foundB = true
		}
	}
	if !foundA {
		t.Fatal("expected to find ServiceA.csproj")
	}
	if !foundB {
		t.Fatal("expected to find ServiceB.csproj")
	}
}

func TestFindProjectFiles_SkipsIgnoredDirs(t *testing.T) {
	td := testDataDir(t)
	files, err := FindProjectFiles(td)
	if err != nil {
		t.Fatal(err)
	}

	// test-dotnet has bin/ and obj/ subdirectories that contain .nuget.g.props
	// files but no .csproj files. Verify none of the returned paths go through
	// an ignored directory.
	ignored := map[string]bool{
		"bin": true, "obj": true, "node_modules": true,
		".git": true, ".vs": true, "packages": true,
	}
	for _, f := range files {
		parts := strings.Split(filepath.ToSlash(f), "/")
		for _, part := range parts {
			if ignored[strings.ToLower(part)] {
				t.Fatalf("found project in ignored directory %q: %s", part, f)
			}
		}
	}
}

func TestFindProjectFiles_ExpectedCount(t *testing.T) {
	td := testDataDir(t)
	files, err := FindProjectFiles(td)
	if err != nil {
		t.Fatal(err)
	}

	// test-dotnet contains: CapchaValidator, FSharpLib, HttpHelper, PerfTest,
	// Scryfall, Serialization, ServiceA, ServiceB, SqlLinqer, Zettabytes
	// = 9 .csproj + 1 .fsproj = 10 project files
	if len(files) != 10 {
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = filepath.Base(f)
		}
		t.Fatalf("expected 10 project files, got %d: %v", len(files), names)
	}
}

func TestFindProjectFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	files, err := FindProjectFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("expected 0 files in empty dir, got %d", len(files))
	}
}

func TestFindProjectFiles_NonexistentDir(t *testing.T) {
	_, err := FindProjectFiles(filepath.Join(os.TempDir(), "nonexistent_guget_test_dir"))
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestFindProjectFiles_ExcludesPropsFiles(t *testing.T) {
	td := testDataDir(t)
	files, err := FindProjectFiles(td)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".props" {
			t.Fatalf("FindProjectFiles should not return .props files, got: %s", f)
		}
	}
}
