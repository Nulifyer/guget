package userconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeEnv(goos string, vars map[string]string) Environment {
	return Environment{GOOS: goos, LookupEnv: func(name string) (string, bool) {
		value, ok := vars[name]
		return value, ok
	}}
}

func testKeys() []Key {
	return []Key{
		{Name: "theme", Default: "auto", Validate: func(value string) (string, error) {
			lower := strings.ToLower(value)
			if lower == "bad" {
				return "", errors.New("not a theme")
			}
			return lower, nil
		}},
		{Name: "mouse", Kind: Bool, Default: "true"},
		{Name: "color", Kind: Bool, Default: "true", Aliases: []EnvAlias{{Name: "NO_COLOR", Map: func(string) (string, bool) { return "false", true }}}},
		{Name: "update-check", Kind: Bool, Default: "true", Aliases: []EnvAlias{{Name: "GUGET_NO_UPDATE_CHECK", Map: func(value string) (string, bool) { return "false", value == "1" }}}},
		{Name: "label", Default: ""},
	}
}

func keyNamed(t *testing.T, name string) Key {
	t.Helper()
	for _, key := range testKeys() {
		if key.Name == name {
			return key
		}
	}
	t.Fatalf("no test key %q", name)
	return Key{}
}

func TestPathResolution(t *testing.T) {
	join := filepath.Join
	// IsAbs follows the host, so build an absolute XDG path the host accepts.
	xdg := filepath.Join(filepath.VolumeName(os.TempDir())+string(filepath.Separator), "xdg")
	tests := []struct {
		name string
		goos string
		vars map[string]string
		want string
		err  string
	}{
		{"explicit file wins", "linux", map[string]string{"GUGET_CONFIG": "/etc/guget.toml", "XDG_CONFIG_HOME": "/xdg", "HOME": "/home/u"}, "/etc/guget.toml", ""},
		{"explicit file wins on Windows", "windows", map[string]string{"GUGET_CONFIG": `C:\guget.toml`, "APPDATA": `C:\AppData`}, `C:\guget.toml`, ""},
		{"XDG config home", "linux", map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": "/home/u"}, join(xdg, "guget", "config.toml"), ""},
		{"relative XDG is ignored", "linux", map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": "/home/u"}, join("/home/u", ".config", "guget", "config.toml"), ""},
		{"empty XDG is ignored", "linux", map[string]string{"XDG_CONFIG_HOME": "", "HOME": "/home/u"}, join("/home/u", ".config", "guget", "config.toml"), ""},
		{"macOS uses .config", "darwin", map[string]string{"HOME": "/Users/u"}, join("/Users/u", ".config", "guget", "config.toml"), ""},
		{"BSD uses .config", "freebsd", map[string]string{"HOME": "/home/u"}, join("/home/u", ".config", "guget", "config.toml"), ""},
		{"Windows APPDATA", "windows", map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`, "XDG_CONFIG_HOME": "/xdg", "HOME": "/home/u"}, join(`C:\Users\u\AppData\Roaming`, "guget", "config.toml"), ""},
		{"no HOME", "linux", map[string]string{}, "", "HOME is not set"},
		{"no APPDATA", "windows", map[string]string{"HOME": "/home/u"}, "", "APPDATA is not set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Path(fakeEnv(tt.goos, tt.vars))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) || !strings.Contains(err.Error(), PathVariable) {
					t.Fatalf("Path() = %q, %v; want error mentioning %q and %s", got, err, tt.err, PathVariable)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Path() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func loadFile(t *testing.T, content string, vars map[string]string) Values {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	all := map[string]string{"GUGET_CONFIG": path}
	for name, value := range vars {
		all[name] = value
	}
	return Load(testKeys(), fakeEnv("linux", all))
}

func assertSetting(t *testing.T, values Values, name, value string, source Source) {
	t.Helper()
	setting, ok := values.Get(name)
	if !ok || setting.Value != value || setting.Source != source {
		t.Fatalf("%s = %q (%s), want %q (%s); warnings: %q", name, setting.Value, setting.Source, value, source, values.Warnings)
	}
}

func assertWarnings(t *testing.T, values Values, want ...string) {
	t.Helper()
	if len(values.Warnings) != len(want) {
		t.Fatalf("warnings = %q, want %d matching %q", values.Warnings, len(want), want)
	}
	for i, fragment := range want {
		if !strings.Contains(values.Warnings[i], fragment) {
			t.Fatalf("warning %d = %q, want it to contain %q", i, values.Warnings[i], fragment)
		}
	}
}

func TestLoadParsesCommentsStringsAndBooleans(t *testing.T) {
	values := loadFile(t, "\xEF\xBB\xBF# guget settings\r\n\r\n  theme = \"NORD\"   # trailing comment\r\n\tmouse=false#no space\r\nlabel = \"a \\\"b\\\" \\\\ c\\t\\u00e9\\U0001F600 # not a comment\"\r\n", nil)
	assertWarnings(t, values)
	assertSetting(t, values, "theme", "nord", SourceFile)
	assertSetting(t, values, "mouse", "false", SourceFile)
	assertSetting(t, values, "label", "a \"b\" \\ c\té😀 # not a comment", SourceFile)
	assertSetting(t, values, "color", "true", SourceDefault)
}

func TestLoadAcceptsLiteralStrings(t *testing.T) {
	values := loadFile(t, `label = 'C:\no\escapes "here"'`, nil)
	assertWarnings(t, values)
	assertSetting(t, values, "label", `C:\no\escapes "here"`, SourceFile)
}

func TestLoadWarnsAboutBadLinesAndUsesDefaults(t *testing.T) {
	content := strings.Join([]string{
		`theme = nord`,              // 1 unquoted string
		`mouse = "false"`,           // 2 quoted boolean
		`color = yes`,               // 3 not a TOML boolean
		`label = "open`,             // 4 unterminated
		`label = "x" trailing`,      // 5 text after value
		`label = "\q"`,              // 6 bad escape
		`just text`,                 // 7 no equals sign
		`a.b = "x"`,                 // 8 dotted key
		`unknown = "x"`,             // 9 unknown key
		`theme = "bad"`,             // 10 rejected by validator
		`label =`,                   // 11 missing value
		`label = """multi"""`,       // 12 multi-line string
		"label = \"tab\there\"",     // 13 raw tab is allowed
		"label = \"bell\x07\"",      // 14 raw control character
		`[ ]`,                       // 15 empty table header
		`truely = true`,             // 16 unknown key that starts with true
		`update-check = truevalue`,  // 17 not a boolean
		`"theme" = "dracula"`,       // 18 quoted key is the same key
		`label = "\uD800"`,          // 19 surrogate escape
		`label = "\u00"`,            // 20 short escape
		`label = 'literal'  # okay`, // 21
	}, "\n")
	values := loadFile(t, content, nil)
	assertWarnings(t, values,
		":1: theme: value must be a quoted string",
		":2: mouse expects true or false without quotes",
		":3: color: value must be a quoted string",
		":4: label: unterminated string",
		":5: label is already set on line 4",
		":5: label: unexpected text after the value",
		":6: label is already set on line 5",
		`:6: label: invalid escape \q`,
		":7: expected = after just",
		":8: dotted keys are not supported",
		`:9: unknown setting "unknown"`,
		`:10: theme is already set on line 1`,
		`:10: invalid value "bad" for theme: not a theme`,
		":11: label is already set on line 6",
		":11: label: missing value",
		":12: label is already set on line 11",
		":12: label: multi-line strings are not supported",
		":13: label is already set on line 12",
		":14: label is already set on line 13",
		":14: label: control characters must be escaped",
		":15: invalid table header",
		`:16: unknown setting "truely"`,
		":17: update-check: value must be a quoted string",
		":18: theme is already set on line 10",
		":19: label is already set on line 14",
		`:19: label: invalid unicode escape \uD800`,
		":20: label is already set on line 19",
		":20: label: incomplete unicode escape",
		":21: label is already set on line 20",
	)
	assertSetting(t, values, "theme", "dracula", SourceFile)
	assertSetting(t, values, "mouse", "true", SourceDefault)
	assertSetting(t, values, "color", "true", SourceDefault)
	assertSetting(t, values, "update-check", "true", SourceDefault)
	assertSetting(t, values, "label", "literal", SourceFile)
}

func TestLoadDuplicateKeyUsesLastValueWithWarning(t *testing.T) {
	values := loadFile(t, "theme = \"nord\"\ntheme = \"gruvbox\"\n", nil)
	assertWarnings(t, values, "config.toml:2: theme is already set on line 1; using this later value (TOML readers reject duplicate keys)")
	assertSetting(t, values, "theme", "gruvbox", SourceFile)

	// An invalid last value discards the earlier one too: the last line wins.
	values = loadFile(t, "theme = \"nord\"\ntheme = \"bad\"\n", nil)
	assertSetting(t, values, "theme", "auto", SourceDefault)
}

func TestLoadIgnoresTables(t *testing.T) {
	values := loadFile(t, "mouse = false\n[tui]\ntheme = \"nord\"\n[[more]]\ncolor = false\n", nil)
	assertWarnings(t, values,
		":2: tables are not supported; ignoring [tui] and the keys after it",
		":4: tables are not supported; ignoring [more] and the keys after it",
	)
	assertSetting(t, values, "mouse", "false", SourceFile)
	assertSetting(t, values, "theme", "auto", SourceDefault)
	assertSetting(t, values, "color", "true", SourceDefault)
}

func TestLoadPrecedenceEnvironmentFileDefault(t *testing.T) {
	file := "theme = \"nord\"\nmouse = false\ncolor = true\nupdate-check = true\n"
	values := loadFile(t, file, map[string]string{
		"GUGET_THEME":           "Dracula",
		"GUGET_MOUSE":           "",  // empty means unset
		"NO_COLOR":              "0", // any non-empty value counts
		"GUGET_NO_UPDATE_CHECK": "1",
	})
	assertWarnings(t, values)
	assertSetting(t, values, "theme", "dracula", SourceEnv)
	assertSetting(t, values, "mouse", "false", SourceFile)
	assertSetting(t, values, "color", "false", SourceEnv)
	assertSetting(t, values, "update-check", "false", SourceEnv)
	assertSetting(t, values, "label", "", SourceDefault)
	if setting, _ := values.Get("color"); setting.EnvVar != "NO_COLOR" {
		t.Fatalf("color EnvVar = %q", setting.EnvVar)
	}
}

func TestLoadCanonicalVariableBeatsAlias(t *testing.T) {
	values := loadFile(t, "", map[string]string{"GUGET_COLOR": "yes", "NO_COLOR": "1", "GUGET_UPDATE_CHECK": "on", "GUGET_NO_UPDATE_CHECK": "1"})
	assertSetting(t, values, "color", "true", SourceEnv)
	assertSetting(t, values, "update-check", "true", SourceEnv)
	if setting, _ := values.Get("color"); setting.EnvVar != "GUGET_COLOR" {
		t.Fatalf("color EnvVar = %q", setting.EnvVar)
	}
}

func TestLoadAliasThatMeansNothingIsIgnored(t *testing.T) {
	values := loadFile(t, "update-check = false\n", map[string]string{"GUGET_NO_UPDATE_CHECK": "0"})
	assertSetting(t, values, "update-check", "false", SourceFile)
}

func TestLoadInvalidEnvironmentFallsBackToFile(t *testing.T) {
	values := loadFile(t, "theme = \"nord\"\nmouse = false\n", map[string]string{"GUGET_THEME": "bad", "GUGET_MOUSE": "maybe"})
	assertWarnings(t, values,
		`GUGET_THEME: invalid value "bad" for theme: not a theme; ignoring it`,
		`GUGET_MOUSE: invalid value "maybe" for mouse: expected true or false; ignoring it`,
	)
	assertSetting(t, values, "theme", "nord", SourceFile)
	assertSetting(t, values, "mouse", "false", SourceFile)
}

func TestLoadMissingFileIsSilent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "config.toml")
	values := Load(testKeys(), fakeEnv("linux", map[string]string{"GUGET_CONFIG": path}))
	assertWarnings(t, values)
	if values.Path != path {
		t.Fatalf("Path = %q", values.Path)
	}
	assertSetting(t, values, "theme", "auto", SourceDefault)
}

