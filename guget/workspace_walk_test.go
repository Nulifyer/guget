package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// isolateGitExcludes keeps the developer's git configuration out of a test.
func isolateGitExcludes(t *testing.T, path string) {
	t.Helper()
	isolateGitConfig(t, gitRepoConfig{ExcludesFile: path})
}

func isolateGitConfig(t *testing.T, config gitRepoConfig) {
	t.Helper()
	previous := readGitRepoConfig
	readGitRepoConfig = func(string) gitRepoConfig { return config }
	t.Cleanup(func() { readGitRepoConfig = previous })
}

// initFakeRepo creates the .git directory that marks a repository root without
// needing git installed.
func initFakeRepo(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func relPaths(t *testing.T, root string, paths []string) []string {
	t.Helper()
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, filepath.ToSlash(rel))
	}
	slices.Sort(result)
	return result
}

func mustWalk(t *testing.T, root string, opts workspaceScanOptions) *workspaceTree {
	t.Helper()
	tree, err := walkWorkspace(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestWalkWorkspaceSkipsGitIgnoredDirectories(t *testing.T) {
	isolateGitExcludes(t, "")
	root := t.TempDir()
	initFakeRepo(t, root)
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "bin/\n.local-results/\n*.generated.props\n")
	mustWriteFile(t, filepath.Join(root, "App", "App.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, ".local-results", "run1", "App", "App.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, ".local-results", "run1", "packages.config"), "<packages />")
	mustWriteFile(t, filepath.Join(root, "App", "out.generated.props"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "App", "bin", "Copy.csproj"), "<Project />")

	tree := mustWalk(t, root, workspaceScanOptions{})
	if got := relPaths(t, root, tree.ProjectFiles); !slices.Equal(got, []string{"App/App.csproj"}) {
		t.Fatalf("projects = %v", got)
	}
	if got := relPaths(t, root, tree.WatchedFiles); !slices.Equal(got, []string{"App/App.csproj"}) {
		t.Fatalf("watched = %v", got)
	}
	if len(tree.Unsupported) != 0 {
		t.Fatalf("unsupported = %v", tree.Unsupported)
	}
	// bin is skipped by the built-in list first, so only .local-results and
	// the generated props file count as .gitignore exclusions.
	if tree.GitIgnored != 2 {
		t.Fatalf("GitIgnored = %d, want 2", tree.GitIgnored)
	}

	all := mustWalk(t, root, workspaceScanOptions{IncludeIgnored: true})
	want := []string{".local-results/run1/App/App.csproj", "App/App.csproj"}
	if got := relPaths(t, root, all.ProjectFiles); !slices.Equal(got, want) {
		t.Fatalf("projects with IncludeIgnored = %v, want %v (bin must stay excluded)", got, want)
	}
	if len(all.Unsupported) != 1 || all.GitIgnored != 0 {
		t.Fatalf("IncludeIgnored tree = %+v", all)
	}
}

func TestWalkWorkspaceIgnoresGitignoreOutsideRepository(t *testing.T) {
	isolateGitExcludes(t, "")
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "Tools/\n")
	mustWriteFile(t, filepath.Join(root, "Tools", "Tool.csproj"), "<Project />")

	tree := mustWalk(t, root, workspaceScanOptions{})
	if got := relPaths(t, root, tree.ProjectFiles); !slices.Equal(got, []string{"Tools/Tool.csproj"}) {
		t.Fatalf("projects = %v; .gitignore outside a repository must not apply", got)
	}
}

