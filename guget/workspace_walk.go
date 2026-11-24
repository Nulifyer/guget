package main

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/nulifyer/guget/internal/gitignore"
)

// workspaceScanOptions controls which directories workspace discovery visits.
type workspaceScanOptions struct {
	// IncludeIgnored also searches paths that .gitignore rules exclude. The
	// built-in build-output exclusions (bin, obj, node_modules, ...) still apply.
	IncludeIgnored bool
}

// includeIgnoredFlag is the flag name shared by the TUI and CLI commands.
const includeIgnoredFlag = "include-ignored"

// workspaceTree is the result of one directory walk. Every consumer of the
// workspace layout reads it, so one load lists each directory once.
type workspaceTree struct {
	ProjectFiles []string
	WatchedFiles []string
	Unsupported  []unsupportedPackageArtifact
	// GitIgnored counts directories and candidate files skipped because a
	// .gitignore rule excludes them.
	GitIgnored int
}

func isProjectFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csproj", ".fsproj", ".vbproj":
		return true
	}
	return false
}

func unsupportedArtifactReason(path string) string {
	name := filepath.Base(path)
	switch {
	case strings.EqualFold(name, "packages.config"):
		return "packages.config mutation is not supported"
	case projectPackagesConfig(path):
		return "packages.config mutation is not supported"
	case strings.EqualFold(name, "dotnet-tools.json") && strings.EqualFold(filepath.Base(filepath.Dir(path)), ".config"):
		return ".NET tool manifests are outside project package management"
	}
	return ""
}

// projectPackagesConfig reports whether path is packages.<ProjectName>.config
// next to that project, which NuGet also uses as the project's packages.config
// (NuGet BuildTasksUtility.GetPackagesConfigFilePath).
func projectPackagesConfig(path string) bool {
	name := filepath.Base(path)
	lower := strings.ToLower(name)
	if !strings.HasPrefix(lower, "packages.") || !strings.HasSuffix(lower, ".config") || len(name) <= len("packages..config") {
		return false
	}
	project := name[len("packages.") : len(name)-len(".config")]
	for _, extension := range []string{".csproj", ".fsproj", ".vbproj"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), project+extension)); err == nil {
			return true
		}
	}
	return false
}

// walkWorkspace lists project files, watched inputs, and unsupported package
// artifacts beneath root. Built-in exclusions apply to every directory below
// root. Unless opts.IncludeIgnored is set, paths excluded by git ignore rules
// are skipped too, following git's precedence: core.excludesFile, then
// .git/info/exclude, then .gitignore files from the repository root down.
// Tracked files are never treated as ignored.
func walkWorkspace(root string, opts workspaceScanOptions) (*workspaceTree, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	w := &workspaceWalker{opts: opts, tree: &workspaceTree{}}
	var repo *gitRepo
	var rules []gitignore.Rule
	if !opts.IncludeIgnored {
		repo, rules = enclosingGitRepo(root)
	}
	if err := w.walkDir(root, repo, rules, false); err != nil {
		return nil, err
	}
	slices.SortFunc(w.tree.Unsupported, func(a, b unsupportedPackageArtifact) int { return strings.Compare(a.Path, b.Path) })
	return w.tree, nil
}

type workspaceWalker struct {
	opts workspaceScanOptions
	tree *workspaceTree
}

// walkDir lists dir. insideIgnored is set below an ignored directory that the
// walk entered only because it holds tracked files; there, every untracked
// path is ignored, as it is for git.
func (w *workspaceWalker) walkDir(dir string, repo *gitRepo, rules []gitignore.Rule, insideIgnored bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if !w.opts.IncludeIgnored {
		repo, rules = enterGitDir(dir, entries, repo, rules)
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			if shouldSkipProjectDir(entry.Name()) {
				continue
			}
			ignored, tracked := repo.ignores(rules, path, true, insideIgnored)
			if ignored && !tracked {
				w.tree.GitIgnored++
				continue
			}
			if err := w.walkDir(path, repo, rules, ignored); err != nil {
				return err
			}
			continue
		}
		project := isProjectFile(entry.Name())
		watched := isWatchedWorkspaceFile(path)
		reason := unsupportedArtifactReason(path)
		if !project && !watched && reason == "" {
			continue
		}
		if ignored, tracked := repo.ignores(rules, path, false, insideIgnored); ignored && !tracked {
			w.tree.GitIgnored++
			continue
		}
		if project {
			w.tree.ProjectFiles = append(w.tree.ProjectFiles, path)
		}
		if watched {
			w.tree.WatchedFiles = append(w.tree.WatchedFiles, path)
		}
		if reason != "" {
			w.tree.Unsupported = append(w.tree.Unsupported, unsupportedPackageArtifact{Path: path, Reason: reason})
		}
	}
	return nil
}

