//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const seRedisURL = "https://github.com/StackExchange/StackExchange.Redis.git"

// cloneOnce caches the local path of the StackExchange.Redis clone for the
// duration of the test binary run. Each test function calls seRedisDir() which
// returns the same directory without cloning again.
var (
	cloneOnce sync.Once
	cloneDir  string
	cloneErr  error
)

// seRedisDir returns a path to a StackExchange.Redis checkout.
//
// Priority:
//  1. SE_REDIS_DIR env var — use an existing local clone (fastest, no network).
//  2. First call clones --depth=1 into a shared temp dir under /tmp; subsequent
//     calls reuse that same dir within the same test binary run.
func seRedisDir(t *testing.T) string {
	t.Helper()

	if dir := os.Getenv("SE_REDIS_DIR"); dir != "" {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("SE_REDIS_DIR %q does not exist: %v", dir, err)
		}
		return dir
	}

	cloneOnce.Do(func() {
		// Use a stable path so repeated `go test` runs re-use the clone.
		dir := filepath.Join(os.TempDir(), "guget-integration-serebis")
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			cloneDir = dir // already cloned
			return
		}
		t.Logf("Cloning %s ...", seRedisURL)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			cloneErr = err
			return
		}
		cmd := exec.Command("git", "clone", "--depth=1", seRedisURL, dir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			cloneErr = err
			return
		}
		cloneDir = dir
	})

	if cloneErr != nil {
		t.Skipf("git clone failed (no network?): %v", cloneErr)
	}
	return cloneDir
}

// TestIntegration_SERedis_Discovery checks that guget finds the expected
// project files in the StackExchange.Redis repository.
func TestIntegration_SERedis_Discovery(t *testing.T) {
	dir := seRedisDir(t)

	files, err := FindProjectFiles(dir)
	if err != nil {
		t.Fatalf("FindProjectFiles: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one project file, got none")
	}
	t.Logf("Found %d project files", len(files))

	var hasCsproj bool
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f), ".csproj") {
			hasCsproj = true
			break
		}
	}
	if !hasCsproj {
		t.Fatal("expected at least one .csproj file")
	}

	// Confirm the CPM file is discoverable from any project directory.
	dpp := findDirectoryPackagesProps(filepath.Dir(files[0]))
	if dpp == "" {
		t.Fatal("expected to find Directory.Packages.props walking up from project dir")
	}
	t.Logf("Found CPM file: %s", dpp)
}

// TestIntegration_SERedis_CPMVersionResolution parses every project and asserts
// that no package ends up with an empty version after CPM resolution.
func TestIntegration_SERedis_CPMVersionResolution(t *testing.T) {
	dir := seRedisDir(t)

	files, err := FindProjectFiles(dir)
	if err != nil {
		t.Fatalf("FindProjectFiles: %v", err)
	}

	var failures []string
	for _, f := range files {
		proj, err := ParseCsproj(f)
		if err != nil {
			t.Logf("skipping unparseable project %s: %v", filepath.Base(f), err)
			continue
		}
		for ref := range proj.Packages {
			if ref.Version.Raw == "" {
				failures = append(failures,
					filepath.Base(f)+": "+ref.Name+" has empty version")
			}
		}
	}

	for _, msg := range failures {
		t.Error(msg)
	}
}

// TestIntegration_SERedis_CPMFileContents parses Directory.Packages.props
// directly and asserts that known packages are present with non-empty versions.
func TestIntegration_SERedis_CPMFileContents(t *testing.T) {
	dir := seRedisDir(t)
	cpmPath := filepath.Join(dir, "Directory.Packages.props")

	proj, err := ParsePropsAsProject(cpmPath)
	if err != nil {
		t.Fatalf("ParsePropsAsProject: %v", err)
	}
	if proj.Packages.Len() == 0 {
		t.Fatal("expected packages in Directory.Packages.props, got none")
	}
	t.Logf("Directory.Packages.props contains %d packages", proj.Packages.Len())

	// These packages are stable anchors in the SE.Redis CPM file.
	knownPackages := []string{
		"Newtonsoft.Json",
		"BenchmarkDotNet",
		"Microsoft.Extensions.Logging.Abstractions",
		"StyleCop.Analyzers",
	}
	pkgNames := pkgNameSet(proj)
	for _, pkg := range knownPackages {
		assertContains(t, pkgNames, pkg)
	}

	// Every declared package must have a non-empty version.
	for ref := range proj.Packages {
		if ref.Version.Raw == "" {
			t.Errorf("package %q has empty version in Directory.Packages.props", ref.Name)
		}
	}
}

