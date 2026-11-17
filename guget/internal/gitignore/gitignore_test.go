package gitignore

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIgnored(t *testing.T) {
	cases := []struct {
		name     string
		patterns string
		base     string
		path     string
		isDir    bool
		want     bool
	}{
		{"basename at any depth", "*.log", "", "a/b/debug.log", false, true},
		{"basename miss", "*.log", "", "a/b/debug.txt", false, false},
		{"directory-only matches directory", "out/", "", "src/out", true, true},
		{"directory-only skips file", "out/", "", "src/out", false, false},
		{"leading slash anchors", "/out", "", "src/out", true, false},
		{"leading slash matches root entry", "/out", "", "out", true, true},
		{"inner slash anchors", "a/out", "", "x/a/out", true, false},
		{"inner slash matches from base", "a/out", "", "a/out", true, true},
		{"leading double star", "**/gen", "", "x/y/gen", true, true},
		{"leading double star at root", "**/gen", "", "gen", true, true},
		{"middle double star zero dirs", "a/**/b", "", "a/b", true, true},
		{"middle double star many dirs", "a/**/b", "", "a/x/y/b", true, true},
		{"trailing double star contents", "a/**", "", "a/x", false, true},
		{"trailing double star matches directory", "a/**", "", "a", true, true},
		{"trailing double star skips same-named file", "a/**", "", "a", false, false},
		{"star stays in one segment", "a/*", "", "a/x/y", false, false},
		{"question mark", "file?.txt", "", "file1.txt", false, true},
		{"negated class", "file[!0-9].txt", "", "fileA.txt", false, true},
		{"negated class miss", "file[!0-9].txt", "", "file1.txt", false, false},
		{"comment", "# out", "", "# out", false, false},
		{"escaped hash", `\#out`, "", "#out", false, true},
		{"escaped bang", `\!out`, "", "!out", false, true},
		{"trailing spaces trimmed", "out   ", "", "out", false, true},
		{"escaped trailing space kept", `out\ `, "", "out ", false, true},
		{"last match wins", "*.log\n!keep.log", "", "keep.log", false, false},
		{"negation then exclusion", "!keep.log\n*.log", "", "keep.log", false, true},
		{"nested base scopes rule", "out", "sub", "other/out", true, false},
		{"nested base applies below", "out", "sub", "sub/x/out", true, true},
		{"nested anchored rule", "/out", "sub", "sub/out", true, true},
		{"nested anchored rule miss", "/out", "sub", "sub/x/out", true, false},
		{"CRLF line", "out\r\n", "", "out", true, true},
		{"case sensitive", "Out", "", "out", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := Parse([]byte(tc.patterns), tc.base)
			if got := Ignored(rules, tc.path, tc.isDir, false); got != tc.want {
				t.Fatalf("Ignored(%q, %q, dir=%v) = %v, want %v", tc.patterns, tc.path, tc.isDir, got, tc.want)
			}
		})
	}
}

func TestLaterSourcesOverrideEarlierOnes(t *testing.T) {
	rules := Parse([]byte("*.csproj\n"), "")
	rules = append(rules, Parse([]byte("!App.csproj\n"), "src")...)
	if Ignored(rules, "src/App.csproj", false, false) {
		t.Fatal("deeper negation did not re-include the file")
	}
	if !Ignored(rules, "tools/App.csproj", false, false) {
		t.Fatal("deeper negation leaked outside its directory")
	}
}

func TestIgnoredCaseInsensitive(t *testing.T) {
	rules := Parse([]byte("Debug/\n/Generated/*.Props\n[A-C]x\n"), "Src")
	cases := []struct {
		path  string
		isDir bool
	}{
		{"src/debug", true},
		{"SRC/x/DEBUG", true},
		{"src/generated/a.props", false},
		{"src/bx", false},
	}
	for _, tc := range cases {
		if Ignored(rules, tc.path, tc.isDir, false) {
			t.Errorf("%q matched case-sensitively", tc.path)
		}
		if !Ignored(rules, tc.path, tc.isDir, true) {
			t.Errorf("%q did not match case-insensitively", tc.path)
		}
	}
}