// enterGitDir applies a directory's own ignore context: a nested repository
// replaces the enclosing rules, and a .gitignore file adds rules for its subtree.
func enterGitDir(dir string, entries []os.DirEntry, repo *gitRepo, rules []gitignore.Rule) (*gitRepo, []gitignore.Rule) {
	hasGit, hasIgnore := false, false
	for _, entry := range entries {
		switch entry.Name() {
		case ".git":
			hasGit = true
		case ".gitignore":
			hasIgnore = !entry.IsDir()
		}
	}
	if hasGit && (repo == nil || repo.root != dir) {
		repo = openGitRepo(dir)
		rules = repo.baseRules
	}
	if hasIgnore && repo != nil {
		rules = appendIgnoreFile(rules, filepath.Join(dir, ".gitignore"), repo.rel(dir))
	}
	return repo, rules
}

func appendIgnoreFile(rules []gitignore.Rule, path, base string) []gitignore.Rule {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logWarn("Couldn't read %s; its ignore rules are not applied: %v", path, err)
		}
		return rules
	}
	parsed := gitignore.Parse(data, base)
	if len(parsed) == 0 {
		return rules
	}
	// Clip so sibling directories never share an appended backing array.
	return append(slices.Clip(rules), parsed...)
}

// enclosingGitRepo finds the repository containing root and the rules that
// apply to root's children from .gitignore files in its ancestors. root's own
// .gitignore is read when the walk lists root.
func enclosingGitRepo(root string) (*gitRepo, []gitignore.Rule) {
	for dir := root; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			repo := openGitRepo(dir)
			rules := repo.baseRules
			if dir == root {
				return repo, rules
			}
			rel := repo.rel(root)
			parts := strings.Split(rel, "/")
			// The repository root and each ancestor strictly above root.
			for i := 0; i < len(parts); i++ {
				base := strings.Join(parts[:i], "/")
				rules = appendIgnoreFile(rules, filepath.Join(dir, filepath.FromSlash(base), ".gitignore"), base)
			}
			return repo, rules
		}
		if filepath.Dir(dir) == dir {
			return nil, nil
		}
	}
}

type gitRepo struct {
	root       string
	gitDir     string
	ignoreCase bool
	baseRules  []gitignore.Rule
}

func openGitRepo(root string) *gitRepo {
	gitDir, commonDir := resolveGitDir(root)
	config := readGitRepoConfig(root)
	repo := &gitRepo{root: root, gitDir: gitDir, ignoreCase: config.IgnoreCase}
	if config.ExcludesFile != "" {
		repo.baseRules = appendIgnoreFile(repo.baseRules, config.ExcludesFile, "")
	}
	if commonDir != "" {
		repo.baseRules = appendIgnoreFile(repo.baseRules, filepath.Join(commonDir, "info", "exclude"), "")
	}
	return repo
}

// resolveGitDir follows a ".git" file (worktrees and submodules) to the
// repository directory, and "commondir" to the directory holding info/exclude.
func resolveGitDir(root string) (gitDir, commonDir string) {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", ""
	}
	gitDir = dotGit
	if !info.IsDir() {
		data, err := os.ReadFile(dotGit)
		if err != nil {
			return "", ""
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		if !ok {
			return "", ""
		}
		gitDir = resolveRelative(root, strings.TrimSpace(target))
	}
	commonDir = gitDir
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = resolveRelative(gitDir, strings.TrimSpace(string(data)))
	}
	return gitDir, commonDir
}

