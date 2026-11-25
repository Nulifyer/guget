package nugetconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDetectCaseInsensitiveRequiresTheNuGetHomeAndWorkingDirectory(t *testing.T) {
	folding := func(dirs ...string) func(string) bool {
		return func(path string) bool {
			for _, dir := range dirs {
				if strings.EqualFold(path, dir) {
					return true
				}
			}
			return false
		}
	}
	exact := func(dirs ...string) func(string) bool {
		return func(path string) bool {
			for _, dir := range dirs {
				if path == dir {
					return true
				}
			}
			return false
		}
	}
	home, work := filepath.FromSlash("/Users/Dev/.nuget"), filepath.FromSlash("/Volumes/Code/Repo")
	both := func(path string) bool {
		return folding(home)(path) || exact(work, filepath.Dir(work), filepath.Dir(filepath.Dir(work)))(path)
	}
	for _, c := range []struct {
		name   string
		paths  []string
		exists func(string) bool
		want   bool
	}{
		{"both ignore case", []string{home, work}, folding(home, work), true},
		{"both exact", []string{home, work}, exact(home, work), false},
		{"working directory exact", []string{home, work}, both, false},
		// A missing NuGet home is judged by its first existing ancestor.
		{"missing home uses its parent", []string{home}, exact(filepath.Dir(home)), false},
		{"missing home under a folding parent", []string{home}, folding(filepath.Dir(home)), true},
		{"nothing to check", nil, exact(), true},
	} {
		if got := detectCaseInsensitive(c.paths, c.exists); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWindowsCountsAsCaseInsensitiveWithoutProbing(t *testing.T) {
	if !caseInsensitive(Environment{GOOS: "windows", LookupEnv: func(string) (string, bool) { return "", false }}) {
		t.Fatal("NuGet treats every Windows file system as case-insensitive")
	}
	if caseInsensitive(Environment{GOOS: "windows", CaseInsensitive: func() bool { return false }}) {
		t.Fatal("an explicit CaseInsensitive must win")
	}
}

func TestCaseInsensitiveFileSystemProbesOnlyNuGetConfig(t *testing.T) {
	f := newFixture(t)
	env := f.env()
	env.CaseInsensitive = func() bool { return true }
	f.write(filepath.Join(f.repo, "nuget.config"), sources(add("lower", "https://lower.test/index.json")))

	settings, err := Load(f.project, env)
	if err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(f.repo, "NuGet.Config")
	if _, err := os.Stat(upper); err != nil {
		// A case-sensitive directory under a NuGet home and working directory
		// that ignore case: NuGet only looks for NuGet.Config.
		if got := sourceNames(settings); len(got) != 0 {
			t.Fatalf("nuget.config must not be read when only NuGet.Config is probed, got %v", got)
		}
		f.write(upper, sources(add("upper", "https://upper.test/index.json")))
		if settings, err = Load(f.project, env); err != nil {
			t.Fatal(err)
		}
		if got := sourceNames(settings); !reflect.DeepEqual(got, []string{"upper"}) {
			t.Fatalf("sources = %v, want NuGet.Config's", got)
		}
	} else if got := sourceNames(settings); !reflect.DeepEqual(got, []string{"lower"}) {
		t.Fatalf("the file system must match NuGet.Config to nuget.config, got %v", got)
	}
	// NuGet reports the name it probed.
	if settings.Files[0].Path != upper {
		t.Fatalf("files = %+v, want %s first", settings.Files, upper)
	}
	for _, path := range CandidatePaths(f.project, env) {
		if base := filepath.Base(path); base != "NuGet.Config" && base != "NuGetDefaults.config" {
			t.Fatalf("candidate %s: only NuGet.Config is probed", path)
		}
	}
}

func TestConfigFolderFollowsTheFileSystemCaseRule(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"nuget.config", "a.config", "_b.config", "c.CONFIG", "d.Config"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	names := func(goos string, ignoreCase bool) []string {
		var got []string
		for _, path := range configsInDir(nil, dir, "NuGet.Config", Environment{GOOS: goos}, ignoreCase) {
			got = append(got, filepath.Base(path))
		}
		return got
	}
	// Linux patterns match *.config and *.Config exactly; a case-sensitive
	// file system excludes only the exact NuGet.Config and orders ordinally.
	if got, want := names("linux", false), []string{"_b.config", "a.config", "d.Config", "nuget.config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("case-sensitive: %v, want %v", got, want)
	}
	// Ignoring case excludes any spelling of NuGet.Config and orders like
	// StringComparer.OrdinalIgnoreCase, which compares upper-case text.
	if got, want := names("linux", true), []string{"a.config", "d.Config", "_b.config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("case-insensitive: %v, want %v", got, want)
	}
	// .NET patterns ignore case on macOS and Windows.
	if got, want := names("darwin", true), []string{"a.config", "c.CONFIG", "d.Config", "_b.config"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("macOS: %v, want %v", got, want)
	}
}
