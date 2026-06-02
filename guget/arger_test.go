package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func parseRegisteredCLIForTest(t *testing.T, args ...string) (BuiltFlags, []string) {
	t.Helper()
	resetCLIParserForTest(t)
	os.Args = append([]string{"guget"}, args...)
	registerCLIFlags()
	parsed, extra := ParseFlags()
	return BuildFlags(parsed), extra
}

func resetCLIParserForTest(t *testing.T) {
	t.Helper()

	oldArgs := os.Args
	oldRegisteredFlags := registeredFlags
	oldAliasToFlag := aliasToFlag
	oldLogLevel := logLevel
	oldLogColorEnabled := logColorEnabled
	oldLogOutWriter := logOutWriter
	oldLogErrWriter := logErrWriter

	registeredFlags = make(map[string]IFlag)
	aliasToFlag = make(map[string]IFlag)
	logSetLevel(LogLevelNone)
	logSetColor(false)
	logSetOutput(io.Discard)

	t.Cleanup(func() {
		os.Args = oldArgs
		registeredFlags = oldRegisteredFlags
		aliasToFlag = oldAliasToFlag
		logLevel = oldLogLevel
		logColorEnabled = oldLogColorEnabled
		logOutWriter = oldLogOutWriter
		logErrWriter = oldLogErrWriter
	})
}