func resolveRelative(base, path string) string {
	path = filepath.FromSlash(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
}

func (r *gitRepo) rel(path string) string {
	rel, err := filepath.Rel(r.root, path)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ignores reports whether an ignore rule (or an ignored parent) covers path,
// and whether git tracks it anyway: a tracked file, or a directory holding
// one. Ignore rules never apply to tracked files. A nil repository ignores
// nothing, so walks outside a repository apply only the built-in exclusions.
func (r *gitRepo) ignores(rules []gitignore.Rule, path string, isDir, insideIgnored bool) (ignored, tracked bool) {
	if r == nil {
		return false, false
	}
	rel := r.rel(path)
	if rel == "" || !insideIgnored && !gitignore.Ignored(rules, rel, isDir, r.ignoreCase) {
		return false, false
	}
	if r.ignoreCase {
		rel = strings.ToLower(rel)
	}
	return true, trackedGitPaths(r).contains(rel, isDir)
}

// gitRepoConfig is the part of a repository's git configuration that changes
// which paths git ignores.
type gitRepoConfig struct {
	// ExcludesFile is the user's global ignore file (core.excludesFile).
	ExcludesFile string
	// IgnoreCase matches ignore rules case-insensitively (core.ignoreCase).
	IgnoreCase bool
}

// readGitRepoConfig returns the configuration for the repository at root,
// asking git once per repository per process. Tests replace it to keep results
// independent of the developer machine.
var readGitRepoConfig = func(root string) gitRepoConfig {
	repoConfigMu.Lock()
	defer repoConfigMu.Unlock()
	if config, ok := repoConfigByRoot[root]; ok {
		return config
	}
	config := loadGitRepoConfig(root)
	repoConfigByRoot[root] = config
	return config
}

var (
	repoConfigMu     sync.Mutex
	repoConfigByRoot = map[string]gitRepoConfig{}
)

func loadGitRepoConfig(root string) gitRepoConfig {
	var config gitRepoConfig
	if _, err := exec.LookPath("git"); err == nil {
		// One process for both keys. --path expands "~/" in core.excludesFile.
		// Exit status 1 means neither key is set.
		out, _ := exec.Command("git", "-C", root, "config", "-z", "--path", "--get-regexp", `^core\.(excludesfile|ignorecase)$`).Output()
		config = parseGitRepoConfig(root, out)
	}
	if config.ExcludesFile == "" {
		// git's default when core.excludesFile is unset.
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			config.ExcludesFile = filepath.Join(xdg, "git", "ignore")
		} else if home, err := os.UserHomeDir(); err == nil {
			config.ExcludesFile = filepath.Join(home, ".config", "git", "ignore")
		}
	}
	return config
}

// parseGitRepoConfig reads "git config -z --get-regexp" output: entries end in
// NUL, and a newline separates each key from its value. A key without a value
// is a boolean true. Later entries override earlier ones, as in git.
func parseGitRepoConfig(root string, out []byte) gitRepoConfig {
	var config gitRepoConfig
	for _, entry := range strings.Split(string(out), "\x00") {
		key, value, hasValue := strings.Cut(entry, "\n")
		switch strings.ToLower(key) {
		case "core.excludesfile":
			if value != "" {
				config.ExcludesFile = resolveRelative(root, value)
			}
		case "core.ignorecase":
			config.IgnoreCase = !hasValue || gitConfigBool(value)
		}
	}
	return config
}

func gitConfigBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// gitTrackedSet holds the index's tracked files and their parent directories.
type gitTrackedSet struct {
	files map[string]struct{}
	dirs  map[string]struct{}
}

func (s *gitTrackedSet) contains(rel string, isDir bool) bool {
	if s == nil {
		return false
	}
	if isDir {
		_, ok := s.dirs[rel]
		return ok
	}
	_, ok := s.files[rel]
	return ok
}

type trackedCacheEntry struct {
	size    int64
	modTime int64
	set     *gitTrackedSet
}

var (
	trackedMu    sync.Mutex
	trackedByDir = map[string]trackedCacheEntry{}
)

// trackedGitPaths lists tracked paths only once an ignore rule matches, and
// reuses the list until the index changes. Without git it returns nil, so
// ignore rules apply to every path.
func trackedGitPaths(repo *gitRepo) *gitTrackedSet {
	if repo.gitDir == "" {
		return nil
	}
	info, err := os.Stat(filepath.Join(repo.gitDir, "index"))
	if err != nil {
		return nil
	}
	trackedMu.Lock()
	defer trackedMu.Unlock()
	if entry, ok := trackedByDir[repo.gitDir]; ok && entry.size == info.Size() && entry.modTime == info.ModTime().UnixNano() {
		return entry.set
	}
	set := listTrackedPaths(repo.root, repo.ignoreCase)
	trackedByDir[repo.gitDir] = trackedCacheEntry{size: info.Size(), modTime: info.ModTime().UnixNano(), set: set}
	return set
}

// listTrackedPaths lower-cases the paths when fold is set, so lookups match the
// case-insensitive ignore rules.
func listTrackedPaths(root string, fold bool) *gitTrackedSet {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached").Output()
	if err != nil {
		logDebug("Couldn't list tracked files in %s; ignore rules apply to tracked files too: %v", root, err)
		return nil
	}
	set := &gitTrackedSet{files: map[string]struct{}{}, dirs: map[string]struct{}{}}
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		rel := string(raw)
		if fold {
			rel = strings.ToLower(rel)
		}
		set.files[rel] = struct{}{}
		for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
			if _, seen := set.dirs[dir]; seen {
				break
			}
			set.dirs[dir] = struct{}{}
		}
	}
	return set
}