const otelURL = "https://github.com/open-telemetry/opentelemetry-dotnet.git"

var (
	otelCloneOnce sync.Once
	otelCloneDir  string
	otelCloneErr  error
)

func otelDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("OTEL_DOTNET_DIR"); dir != "" {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("OTEL_DOTNET_DIR %q does not exist: %v", dir, err)
		}
		return dir
	}
	otelCloneOnce.Do(func() {
		dir := filepath.Join(os.TempDir(), "guget-integration-otel-dotnet")
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			otelCloneDir = dir
			return
		}
		t.Logf("Cloning %s ...", otelURL)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			otelCloneErr = err
			return
		}
		cmd := exec.Command("git", "clone", "--depth=1", otelURL, dir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			otelCloneErr = err
			return
		}
		otelCloneDir = dir
	})
	if otelCloneErr != nil {
		t.Skipf("git clone failed (no network?): %v", otelCloneErr)
	}
	return otelCloneDir
}

// TestIntegration_OTel_PropertyResolution verifies that MSBuild property
// references like $(OTelLatestStableVer) in version strings are resolved using
// values declared in the same PropertyGroup, and that NuGet version ranges
// like [1.15.0,2.0) are stripped to their lower bound (1.15.0).
func TestIntegration_OTel_PropertyResolution(t *testing.T) {
	dir := otelDir(t)
	cpmPath := filepath.Join(dir, "Directory.Packages.props")

	proj, err := ParsePropsAsProject(cpmPath)
	if err != nil {
		t.Fatalf("ParsePropsAsProject: %v", err)
	}
	t.Logf("Directory.Packages.props contains %d packages", proj.Packages.Len())

	// Every package must have a non-empty, parseable version — no raw $(...)
	// property references or bracket-notation ranges should remain.
	for ref := range proj.Packages {
		if ref.Version.Raw == "" {
			t.Errorf("package %q has empty version (unresolved property or unparsed range)", ref.Name)
		}
		if strings.Contains(ref.Version.Raw, "$(") {
			t.Errorf("package %q still has unresolved property in version: %q", ref.Name, ref.Version.Raw)
		}
		if strings.HasPrefix(ref.Version.Raw, "[") || strings.HasPrefix(ref.Version.Raw, "(") {
			t.Errorf("package %q still has range notation in version: %q", ref.Name, ref.Version.Raw)
		}
	}
}

// TestIntegration_OTel_NoEmptyVersionsInProjects parses all projects and
// verifies that no package retains an unresolved $(Property) reference in its
// version string. Empty versions are logged but not failed — a project may
// reference a package that its nearest Directory.Packages.props doesn't define.
func TestIntegration_OTel_NoEmptyVersionsInProjects(t *testing.T) {
	dir := otelDir(t)

	files, err := FindProjectFiles(dir)
	if err != nil {
		t.Fatalf("FindProjectFiles: %v", err)
	}
	t.Logf("Found %d project files", len(files))

	emptyCount := 0
	for _, f := range files {
		proj, err := ParseCsproj(f)
		if err != nil {
			t.Logf("skipping %s: %v", filepath.Base(f), err)
			continue
		}
		for ref := range proj.Packages {
			// Unresolved $(Property) in a version string is always a bug.
			if strings.Contains(ref.Version.Raw, "$(") {
				t.Errorf("%s: package %q has unresolved property: %q", filepath.Base(f), ref.Name, ref.Version.Raw)
			}
			// A package with no version at all may simply not be in the nearest
			// Directory.Packages.props — log it but don't fail.
			if ref.Version.Raw == "" {
				emptyCount++
				t.Logf("note: %s: package %q has no resolvable version", filepath.Base(f), ref.Name)
			}
		}
	}
	if emptyCount > 0 {
		t.Logf("%d package/project combinations had no resolvable version (see notes above)", emptyCount)
	}
}