func TestLoadUnreadableFileWarns(t *testing.T) {
	dir := t.TempDir() // a directory cannot be read as a file
	values := Load(testKeys(), fakeEnv("linux", map[string]string{"GUGET_CONFIG": dir, "GUGET_THEME": "nord"}))
	assertWarnings(t, values, "cannot read settings file")
	assertSetting(t, values, "theme", "nord", SourceEnv)
	assertSetting(t, values, "mouse", "true", SourceDefault)
}

func TestLoadWithoutLocationStillUsesEnvironment(t *testing.T) {
	values := Load(testKeys(), fakeEnv("linux", map[string]string{"GUGET_THEME": "nord"}))
	assertWarnings(t, values, "HOME is not set")
	if values.Path != "" {
		t.Fatalf("Path = %q", values.Path)
	}
	assertSetting(t, values, "theme", "nord", SourceEnv)
}

func TestKeyEnvName(t *testing.T) {
	if got := (Key{Name: "update-target"}).EnvName(); got != "GUGET_UPDATE_TARGET" {
		t.Fatalf("EnvName() = %q", got)
	}
}

func writeAndSet(t *testing.T, content string, edit func(path string) error) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := edit(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSetPreservesCommentsAndOtherLines(t *testing.T) {
	tests := []struct {
		name, key, value, content, want string
	}{
		{"replace keeps trailing comment", "theme", "Nord",
			"# mine\ntheme   =   \"dracula\"   # favorite\nmouse = false\n",
			"# mine\ntheme   =   \"nord\"   # favorite\nmouse = false\n"},
		{"replace compact line", "theme", "nord", "theme=\"x\"\n", "theme=\"nord\"\n"},
		{"replace invalid value before comment", "theme", "nord", "theme = nord yes # c\n", "theme = \"nord\" # c\n"},
		{"replace missing value", "theme", "nord", "theme =\n", "theme = \"nord\"\n"},
		{"replace missing value before comment", "theme", "nord", "theme =# c\n", "theme = \"nord\" # c\n"},
		{"replace boolean", "mouse", "0", "mouse = true\n", "mouse = false\n"},
		{"replace quoted key", "theme", "nord", "\"theme\" = 'x'\n", "\"theme\" = \"nord\"\n"},
		{"duplicates collapse to the last line", "theme", "nord",
			"theme = \"a\"\n# keep\ntheme = \"b\"\n", "# keep\ntheme = \"nord\"\n"},
		{"append to file without final newline", "mouse", "false", "# only a comment", "# only a comment\nmouse = false\n"},
		{"append keeps CRLF", "mouse", "false", "theme = \"a\"\r\n", "theme = \"a\"\r\nmouse = false\r\n"},
		{"replace keeps CRLF", "theme", "b", "theme = \"a\"\r\nmouse = true\r\n", "theme = \"b\"\r\nmouse = true\r\n"},
		{"insert before first table", "mouse", "false",
			"theme = \"a\"\n\n[tui]\nmouse = true\n", "theme = \"a\"\n\nmouse = false\n[tui]\nmouse = true\n"},
		{"escape string values", "label", "a \"b\" \\ \n\x01", "", "label = \"a \\\"b\\\" \\\\ \\n\\u0001\"\n"},
		{"keep byte order mark", "theme", "b", "\xEF\xBB\xBFtheme = \"a\"\n", "\xEF\xBB\xBFtheme = \"b\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := keyNamed(t, tt.key)
			got := writeAndSet(t, tt.content, func(path string) error { return Set(path, key, tt.value) })
			if got != tt.want {
				t.Fatalf("Set() wrote\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestSetValuesRoundTrip(t *testing.T) {
	for _, value := range []string{"", "plain", `quote " and \ backslash`, "tab\tnewline\nreturn\r", "unicode é😀", "#hash", "\x7f\x1f"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := Set(path, keyNamed(t, "label"), value); err != nil {
			t.Fatal(err)
		}
		values := Load(testKeys(), fakeEnv("linux", map[string]string{"GUGET_CONFIG": path}))
		assertWarnings(t, values)
		assertSetting(t, values, "label", value, SourceFile)
	}
}

func TestSetRejectsInvalidValueWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := Set(path, keyNamed(t, "theme"), "bad"); err == nil || !strings.Contains(err.Error(), "not a theme") {
		t.Fatalf("Set() error = %v", err)
	}
	if err := Set(path, keyNamed(t, "mouse"), "maybe"); err == nil {
		t.Fatal("Set() accepted a non-boolean")
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid Set created the directory: %v", err)
	}
}

func TestSetCreatesPrivateFileAndDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guget", "nested", "config.toml")
	if err := Set(path, keyNamed(t, "theme"), "nord"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "theme = \"nord\"\n" {
		t.Fatalf("file = %q, %v", data, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for target, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700, filepath.Dir(filepath.Dir(path)): 0o700} {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", target, got, want)
		}
	}
}

func TestSetKeepsExistingPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("theme = \"a\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, keyNamed(t, "theme"), "b"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestSetWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# managed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Set(link, keyNamed(t, "mouse"), "false"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "# managed\nmouse = false\n" {
		t.Fatalf("target = %q, %v", data, err)
	}
}

func TestUnsetRemovesOnlyTheKeyLines(t *testing.T) {
	content := "# header\ntheme = \"a\"  # note\nmouse = false\ntheme = \"b\"\n[tui]\ntheme = \"c\"\n"
	got := writeAndSet(t, content, func(path string) error { return Unset(path, keyNamed(t, "theme")) })
	if want := "# header\nmouse = false\n[tui]\ntheme = \"c\"\n"; got != want {
		t.Fatalf("Unset() wrote %q, want %q", got, want)
	}
}

func TestUnsetWithoutFileOrKeyDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "config.toml")
	if err := Unset(path, keyNamed(t, "theme")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Unset created the directory: %v", err)
	}

	path = filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("mouse = false"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if err := Unset(path, keyNamed(t, "theme")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) {
		t.Fatal("Unset rewrote a file that did not set the key")
	}
}
