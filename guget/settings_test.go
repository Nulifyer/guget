package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useSettingsFile points GUGET_CONFIG at a temporary file holding content;
// an empty content leaves the file absent.
func useSettingsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guget", "config.toml")
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GUGET_CONFIG", path)
	return path
}

func TestSettingsApplyAsFlagDefaults(t *testing.T) {
	useSettingsFile(t, strings.Join([]string{
		`theme = "nord"`,
		`sort = "name:desc"`,
		`mouse = false`,
		`update-check = false`,
		`verbosity = "debug"`,
		`color = false`,
	}, "\n"))
	flags, _ := parseRegisteredCLIForTest(t, "--project", ".")
	assertBuiltFlags(t, flags, BuiltFlags{
		ProjectDir: ".", Theme: "nord", SortBy: "name:desc", NoMouse: true,
		NoUpdateCheck: true, Verbosity: "debug", NoColor: true,
	})
}

func TestSettingsPrecedenceFlagEnvironmentFileDefault(t *testing.T) {
	useSettingsFile(t, "theme = \"nord\"\nsort = \"name\"\nverbosity = \"info\"\n")
	t.Setenv("GUGET_THEME", "gruvbox")
	t.Setenv("LOG_LEVEL", "error")

	flags, _ := parseRegisteredCLIForTest(t, "--project", ".")
	if flags.Theme != "gruvbox" || flags.SortBy != "name" || flags.Verbosity != "error" || flags.NoColor {
		t.Fatalf("environment and file defaults not applied: %+v", flags)
	}
	t.Setenv("GUGET_VERBOSITY", "trace") // the canonical variable beats LOG_LEVEL
	flags, _ = parseRegisteredCLIForTest(t, "--project", ".", "--theme", "dracula", "--sort-by", "current:asc")
	if flags.Theme != "dracula" || flags.SortBy != "current:asc" || flags.Verbosity != "trace" {
		t.Fatalf("flags did not override settings: %+v", flags)
	}
}

func TestSettingsLegacyVariablesStillWork(t *testing.T) {
	useSettingsFile(t, "")
	t.Setenv("NO_COLOR", "anything")
	t.Setenv("GUGET_NO_UPDATE_CHECK", "1")
	flags, _ := parseRegisteredCLIForTest(t, "--project", ".")
	if !flags.NoColor || !flags.NoUpdateCheck || flags.NoMouse {
		t.Fatalf("legacy variables not applied: %+v", flags)
	}
}

func TestSettingsInvalidValuesWarnAndUseDefaults(t *testing.T) {
	path := useSettingsFile(t, "theme = \"neon\"\nsort = \"size\"\nverbosity = \"loud\"\nupdate-target = \"newest\"\nmystery = true\n")
	settings := effectiveSettings()
	if settings.Theme != "auto" || settings.Sort != "status:asc" || settings.Verbosity != "warn" || settings.UpdateTarget != "compatible-stable" {
		t.Fatalf("invalid values were not replaced by defaults: %+v", settings)
	}
	want := []string{
		path + `:1: invalid value "neon" for theme: expected one of auto,`,
		path + `:2: invalid value "size" for sort: invalid sort mode: size`,
		path + `:3: invalid value "loud" for verbosity`,
		path + `:4: invalid value "newest" for update-target: expected one of compatible-stable, compatible, stable`,
		path + `:5: unknown setting "mystery"`,
	}
	if len(settings.Values.Warnings) != len(want) {
		t.Fatalf("warnings = %q", settings.Values.Warnings)
	}
	for i, prefix := range want {
		if !strings.HasPrefix(settings.Values.Warnings[i], prefix) {
			t.Fatalf("warning %d = %q, want prefix %q", i, settings.Values.Warnings[i], prefix)
		}
	}
	flags, _ := parseRegisteredCLIForTest(t, "--project", ".")
	if flags.Theme != "auto" || flags.SortBy != "status:asc" {
		t.Fatalf("startup did not fall back to defaults: %+v", flags)
	}
}

func TestEffectiveSettingsExposeUpdateTarget(t *testing.T) {
	useSettingsFile(t, "update-target = \"Stable\"\n")
	if got := effectiveSettings().UpdateTarget; got != "stable" {
		t.Fatalf("UpdateTarget = %q", got)
	}
	t.Setenv("GUGET_UPDATE_TARGET", "compatible")
	if got := effectiveSettings().UpdateTarget; got != "compatible" {
		t.Fatalf("UpdateTarget = %q", got)
	}
}