// TestMatchesGitCheckIgnore compares the matcher with git itself so pattern
// semantics cannot drift from the tool users already rely on. It runs once
// with each core.ignoreCase setting.
func TestMatchesGitCheckIgnore(t *testing.T) {
	for _, ignoreCase := range []bool{false, true} {
		t.Run(fmt.Sprintf("ignoreCase=%v", ignoreCase), func(t *testing.T) {
			compareWithGitCheckIgnore(t, ignoreCase)
		})
	}
}

func compareWithGitCheckIgnore(t *testing.T, ignoreCase bool) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	rootRules := strings.Join([]string{
		"# build results",
		"[Bb]in/",
		"*.log",
		"!important.log",
		"/rooted",
		"docs/*.tmp",
		"**/generated/",
		"a/**/z",
		"cache/**",
		"file[!0-9].txt",
		`spaced\ `,
		".local-*/",
	}, "\n")
	subRules := "!keep.tmp\n/anchored\nnested\n"
	git(t, root, "init", "-q")
	write(t, filepath.Join(root, ".gitignore"), rootRules)
	write(t, filepath.Join(root, "sub", ".gitignore"), subRules)

	paths := []struct {
		path  string
		isDir bool
	}{
		{"bin", true}, {"Bin", true}, {"src/bin", true}, {"bin", false},
		{"x.log", false}, {"deep/x.log", false}, {"important.log", false},
		{"rooted", false}, {"sub/rooted", false},
		{"docs/a.tmp", false}, {"other/docs/a.tmp", false},
		{"generated", true}, {"p/q/generated", true}, {"generated", false},
		{"a/z", false}, {"a/b/c/z", false}, {"b/a/z", false},
		{"cache/x", false}, {"cache/x/y", false}, {"cache", true}, {"cache", false},
		{"fileA.txt", false}, {"file1.txt", false},
		{"spaced ", false}, {"spaced", false},
		{".local-results", true}, {".local-tests", true}, {".local", true},
		{"sub/keep.tmp", false}, {"sub/anchored", false}, {"sub/x/anchored", false},
		{"sub/nested", true}, {"sub/x/nested", true}, {"nested", true},
		// Case variants of paths ruled by the root .gitignore. Variants under
		// "sub" are left out: git looks up SUB/.gitignore on disk, which a
		// case-sensitive filesystem does not find, while the walker always
		// passes directory names exactly as listed.
		{"BIN", true}, {"X.LOG", false}, {"DOCS/a.tmp", false}, {"Rooted", false},
		{"GENERATED", true}, {"Cache/X", false}, {"FILEa.TXT", false},
	}
	rules := Parse([]byte(rootRules), "")
	rules = append(rules, Parse([]byte(subRules), "sub")...)

	var input bytes.Buffer
	for _, p := range paths {
		input.WriteString(p.path)
		if p.isDir {
			input.WriteString("/")
		}
		input.WriteByte(0)
	}
	cmd := exec.Command("git", "-c", fmt.Sprintf("core.ignoreCase=%v", ignoreCase), "check-ignore", "--no-index", "-z", "--stdin", "-v", "-n")
	cmd.Dir = root
	cmd.Env = isolatedGitEnv(t)
	cmd.Stdin = &input
	out, err := cmd.Output()
	if err != nil && cmd.ProcessState.ExitCode() > 1 {
		t.Fatalf("git check-ignore: %v", err)
	}
	// -v -n -z emits source, line, pattern, and path for every input path. A
	// path is ignored when a pattern matched and that pattern is not a negation.
	fields := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if len(fields) != len(paths)*4 {
		t.Fatalf("unexpected check-ignore output %q", out)
	}
	for i, p := range paths {
		pattern := fields[i*4+2]
		want := pattern != "" && !strings.HasPrefix(pattern, "!")
		if got := Ignored(rules, p.path, p.isDir, ignoreCase); got != want {
			t.Errorf("%q (dir=%v): matcher=%v git=%v (git pattern %q)", p.path, p.isDir, got, want, pattern)
		}
	}
}

func isolatedGitEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(home, "none"))
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = isolatedGitEnv(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