func assertBuiltFlags(t *testing.T, got, want BuiltFlags) {
	t.Helper()
	if got != want {
		t.Fatalf("flags mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestCLIParseDefaults(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	flags, extra := parseRegisteredCLIForTest(t)
	assertBuiltFlags(t, flags, BuiltFlags{
		NoColor:    false,
		Verbosity:  "warn",
		ProjectDir: cwd,
		Version:    false,
		LogFile:    "",
		Theme:      "auto",
		SortBy:     "status:asc",
	})
	if len(extra) != 0 {
		t.Fatalf("expected no extra args, got %v", extra)
	}
}

func TestCLIParseLongAliases(t *testing.T) {
	projectPath := `F:\Projects\Clipboard inspector\Clipboard inspector CLI\`
	logPath := `C:\Logs\guget log.txt`

	flags, extra := parseRegisteredCLIForTest(
		t,
		"--version",
		"--no-color",
		"--verbose", "debug",
		"--project", projectPath,
		"--log-file", logPath,
		"--theme", "nord",
		"--sort-by", "name:desc",
	)

	assertBuiltFlags(t, flags, BuiltFlags{
		NoColor:    true,
		Verbosity:  "debug",
		ProjectDir: projectPath,
		Version:    true,
		LogFile:    logPath,
		Theme:      "nord",
		SortBy:     "name:desc",
	})
	if len(extra) != 0 {
		t.Fatalf("expected no extra args, got %v", extra)
	}
}

func TestCLIParseShortAliases(t *testing.T) {
	projectPath := `D:\Workspace With Spaces\App\`
	logPath := `D:\Log Files\guget startup.log`

	flags, extra := parseRegisteredCLIForTest(
		t,
		"-V",
		"-nc",
		"-v", "trc",
		"-p", projectPath,
		"-lf", logPath,
		"-t", "gruvbox",
		"-o", "current",
	)

	assertBuiltFlags(t, flags, BuiltFlags{
		NoColor:    true,
		Verbosity:  "trc",
		ProjectDir: projectPath,
		Version:    true,
		LogFile:    logPath,
		Theme:      "gruvbox",
		SortBy:     "current",
	})
	if len(extra) != 0 {
		t.Fatalf("expected no extra args, got %v", extra)
	}
}

func TestCLIParsePreservesStringValues(t *testing.T) {
	projectPath := `F:\Projects\Clipboard inspector\Clipboard inspector CLI\`
	logPath := `F:\Projects\Clipboard inspector\logs\guget trace.log`

	flags, _ := parseRegisteredCLIForTest(t, "-v", "", "-p", projectPath, "-lf", logPath)

	if flags.Verbosity != "" {
		t.Fatalf("expected empty verbosity, got %q", flags.Verbosity)
	}
	if flags.ProjectDir != projectPath {
		t.Fatalf("expected project path %q, got %q", projectPath, flags.ProjectDir)
	}
	if flags.LogFile != logPath {
		t.Fatalf("expected log path %q, got %q", logPath, flags.LogFile)
	}
}

func TestCLIParseNamedProjectOverridesDefault(t *testing.T) {
	projectPath := `E:\Repos\Named Project\`

	flags, _ := parseRegisteredCLIForTest(t, "--project", projectPath)

	if flags.ProjectDir != projectPath {
		t.Fatalf("expected named project path %q, got %q", projectPath, flags.ProjectDir)
	}
}

func TestCLIParseSortByCustomParser(t *testing.T) {
	tests := []string{
		"",
		"status",
		"name:desc",
		"source:asc",
		"current:desc",
		"available",
		":desc",
	}

	for _, sortBy := range tests {
		t.Run("sort-by="+sortBy, func(t *testing.T) {
			flags, _ := parseRegisteredCLIForTest(t, "--sort-by", sortBy)
			if flags.SortBy != sortBy {
				t.Fatalf("expected sort-by %q, got %q", sortBy, flags.SortBy)
			}
		})
	}
}

func TestCLIParseCollectsExtraArgsAfterDoubleDash(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "marker first",
			args: []string{"--", "--not-a-flag", "value"},
			want: []string{"--not-a-flag", "value"},
		},
		{
			name: "after parsed flags",
			args: []string{"--verbose", "info", "--", "one two", "-z"},
			want: []string{"one two", "-z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, extra := parseRegisteredCLIForTest(t, tt.args...)
			if strings.Join(extra, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("expected extra args %v, got %v", tt.want, extra)
			}
		})
	}
}

func TestCLIParseNonStringGenericParsing(t *testing.T) {
	resetCLIParserForTest(t)
	os.Args = []string{"guget", "--count", "42"}
	RegisterFlag(Flag[int]{
		Name:    "count",
		Aliases: []string{"--count"},
	})

	parsed, extra := ParseFlags()

	if got := GetFlag[int](parsed, "count"); got != 42 {
		t.Fatalf("expected count 42, got %d", got)
	}
	if len(extra) != 0 {
		t.Fatalf("expected no extra args, got %v", extra)
	}
}

func TestCLIParseRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unknown flag",
			args: []string{"--unknown"},
			want: "Unknown flag: --unknown",
		},
		{
			name: "missing flag value",
			args: []string{"--verbose"},
			want: "Flag --verbosity expects a value but none was provided",
		},
		{
			name: "invalid verbosity",
			args: []string{"--verbose", "loud"},
			want: "invalid value loud",
		},
		{
			name: "invalid theme",
			args: []string{"--theme", "solarized"},
			want: "invalid value solarized",
		},
		{
			name: "invalid sort field",
			args: []string{"--sort-by", "age"},
			want: "invalid sort mode: age",
		},
		{
			name: "invalid sort direction",
			args: []string{"--sort-by", "name:sideways"},
			want: "invalid sort dir: sideways",
		},
		{
			name: "unexpected additional positional",
			args: []string{"project one", "project two"},
			want: "Unexpected positional argument: project one",
		},
		{
			name: "positional project path",
			args: []string{`F:\Projects\Clipboard inspector\Clipboard inspector CLI\`},
			want: `Unexpected positional argument: F:\Projects\Clipboard inspector\Clipboard inspector CLI\`,
		},
		{
			name: "positional project with named project",
			args: []string{"--project", "project one", "project two"},
			want: "Unexpected positional argument: project two",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			argsJSON, err := json.Marshal(tt.args)
			if err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestCLIParseRejectsInvalidArgumentsHelper$")
			cmd.Env = append(os.Environ(),
				"GUGET_TEST_PARSE_REJECT=1",
				"GUGET_TEST_PARSE_ARGS="+string(argsJSON),
			)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected parse failure, got success; output:\n%s", output)
			}
			if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
				t.Fatalf("expected exit code 1, got %v; output:\n%s", err, output)
			}
			if !strings.Contains(string(output), tt.want) {
				t.Fatalf("expected output to contain %q, got:\n%s", tt.want, output)
			}
		})
	}
}

func TestCLIParseRejectsInvalidArgumentsHelper(t *testing.T) {
	if os.Getenv("GUGET_TEST_PARSE_REJECT") != "1" {
		return
	}

	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("GUGET_TEST_PARSE_ARGS")), &args); err != nil {
		t.Fatal(err)
	}

	registeredFlags = make(map[string]IFlag)
	aliasToFlag = make(map[string]IFlag)
	os.Args = append([]string{"guget"}, args...)
	logSetLevel(LogLevelError)
	logSetColor(false)
	logOutWriter = nil
	logErrWriter = nil

	registerCLIFlags()
	ParseFlags()
	os.Exit(0)
}