func runConfigCLI(t *testing.T, args ...string) (ExitCode, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := dispatchCLI(context.Background(), append([]string{"config"}, args...), strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestConfigCommandPathGetSetUnset(t *testing.T) {
	path := useSettingsFile(t, "")
	if code, out, errOut := runConfigCLI(t, "path"); code != ExitSuccess || out != path+"\n" || errOut != "" {
		t.Fatalf("path = %d %q %q", code, out, errOut)
	}
	if code, out, _ := runConfigCLI(t, "get", "theme"); code != ExitSuccess || out != "auto\n" {
		t.Fatalf("get default = %d %q", code, out)
	}
	if code, out, errOut := runConfigCLI(t, "set", "theme", "Nord"); code != ExitSuccess || out != "" || errOut != "" {
		t.Fatalf("set = %d %q %q", code, out, errOut)
	}
	if code, _, _ := runConfigCLI(t, "set", "mouse", "off"); code != ExitSuccess {
		t.Fatalf("set mouse = %d", code)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "theme = \"nord\"\nmouse = false\n" {
		t.Fatalf("file = %q, %v", data, err)
	}
	if code, out, _ := runConfigCLI(t, "get", "THEME"); code != ExitSuccess || out != "nord\n" {
		t.Fatalf("get = %d %q", code, out)
	}
	if code, _, _ := runConfigCLI(t, "unset", "theme"); code != ExitSuccess {
		t.Fatalf("unset = %d", code)
	}
	if code, out, _ := runConfigCLI(t, "get", "theme"); code != ExitSuccess || out != "auto\n" {
		t.Fatalf("get after unset = %d %q", code, out)
	}
	t.Setenv("GUGET_MOUSE", "true")
	code, _, errOut := runConfigCLI(t, "set", "mouse", "false")
	if code != ExitSuccess || !strings.Contains(errOut, "GUGET_MOUSE is set in the environment and overrides the settings file") {
		t.Fatalf("set under env override = %d %q", code, errOut)
	}
}

func TestConfigCommandRejectsInvalidInput(t *testing.T) {
	path := useSettingsFile(t, "")
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"set", "theme", "neon"}, `invalid value "neon" for theme`},
		{[]string{"set", "mouse", "maybe"}, `invalid value "maybe" for mouse: expected true or false`},
		{[]string{"set", "colour", "false"}, `unknown setting "colour" (expected one of theme, sort, mouse, update-check, verbosity, color, update-target)`},
		{[]string{"get", "nope"}, `unknown setting "nope"`},
		{[]string{"unset", "nope"}, `unknown setting "nope"`},
		{[]string{"set", "theme"}, "Usage: guget config"},
		{[]string{"get"}, "Usage: guget config"},
		{[]string{"frob"}, `unknown config subcommand "frob"`},
		{[]string{}, "Usage: guget config"},
		{[]string{"get", "theme", "--format", "json"}, "--format and --output apply only to guget config show"},
		{[]string{"show", "--format", "yaml"}, `invalid --format "yaml"`},
		{[]string{"show", "--bogus"}, "unknown flag --bogus"},
	}
	for _, tt := range tests {
		code, out, errOut := runConfigCLI(t, tt.args...)
		if code != ExitUsage || out != "" || !strings.Contains(errOut, tt.want) {
			t.Fatalf("config %v = %d stdout=%q stderr=%q; want usage error containing %q", tt.args, code, out, errOut, tt.want)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("rejected input wrote the settings file: %v", err)
	}
}

func TestConfigCommandShowJSON(t *testing.T) {
	path := useSettingsFile(t, "theme = \"nord\"\nbogus = 1\n")
	t.Setenv("NO_COLOR", "1")
	code, out, errOut := runConfigCLI(t, "show", "--format", "json")
	if code != ExitSuccess || errOut != "" {
		t.Fatalf("show = %d stderr=%q", code, errOut)
	}
	var doc struct {
		SchemaVersion int    `json:"schemaVersion"`
		Command       string `json:"command"`
		Path          string `json:"path"`
		Settings      []struct {
			Key, Source, Description string
			Value, Default           any
		} `json:"settings"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if doc.SchemaVersion != 1 || doc.Command != "config" || doc.Path != path {
		t.Fatalf("header = %+v", doc)
	}
	got := make([]string, 0, len(doc.Settings))
	for _, row := range doc.Settings {
		if row.Description == "" {
			t.Fatalf("%s has no description", row.Key)
		}
		got = append(got, fmt.Sprintf("%s=%#v/%s/%#v", row.Key, row.Value, row.Source, row.Default))
	}
	want := []string{
		`theme="nord"/file/"auto"`,
		`sort="status:asc"/default/"status:asc"`,
		`mouse=true/default/true`,
		`update-check=true/default/true`,
		`verbosity="warn"/default/"warn"`,
		`color=false/env/true`,
		`update-target="compatible-stable"/default/"compatible-stable"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("settings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(doc.Warnings) != 1 || !strings.Contains(doc.Warnings[0], `unknown setting "bogus"`) {
		t.Fatalf("warnings = %q", doc.Warnings)
	}

	useSettingsFile(t, "")
	_, out, _ = runConfigCLI(t, "show", "--format", "json")
	if !strings.Contains(out, `"warnings": []`) {
		t.Fatalf("empty warnings are not an array: %s", out)
	}
}

func TestConfigCommandShowTSVAndOutputFile(t *testing.T) {
	useSettingsFile(t, "nope = true\n")
	output := filepath.Join(t.TempDir(), "settings.tsv")
	code, out, errOut := runConfigCLI(t, "show", "--format", "tsv", "--output", output)
	if code != ExitSuccess || out != "" || !strings.Contains(errOut, `guget: warning: `) || !strings.Contains(errOut, `unknown setting "nope"`) {
		t.Fatalf("show = %d stdout=%q stderr=%q", code, out, errOut)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != len(settingKeys())+1 || lines[0] != "KEY\tVALUE\tSOURCE\tDEFAULT\tDESCRIPTION" || !strings.HasPrefix(lines[1], "theme\tauto\tdefault\tauto\t") {
		t.Fatalf("tsv = %q", data)
	}
}

func TestConfigCommandIsRegistered(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := dispatchCLI(context.Background(), []string{"help", "config"}, nil, &stdout, &stderr); code != ExitSuccess || !strings.Contains(stdout.String(), "set KEY VALUE") {
		t.Fatalf("help config = %d %q", code, stdout.String())
	}
	if !strings.Contains(cliUsage(), "config") {
		t.Fatal("usage does not list config")
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		stdout.Reset()
		if code := dispatchCLI(context.Background(), []string{"completion", shell}, nil, &stdout, &stderr); code != ExitSuccess {
			t.Fatalf("completion %s = %d", shell, code)
		}
		for _, word := range []string{"config", "unset", "update-target"} {
			if !strings.Contains(stdout.String(), word) {
				t.Fatalf("%s completion lacks %q:\n%s", shell, word, stdout.String())
			}
		}
	}
}