func TestWalkWorkspaceAppliesAncestorAndNestedRules(t *testing.T) {
	isolateGitExcludes(t, "")
	repo := t.TempDir()
	initFakeRepo(t, repo)
	mustWriteFile(t, filepath.Join(repo, ".gitignore"), "scratch/\n*.csproj\n!Keep.csproj\n")
	mustWriteFile(t, filepath.Join(repo, "src", ".gitignore"), "!Lib.csproj\n")
	mustWriteFile(t, filepath.Join(repo, "src", "Lib", "Lib.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(repo, "src", "Other", "Other.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(repo, "src", "Keep", "Keep.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(repo, "src", "scratch", "Keep.csproj"), "<Project />")

	// Start below the repository root: the root .gitignore must still apply.
	root := filepath.Join(repo, "src")
	tree := mustWalk(t, root, workspaceScanOptions{})
	want := []string{"Keep/Keep.csproj", "Lib/Lib.csproj"}
	if got := relPaths(t, root, tree.ProjectFiles); !slices.Equal(got, want) {
		t.Fatalf("projects = %v, want %v", got, want)
	}
}

func TestWalkWorkspaceReadsInfoExcludeAndGlobalExcludes(t *testing.T) {
	global := filepath.Join(t.TempDir(), "ignore")
	mustWriteFile(t, global, "GlobalSkip/\n")
	isolateGitExcludes(t, global)
	root := t.TempDir()
	initFakeRepo(t, root)
	mustWriteFile(t, filepath.Join(root, ".git", "info", "exclude"), "LocalSkip/\n")
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "!GlobalSkip/\n")
	for _, name := range []string{"App", "LocalSkip", "GlobalSkip"} {
		mustWriteFile(t, filepath.Join(root, name, name+".csproj"), "<Project />")
	}

	tree := mustWalk(t, root, workspaceScanOptions{})
	// The repository's .gitignore outranks the global file, so its negation
	// re-includes GlobalSkip.
	want := []string{"App/App.csproj", "GlobalSkip/GlobalSkip.csproj"}
	if got := relPaths(t, root, tree.ProjectFiles); !slices.Equal(got, want) {
		t.Fatalf("projects = %v, want %v", got, want)
	}
}

func TestWalkWorkspaceNestedRepositoryUsesItsOwnRules(t *testing.T) {
	isolateGitExcludes(t, "")
	root := t.TempDir()
	initFakeRepo(t, root)
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "gen/\n")
	nested := filepath.Join(root, "vendored-repo")
	initFakeRepo(t, nested)
	mustWriteFile(t, filepath.Join(nested, "gen", "Gen.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "gen", "Skipped.csproj"), "<Project />")

	tree := mustWalk(t, root, workspaceScanOptions{})
	if got := relPaths(t, root, tree.ProjectFiles); !slices.Equal(got, []string{"vendored-repo/gen/Gen.csproj"}) {
		t.Fatalf("projects = %v", got)
	}
}

func TestWalkWorkspaceSearchesRootWithBuiltInExcludedName(t *testing.T) {
	isolateGitExcludes(t, "")
	root := filepath.Join(t.TempDir(), "build")
	mustWriteFile(t, filepath.Join(root, "App", "App.csproj"), "<Project />")

	tree := mustWalk(t, root, workspaceScanOptions{})
	if len(tree.ProjectFiles) != 1 {
		t.Fatalf("projects = %v; an explicitly selected root must be searched", tree.ProjectFiles)
	}
}

func TestWalkWorkspaceKeepsTrackedFilesInIgnoredDirectories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	isolateGitExcludes(t, "")
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "vendor-src/\n")
	mustWriteFile(t, filepath.Join(root, "vendor-src", "Tracked", "Tracked.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "vendor-src", "Untracked", "Untracked.csproj"), "<Project />")
	runGit(t, root, "add", "-f", "vendor-src/Tracked/Tracked.csproj")

	tree := mustWalk(t, root, workspaceScanOptions{})
	if got := relPaths(t, root, tree.ProjectFiles); !slices.Equal(got, []string{"vendor-src/Tracked/Tracked.csproj"}) {
		t.Fatalf("projects = %v", got)
	}
}

func TestWalkWorkspaceFollowsCoreIgnoreCase(t *testing.T) {
	root := t.TempDir()
	initFakeRepo(t, root)
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "Debug/\n")
	mustWriteFile(t, filepath.Join(root, "App", "App.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "debug", "Copy", "Copy.csproj"), "<Project />")

	isolateGitConfig(t, gitRepoConfig{})
	if got := relPaths(t, root, mustWalk(t, root, workspaceScanOptions{}).ProjectFiles); len(got) != 2 {
		t.Fatalf("case-sensitive projects = %v; Debug/ must not match debug", got)
	}
	isolateGitConfig(t, gitRepoConfig{IgnoreCase: true})
	if got := relPaths(t, root, mustWalk(t, root, workspaceScanOptions{}).ProjectFiles); !slices.Equal(got, []string{"App/App.csproj"}) {
		t.Fatalf("case-insensitive projects = %v", got)
	}
}

func TestWalkWorkspaceKeepsTrackedFilesCaseInsensitively(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	isolateGitConfig(t, gitRepoConfig{IgnoreCase: true})
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "VENDOR-SRC/\n")
	mustWriteFile(t, filepath.Join(root, "vendor-src", "Tracked", "Tracked.csproj"), "<Project />")
	mustWriteFile(t, filepath.Join(root, "vendor-src", "Untracked", "Untracked.csproj"), "<Project />")
	runGit(t, root, "add", "-f", "vendor-src/Tracked/Tracked.csproj")

	tree := mustWalk(t, root, workspaceScanOptions{})
	if got := relPaths(t, root, tree.ProjectFiles); !slices.Equal(got, []string{"vendor-src/Tracked/Tracked.csproj"}) {
		t.Fatalf("projects = %v", got)
	}
}

func TestParseGitRepoConfig(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	out := []byte("core.excludesfile\n/abs/ignore\x00core.ignorecase\nfalse\x00core.ignorecase\x00")
	config := parseGitRepoConfig(root, out)
	if config.ExcludesFile != filepath.Clean("/abs/ignore") {
		t.Fatalf("ExcludesFile = %q", config.ExcludesFile)
	}
	if !config.IgnoreCase {
		t.Fatal("a later valueless core.ignorecase must mean true")
	}
	relative := parseGitRepoConfig(root, []byte("core.excludesfile\nlocal/ignore\x00core.ignorecase\nYes\x00"))
	if relative.ExcludesFile != filepath.Join(root, "local", "ignore") || !relative.IgnoreCase {
		t.Fatalf("config = %+v", relative)
	}
	if empty := parseGitRepoConfig(root, nil); empty != (gitRepoConfig{}) {
		t.Fatalf("empty config = %+v", empty)
	}
}

func TestLoadGitRepoConfigReadsRepositorySettings(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "none"))
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "core.ignoreCase", "true")
	runGit(t, root, "config", "core.excludesFile", "~/custom-ignore")

	config := loadGitRepoConfig(root)
	if !config.IgnoreCase || config.ExcludesFile != filepath.Join(home, "custom-ignore") {
		t.Fatalf("config = %+v", config)
	}
	runGit(t, root, "config", "--unset", "core.excludesFile")
	runGit(t, root, "config", "core.ignoreCase", "false")
	config = loadGitRepoConfig(root)
	if config.IgnoreCase || config.ExcludesFile != filepath.Join(home, "git", "ignore") {
		t.Fatalf("defaults = %+v", config)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(home, "none"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestScanWorkspaceExplainsGitIgnoredProjects(t *testing.T) {
	isolateGitExcludes(t, "")
	root := t.TempDir()
	initFakeRepo(t, root)
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "samples/\n")
	mustWriteFile(t, filepath.Join(root, "samples", "App", "App.csproj"), `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)

	_, err := scanWorkspace(root)
	if err == nil || !strings.Contains(err.Error(), "--include-ignored") {
		t.Fatalf("err = %v; want a hint to use --include-ignored", err)
	}
	snapshot, err := scanWorkspaceWith(root, workspaceScanOptions{IncludeIgnored: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ParsedProjects) != 1 {
		t.Fatalf("projects = %d", len(snapshot.ParsedProjects))
	}
}

func TestDispatchCLI_ListIncludeIgnored(t *testing.T) {
	isolateGitExcludes(t, "")
	root := t.TempDir()
	initFakeRepo(t, root)
	mustWriteFile(t, filepath.Join(root, ".gitignore"), "samples/\n")
	writeCLIProject(t, filepath.Join(root, "samples"), "App", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
  <ItemGroup><PackageReference Include="Example.Core" Version="1.0.0" /></ItemGroup>
</Project>
`)

	code, _, stderr := runCLIForTest(t, "list", "--project", root, "--format", "json")
	if code != ExitWorkspace || !strings.Contains(stderr, "--include-ignored") {
		t.Fatalf("default list: code = %d, stderr = %q", code, stderr)
	}
	code, stdout, stderr := runCLIForTest(t, "list", "--project", root, "--format", "json", "--include-ignored")
	if code == ExitWorkspace || code == ExitUsage || !strings.Contains(stdout, "Example.Core") {
		t.Fatalf("--include-ignored list: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}
