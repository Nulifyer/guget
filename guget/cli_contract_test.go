package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDotnet answers the dotnet invocations made by list and show: SDK
// version, MSBuild evaluation, and the restored package graph. A nil map entry
// fails that call, so tests never depend on an installed SDK.
type fakeDotnet struct {
	msbuild map[string]string // project path -> MSBuild JSON
	graph   map[string]string // project path -> package-list JSON
	calls   []string
}

func (f *fakeDotnet) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	switch {
	case len(args) > 0 && args[0] == "--version":
		return []byte("10.0.100\n"), nil
	case len(args) > 1 && args[0] == "msbuild":
		if data, ok := f.msbuild[args[1]]; ok {
			return []byte(data), nil
		}
	case len(args) > 3 && args[0] == "package":
		if data, ok := f.graph[args[3]]; ok {
			return []byte(data), nil
		}
	}
	return nil, errors.New("dotnet unavailable in test")
}

func runCLIWith(t *testing.T, runtime *cliRuntime, args ...string) (ExitCode, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	runtime.stdout, runtime.stderr = &stdout, &stderr
	if runtime.runner == nil {
		runtime.runner = &fakeDotnet{}
	}
	code := runtime.dispatch(context.Background(), args)
	return code, stdout.String(), stderr.String()
}

func runHermetic(t *testing.T, args ...string) (ExitCode, string, string) {
	t.Helper()
	return runCLIWith(t, &cliRuntime{}, args...)
}

func TestVerbSpecFlagsAreDocumentedAndCompleted(t *testing.T) {
	bash, _ := completionScript("bash")
	zsh, _ := completionScript("zsh")
	fish, _ := completionScript("fish")
	usage := cliUsage()
	for _, spec := range cliVerbsInOrder() {
		help := verbHelp(spec)
		if !strings.Contains(usage, "  "+spec.Name) {
			t.Errorf("guget help does not list %s", spec.Name)
		}
		for _, script := range []string{bash, zsh, fish} {
			if !strings.Contains(script, spec.Name) {
				t.Errorf("completion does not offer verb %s", spec.Name)
			}
		}
		for _, flag := range spec.Flags {
			if !strings.Contains(help, "--"+flag.Name) {
				t.Errorf("%s help omits --%s", spec.Name, flag.Name)
			}
			if !strings.Contains(zsh, "--"+flag.Name) {
				t.Errorf("zsh completion omits --%s", flag.Name)
			}
			if !strings.Contains(fish, fmt.Sprintf("'__fish_seen_subcommand_from %s' -l %s", spec.Name, flag.Name)) {
				t.Errorf("fish completion omits %s --%s", spec.Name, flag.Name)
			}
			bashFlags := bashFlagWords(t, bash, spec.Name)
			if !containsFold(bashFlags, "--"+flag.Name) {
				t.Errorf("bash completion omits %s --%s (have %v)", spec.Name, flag.Name, bashFlags)
			}
			// Every documented flag parses; nothing else does.
			argv := []string{"--" + flag.Name}
			if flag.takesValue() {
				value := "x"
				if len(flag.Choices) > 0 {
					value = flag.Choices[0]
				}
				argv = append(argv, value)
			}
			for _, positional := range spec.Positionals {
				if !positional.Optional {
					argv = append(argv, "value")
				}
			}
			if _, err := parseVerbArgs(spec, argv); err != nil && strings.Contains(err.Error(), "unknown flag") {
				t.Errorf("%s rejects documented flag --%s: %v", spec.Name, flag.Name, err)
			}
		}
		if _, err := parseVerbArgs(spec, []string{"--definitely-undocumented"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s accepted an undocumented flag: %v", spec.Name, err)
		}
	}
}

// bashFlagWords extracts the flag list offered for one verb.
func bashFlagWords(t *testing.T, script, verb string) []string {
	t.Helper()
	start := strings.Index(script, "\n    "+verb+")\n")
	if start < 0 {
		t.Fatalf("bash completion has no case for %s", verb)
	}
	section := script[start:]
	marker := `if [[ "$cur" == -* ]]; then COMPREPLY=($(compgen -W "`
	at := strings.Index(section, marker)
	if at < 0 {
		t.Fatalf("bash completion offers no flags for %s", verb)
	}
	words := section[at+len(marker):]
	return strings.Fields(words[:strings.Index(words, `"`)])
}

func TestBashCompletionFallsBackToFilesForFlagValues(t *testing.T) {
	bash, _ := completionScript("bash")
	if !strings.Contains(bash, "complete -o default -F _guget guget") {
		t.Fatal("bash completion must fall back to default file completion")
	}
	// Flags are offered only for words that start with "-".
	if strings.Count(bash, `if [[ "$cur" == -* ]]`) < len(cliVerbRegistry) {
		t.Fatal("bash completion offers flags without a leading dash")
	}
}

func TestHelpBehavesTheSameForEveryVerb(t *testing.T) {
	for _, spec := range cliVerbsInOrder() {
		for _, args := range [][]string{{spec.Name, "--help"}, {spec.Name, "-h"}, {"help", spec.Name}} {
			code, stdout, stderr := runHermetic(t, args...)
			if code != ExitSuccess || stderr != "" || !strings.HasPrefix(stdout, "Usage: guget "+spec.Name) {
				t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
			}
		}
	}
}

func TestSearchHelpShowsDefaultTimeoutAndNoColorNote(t *testing.T) {
	help := verbHelp(lookupCLIVerb("search"))
	if !strings.Contains(help, "(default: 30s)") || !strings.Contains(help, "--no-color") || !strings.Contains(flagNoColor.Description, "no effect on CLI output") {
		t.Fatalf("help=%s", help)
	}
	if !strings.Contains(cliUsage(), "--theme") {
		t.Fatal("guget help should mention TUI options")
	}
}

func TestParseCLIFormat(t *testing.T) {
	for _, test := range []struct {
		raw     string
		kind    cliFormatKind
		wantErr string
	}{
		{raw: "table", kind: formatTable},
		{raw: "TSV", kind: formatTSV},
		{raw: "json", kind: formatJSON},
		{raw: "jsonl", kind: formatJSONL},
		{raw: "{{.package}}", kind: formatTemplate},
		{raw: `table {{.package}}\t{{.version}}`, kind: formatTableTemplate},
		{raw: "yaml", wantErr: "invalid --format"},
		{raw: "{{.package", wantErr: "invalid --format template"},
		{raw: "{{nosuchfunc .package}}", wantErr: "invalid --format template"},
	} {
		format, err := parseCLIFormat(test.raw)
		if test.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("%q: err=%v", test.raw, err)
			}
			continue
		}
		if err != nil || format.kind != test.kind {
			t.Errorf("%q: kind=%v err=%v", test.raw, format.kind, err)
		}
	}
}

func TestRenderTemplatesAndTables(t *testing.T) {
	resolved := "1.2.0"
	rows := []cliPackageRow{
		{Project: "App.csproj", Package: "Example.Core", ResolvedVersion: &resolved, TargetFrameworks: []string{"net8.0", "net9.0"}},
		{Project: "Lib.csproj", Package: "Other", TargetFrameworks: []string{}},
	}
	result := cliResult{Document: rows, Items: rows, Columns: packageColumns}
	render := func(raw string, noHeaders bool) string {
		t.Helper()
		format, err := parseCLIFormat(raw)
		if err != nil {
			t.Fatal(err)
		}
		data, err := renderCLIResult(format, noHeaders, result)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if got := render("{{.package}}@{{.resolvedVersion}}", false); got != "Example.Core@1.2.0\nOther@\n" {
		t.Fatalf("template=%q", got)
	}
	if got := render(`{{.package}} {{join .targetFrameworks ";"}} {{json .resolvedVersion}} {{upper .project}}`, false); got != "Example.Core net8.0;net9.0 \"1.2.0\" APP.CSPROJ\nOther  null LIB.CSPROJ\n" {
		t.Fatalf("funcs=%q", got)
	}
	table := render(`table {{.package}}\t{{.resolvedVersion}}`, false)
	if lines := strings.Split(table, "\n"); lines[0] != "PACKAGE       RESOLVEDVERSION" || lines[1] != "Example.Core  1.2.0" {
		t.Fatalf("table template=%q", table)
	}
	if got := render(`table {{.package}}\t{{.resolvedVersion}}`, true); strings.Contains(got, "PACKAGE") {
		t.Fatalf("no-headers table template=%q", got)
	}
	if got := render("tsv", false); !strings.HasPrefix(got, "PROJECT\tPACKAGE\tREQUESTED\tRESOLVED\tVERSION_OWNER\tFRAMEWORKS\n") || !strings.Contains(got, "net8.0,net9.0") {
		t.Fatalf("tsv=%q", got)
	}
	if got := render("tsv", true); strings.Contains(got, "PROJECT") {
		t.Fatalf("no-headers tsv=%q", got)
	}
	if got := render("table", true); strings.Contains(got, "PROJECT") || !strings.Contains(got, "Example.Core") {
		t.Fatalf("no-headers table=%q", got)
	}
	jsonl := render("jsonl", false)
	if lines := strings.Split(strings.TrimSpace(jsonl), "\n"); len(lines) != 2 || !strings.Contains(lines[1], `"resolvedVersion":null`) {
		t.Fatalf("jsonl=%q", jsonl)
	}
}

func TestInvalidFormatIsRejectedBeforeWorkspaceWork(t *testing.T) {
	runner := &fakeDotnet{}
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	for _, format := range []string{"{{.pacakge}}", "{{.package", "yaml"} {
		code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", missing, "--format", format)
		if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "--format") {
			t.Fatalf("format=%q code=%d stdout=%q stderr=%q", format, code, stdout, stderr)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("format validation ran dotnet: %v", runner.calls)
	}
	// A dry run's template fields are plan rows; the result document's fields
	// are rejected there.
	code, _, stderr := runHermetic(t, "update", "Example", "--all", "--version", "2.0.0", "--dry-run", "--project", missing, "--format", "{{.status}}")
	if code != ExitUsage || !strings.Contains(stderr, `unknown field "status"`) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestTemplateExecutionErrorsAreRejectedBeforeWork(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	for _, test := range []struct {
		args  []string
		valid bool
	}{
		{args: []string{"list", "--format", "{{.package.x}}"}},
		{args: []string{"list", "--format", "{{range .advisories}}{{.nope}}{{end}}"}},
		{args: []string{"list", "--format", "{{range .advisories}}{{.severity}}{{end}}{{.updateTarget}}"}, valid: true},
		{args: []string{"update", "Example", "--all", "--version", "2.0.0", "--format", "{{.status.x}}"}},
		{args: []string{"update", "Example", "--all", "--version", "2.0.0", "--format", "{{range .changes}}{{.version.x}}{{end}}"}},
		{args: []string{"update", "Example", "--all", "--version", "2.0.0", "--dry-run", "--format", "{{.path.x}}"}},
		{args: []string{"update", "Example", "--all", "--version", "2.0.0", "--format", "{{len .changed}} {{.error}}"}, valid: true},
		{args: []string{"sources", "--format", "{{.patterns.x}}"}},
	} {
		code, stdout, stderr := runHermetic(t, append(test.args, "--project", missing)...)
		if test.valid {
			// A valid template gets as far as the missing workspace.
			if code != ExitWorkspace {
				t.Errorf("%v: code=%d stderr=%q", test.args, code, stderr)
			}
			continue
		}
		if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "invalid --format template") {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", test.args, code, stdout, stderr)
		}
	}
}

func TestOutputFailureAfterAnEditExitsWithWriteError(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/></packageSources></configuration>`)
	project := writeCLIProject(t, root, "App", `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	before, _ := os.ReadFile(project)
	code, _, stderr := runHermetic(t, "update", "Example", "--file", project, "--version", "2.0.0", "--project", root, "--format", "{{.status.x}}")
	if after, _ := os.ReadFile(project); code != ExitUsage || !bytes.Equal(before, after) {
		t.Fatalf("invalid template: code=%d stderr=%q, project changed=%v", code, stderr, !bytes.Equal(before, after))
	}
	// The sample row's empty status skips this branch, so only the applied
	// result fails to render: the edit is in place and the exit is 5.
	code, _, stderr = runHermetic(t, "update", "Example", "--file", project, "--version", "2.0.0", "--project", root, "--format", `{{if eq .status "changed"}}{{.status.x}}{{end}}`)
	after, _ := os.ReadFile(project)
	if code != ExitWrite || !strings.Contains(string(after), `Version="2.0.0"`) || !strings.Contains(stderr, "package changes were applied") {
		t.Fatalf("code=%d stderr=%q project=%s", code, stderr, after)
	}
}

func TestDefaultFormatIsTableOnlyForATerminal(t *testing.T) {
	old := isTerminalWriter
	t.Cleanup(func() { isTerminalWriter = old })
	for _, test := range []struct {
		terminal bool
		output   string
		want     cliFormatKind
	}{
		{terminal: true, want: formatTable},
		{terminal: true, output: "-", want: formatTable},
		{terminal: true, output: "result.tsv", want: formatTSV},
		{terminal: false, want: formatTSV},
	} {
		isTerminalWriter = func(io.Writer) bool { return test.terminal }
		if got := defaultCLIFormat(io.Discard, test.output).kind; got != test.want {
			t.Errorf("terminal=%v output=%q: got %v want %v", test.terminal, test.output, got, test.want)
		}
	}
}

func TestJSONFailuresStillProduceADocument(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	code, stdout, stderr := runHermetic(t, "list", "--project", missing, "--format", "json")
	if code != ExitWorkspace || stderr == "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "{\n  \"schemaVersion\": 1,\n  \"command\": \"list\",\n  \"status\": \"failed\",\n  \"error\": ") {
		t.Fatalf("stdout=%s", stdout)
	}
	var document listDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil || document.Packages == nil || document.Warnings == nil || document.Error == nil {
		t.Fatalf("document=%+v err=%v", document, err)
	}

	// Usage errors and non-JSON formats keep stdout empty.
	for _, args := range [][]string{
		{"list", "--project", missing, "--format", "json", "--bogus"},
		{"list", "--project", missing, "--format", "jsonl"},
		{"list", "--project", missing, "--format", "{{.package}}"},
	} {
		if _, stdout, stderr := runHermetic(t, args...); stdout != "" || stderr == "" {
			t.Fatalf("args=%v stdout=%q stderr=%q", args, stdout, stderr)
		}
	}
}

func TestRefusedEditReportsJSONStatus(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Other" Version="1.0.0" /></ItemGroup></Project>`)
	code, stdout, _ := runHermetic(t, "update", "Example", "--project", root, "--file", project, "--version", "2.0.0", "--format", "json")
	var document planDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("stdout=%s err=%v", stdout, err)
	}
	if code != ExitRefused || document.Status != "refused" || document.Error == nil || !strings.Contains(*document.Error, "not installed") {
		t.Fatalf("code=%d document=%+v", code, document)
	}
}

func TestExitCodeMapping(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="test" value="https://packages.example.test/index.json"/><add key="local" value="./local-feed"/></packageSources></configuration>`)
	blocking := func(ctx context.Context, _ NugetSource) (*NugetService, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	t.Run("timeout", func(t *testing.T) {
		code, stdout, stderr := runCLIWith(t, &cliRuntime{newService: blocking}, "search", "Example", "--project", root, "--timeout", "20ms", "--format", "json")
		if code != ExitTimeout || !strings.Contains(stderr, "guget: timed out after 20ms") || !strings.Contains(stdout, `"status": "timed-out"`) {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	})
	t.Run("interrupt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		service := func(ctx context.Context, _ NugetSource) (*NugetService, error) {
			cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		}
		var stdout, stderr bytes.Buffer
		runtime := &cliRuntime{stdout: &stdout, stderr: &stderr, newService: service, runner: &fakeDotnet{}}
		code := runtime.dispatch(ctx, []string{"search", "Example", "--project", root, "--timeout", "1h"})
		if code != ExitInterrupted || !strings.Contains(stderr.String(), "interrupted") {
			t.Fatalf("code=%d stderr=%s", code, stderr.String())
		}
	})
	t.Run("unknown source", func(t *testing.T) {
		code, _, stderr := runHermetic(t, "search", "Example", "--project", root, "--source", "typo")
		if code != ExitUsage || !strings.Contains(stderr, `unknown --source "typo"`) {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	})
	t.Run("restore-only source skipped", func(t *testing.T) {
		service := func(_ context.Context, source NugetSource) (*NugetService, error) {
			return nil, errors.New("offline")
		}
		code, _, stderr := runCLIWith(t, &cliRuntime{newService: service}, "search", "Example", "--project", root, "--source", "local")
		if code != ExitUsage || !strings.Contains(stderr, "restore-only") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	})
	t.Run("invalid version", func(t *testing.T) {
		code, _, stderr := runHermetic(t, "update", "Example", "--project", root, "--file", project, "--version", "not-a-version")
		if code != ExitUsage || !strings.Contains(stderr, "invalid --version") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	})
	t.Run("conflicting scope", func(t *testing.T) {
		code, _, _ := runHermetic(t, "update", "Example", "--project", root, "--file", project, "--all", "--version", "2.0.0")
		if code != ExitUsage {
			t.Fatalf("code=%d", code)
		}
	})
	t.Run("bad project", func(t *testing.T) {
		code, _, _ := runHermetic(t, "sources", "--project", filepath.Join(root, "missing"))
		if code != ExitWorkspace {
			t.Fatalf("code=%d", code)
		}
	})
	t.Run("evaluation unavailable", func(t *testing.T) {
		evaluated := t.TempDir()
		path := writeCLIProject(t, evaluated, "Sdk", `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
		code, _, stderr := runHermetic(t, "update", "Example", "--project", evaluated, "--file", path, "--version", "2.0.0")
		if code != ExitWorkspace || !strings.Contains(stderr, "requires MSBuild evaluation") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	})
}

func TestShowUnreferencedPackageIsEmptySuccess(t *testing.T) {
	root := t.TempDir()
	writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	code, stdout, stderr := runHermetic(t, "show", "Missing.Package", "--project", root, "--format", "json")
	var document listDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatal(err)
	}
	if code != ExitSuccess || len(document.Packages) != 0 || document.Packages == nil || !strings.Contains(stderr, "is not referenced") {
		t.Fatalf("code=%d document=%+v stderr=%s", code, document, stderr)
	}
}

func TestListRowsAreCompleteAndSortedInBothModes(t *testing.T) {
	root := t.TempDir()
	evaluatedProject := writeCLIProject(t, root, "Evaluated", `<Project><PropertyGroup><TargetFrameworks>net8.0;net9.0</TargetFrameworks></PropertyGroup><ItemGroup><PackageReference Include="zeta" Version="1.0.0" /><PackageReference Include="Alpha" Version="2.0.0" /></ItemGroup></Project>`)
	writeCLIProject(t, root, "Fallback", `<Project><PropertyGroup><TargetFrameworks>net9.0;net8.0</TargetFrameworks></PropertyGroup><ItemGroup><PackageReference Include="Alpha" Version="2.0.0" /></ItemGroup></Project>`)
	evaluation := func(framework string) string {
		return fmt.Sprintf(`{"Properties":{"TargetFramework":%q,"TargetFrameworks":"net8.0;net9.0"},"Items":{"PackageReference":[{"Identity":"zeta","Version":"1.0.0","DefiningProjectFullPath":%q},{"Identity":"Alpha","Version":"2.0.0","DefiningProjectFullPath":%q}]}}`, framework, evaluatedProject, evaluatedProject)
	}
	runner := &fakeDotnet{msbuild: map[string]string{evaluatedProject: evaluation("")}, graph: map[string]string{
		evaluatedProject: `{"version":1,"projects":[{"path":"x","frameworks":[{"framework":"net8.0","topLevelPackages":[{"id":"Alpha","requestedVersion":"2.0.0","resolvedVersion":"2.0.0"},{"id":"zeta","requestedVersion":"1.0.0","resolvedVersion":"1.0.0"}]},{"framework":"net9.0","topLevelPackages":[{"id":"Alpha","requestedVersion":"2.0.0","resolvedVersion":"2.0.0"},{"id":"zeta","requestedVersion":"1.0.0","resolvedVersion":"1.0.0"}]}]}]}`,
	}}
	code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--format", "jsonl")
	if code != ExitPartial || !strings.Contains(stderr, "Fallback.csproj: MSBuild evaluation unavailable") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var got []string
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		for _, key := range cliRowKeys(cliPackageRow{}) {
			if _, ok := row[key]; !ok {
				t.Fatalf("row lacks %q: %s", key, line)
			}
		}
		got = append(got, fmt.Sprintf("%s %s %v %v", row["package"], row["project"], row["targetFramework"], row["resolvedVersion"]))
	}
	want := []string{
		"Alpha Evaluated.csproj net8.0 2.0.0",
		"Alpha Evaluated.csproj net9.0 2.0.0",
		"Alpha Fallback.csproj net8.0 <nil>",
		"Alpha Fallback.csproj net9.0 <nil>",
		"zeta Evaluated.csproj net8.0 1.0.0",
		"zeta Evaluated.csproj net9.0 1.0.0",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestReadVerbOutputCannotOverwriteWorkspaceInputs(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	config := filepath.Join(root, "NuGet.Config")
	mustWriteFile(t, config, `<configuration><packageSources><clear /></packageSources></configuration>`)
	for _, args := range [][]string{
		{"list", "--project", root, "--output", project},
		{"show", "Example", "--project", root, "--output", project},
		{"sources", "--project", root, "--output", config},
		{"search", "Example", "--project", root, "--output", config},
		{"restore", "--all", "--project", root, "--output", project, "--format", "json"},
	} {
		beforeProject, _ := os.ReadFile(project)
		beforeConfig, _ := os.ReadFile(config)
		code, stdout, stderr := runHermetic(t, args...)
		if code != ExitWrite || !strings.Contains(stderr, "overwrite a workspace input") {
			t.Fatalf("args=%v code=%d stderr=%s", args, code, stderr)
		}
		afterProject, _ := os.ReadFile(project)
		afterConfig, _ := os.ReadFile(config)
		if !bytes.Equal(beforeProject, afterProject) || !bytes.Equal(beforeConfig, afterConfig) {
			t.Fatalf("args=%v overwrote a workspace input", args)
		}
		if args[len(args)-1] == "json" && !strings.Contains(stdout, `"status": "failed"`) {
			t.Fatalf("refused output should report on stdout: %q", stdout)
		}
	}
}

func TestCLINeverPrintsSourceCredentials(t *testing.T) {
	root := t.TempDir()
	writeCLIProject(t, root, "App", `<Project />`)
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="private" value="https://user:s3cr3t@feed.example.test/index.json"/></packageSources></configuration>`)
	service := func(_ context.Context, source NugetSource) (*NugetService, error) {
		return nil, fmt.Errorf("Get %q: connection refused", source.URL)
	}
	for _, args := range [][]string{
		{"sources", "--project", root, "--format", "json"},
		{"sources", "--project", root, "--format", "table"},
		{"search", "Example", "--project", root, "--format", "json"},
	} {
		_, stdout, stderr := runCLIWith(t, &cliRuntime{newService: service}, args...)
		if strings.Contains(stdout+stderr, "s3cr3t") || !strings.Contains(stdout+stderr, "user:***@feed.example.test") {
			t.Fatalf("args=%v leaked or lost the URL:\nstdout=%s\nstderr=%s", args, stdout, stderr)
		}
	}
}

func TestUpdateSkipsAndRefusesExactPins(t *testing.T) {
	setup := func(t *testing.T) (string, string, string) {
		root := t.TempDir()
		pinned := writeCLIProject(t, root, "Pinned", `<Project><ItemGroup><PackageReference Include="Example" Version="[1.0.0]" /></ItemGroup></Project>`)
		floating := writeCLIProject(t, root, "Floating", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
		return root, pinned, floating
	}
	t.Run("bulk skips pins", func(t *testing.T) {
		root, pinned, floating := setup(t)
		code, stdout, stderr := runHermetic(t, "update", "Example", "--all", "--version", "2.0.0", "--project", root, "--format", "json")
		var document planDocument
		if err := json.Unmarshal([]byte(stdout), &document); err != nil || code != ExitSuccess {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
		if document.Skipped != 1 || len(document.Changes) != 2 {
			t.Fatalf("document=%+v", document)
		}
		var skip *planRow
		for i := range document.Changes {
			if document.Changes[i].Operation == pinnedSkipOperation {
				skip = &document.Changes[i]
			}
		}
		if skip == nil || skip.Path != pinned || skip.Version != "[1.0.0]" {
			t.Fatalf("changes=%+v", document.Changes)
		}
		if data, _ := os.ReadFile(pinned); !strings.Contains(string(data), "[1.0.0]") {
			t.Fatal("bulk update changed a pin")
		}
		if data, _ := os.ReadFile(floating); !strings.Contains(string(data), `Version="2.0.0"`) {
			t.Fatal("bulk update skipped an unpinned project")
		}
	})
	t.Run("explicit pin refused", func(t *testing.T) {
		root, pinned, _ := setup(t)
		code, _, stderr := runHermetic(t, "update", "Example", "--file", pinned, "--version", "2.0.0", "--project", root)
		if code != ExitRefused || !strings.Contains(stderr, "--include-pinned") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
		code, _, stderr = runHermetic(t, "update", "Example", "--file", pinned, "--version", "2.0.0", "--project", root, "--include-pinned")
		// The editor keeps the pin's exact-version form.
		if data, _ := os.ReadFile(pinned); code != ExitSuccess || !strings.Contains(string(data), `Version="[2.0.0]"`) {
			t.Fatalf("code=%d stderr=%s project=%s", code, stderr, data)
		}
	})
	t.Run("bulk includes pins on request", func(t *testing.T) {
		root, pinned, _ := setup(t)
		code, _, stderr := runHermetic(t, "update", "Example", "--all", "--version", "2.0.0", "--project", root, "--include-pinned")
		if data, _ := os.ReadFile(pinned); code != ExitSuccess || !strings.Contains(string(data), `Version="[2.0.0]"`) {
			t.Fatalf("code=%d stderr=%s project=%s", code, stderr, data)
		}
	})
}

func TestAllScopeIgnoresUnrelatedProjects(t *testing.T) {
	root := t.TempDir()
	referencing := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	// An SDK project needs MSBuild evaluation, which the fake runner refuses.
	writeCLIProject(t, root, "Unrelated", `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="Other" Version="1.0.0" /></ItemGroup></Project>`)
	for _, verb := range []string{"update", "remove"} {
		args := []string{verb, "Example", "--all", "--project", root, "--dry-run", "--format", "json"}
		if verb == "update" {
			args = append(args, "--version", "2.0.0")
		}
		code, stdout, stderr := runHermetic(t, args...)
		var document planDocument
		if err := json.Unmarshal([]byte(stdout), &document); err != nil || code != ExitSuccess {
			t.Fatalf("%s code=%d stdout=%s stderr=%s", verb, code, stdout, stderr)
		}
		if len(document.Changes) != 1 || document.Changes[0].Path != referencing {
			t.Fatalf("%s changes=%+v", verb, document.Changes)
		}
	}
}

func TestDryRunRestoreIsReportedAsSkipped(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	code, stdout, _ := runHermetic(t, "update", "Example", "--file", project, "--version", "2.0.0", "--project", root, "--dry-run", "--restore", "--format", "json")
	if code != ExitSuccess || !strings.Contains(stdout, `"restore": "skipped-dry-run"`) || !strings.HasPrefix(stdout, "{\n  \"schemaVersion\": 1,") {
		t.Fatalf("code=%d stdout=%s", code, stdout)
	}
}

func TestRestoreContinuesPastFailures(t *testing.T) {
	root := t.TempDir()
	first := writeCLIProject(t, root, "A", `<Project />`)
	second := writeCLIProject(t, root, "B", `<Project />`)
	var restored []string
	runtime := &cliRuntime{runCommand: func(_ context.Context, _ string, args []string, _ io.Reader, _, _ io.Writer) error {
		restored = append(restored, args[1])
		if args[1] == first {
			return errors.New("feed unavailable")
		}
		return nil
	}}
	code, stdout, stderr := runCLIWith(t, runtime, "restore", "--all", "--project", root, "--format", "json")
	if code != ExitRestore || len(restored) != 2 || !strings.Contains(stderr, "feed unavailable") {
		t.Fatalf("code=%d restored=%v stderr=%s", code, restored, stderr)
	}
	var document restoreDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatal(err)
	}
	if document.Status != "failed" || len(document.Projects) != 2 || document.Projects[0].Status != "failed" || document.Projects[0].Error == nil || document.Projects[1].Status != "succeeded" || document.Projects[1].ProjectPath != second {
		t.Fatalf("document=%+v", document)
	}
	code, stdout, _ = runCLIWith(t, &cliRuntime{runCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error { return nil }}, "restore", "--all", "--project", root, "--format", "{{.project}} {{.status}}")
	if code != ExitSuccess || stdout != "A.csproj succeeded\nB.csproj succeeded\n" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestPathsMatchAcrossListAndPlan(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	writeCLIProject(t, real, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	_, listOutput, _ := runHermetic(t, "list", "--project", alias, "--format", "{{.projectPath}}")
	_, planOutput, _ := runHermetic(t, "update", "Example", "--all", "--version", "2.0.0", "--project", alias, "--dry-run", "--format", "{{.path}}")
	want := filepath.Join(alias, "App", "App.csproj") + "\n"
	if listOutput != want || planOutput != want {
		t.Fatalf("list=%q plan=%q want=%q", listOutput, planOutput, want)
	}
}

func TestTimeoutFlagValidation(t *testing.T) {
	code, _, stderr := runHermetic(t, "list", "--timeout", "soon")
	if code != ExitUsage || !strings.Contains(stderr, "invalid --timeout") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	spec := lookupCLIVerb("search")
	inv, err := (&cliRuntime{stdout: io.Discard}).prepareInvocation(spec, []string{"x"})
	if err != nil || inv.timeout != 30*time.Second {
		t.Fatalf("search default timeout=%v err=%v", inv.timeout, err)
	}
}

func TestConfigShowUsesSharedRenderer(t *testing.T) {
	path := useSettingsFile(t, "theme = \"nord\"\n")
	code, stdout, _ := runHermetic(t, "config", "show", "--format", "{{.key}}={{.value}}")
	if code != ExitSuccess || !strings.HasPrefix(stdout, "theme=nord\n") || !strings.Contains(stdout, "mouse=true\n") {
		t.Fatalf("template code=%d stdout=%q", code, stdout)
	}
	code, stdout, _ = runHermetic(t, "config", "show", "--format", "tsv", "--no-headers")
	if code != ExitSuccess || strings.Contains(stdout, "KEY") || !strings.HasPrefix(stdout, "theme\tnord\tfile\tauto\t") {
		t.Fatalf("tsv code=%d stdout=%q", code, stdout)
	}
	code, _, stderr := runHermetic(t, "config", "show", "--format", "{{.nope}}")
	if code != ExitUsage || !strings.Contains(stderr, `unknown field "nope"`) {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	// --output may not replace the settings file; the JSON error goes to stdout.
	before, _ := os.ReadFile(path)
	code, stdout, stderr = runHermetic(t, "config", "show", "--format", "json", "--output", path)
	after, _ := os.ReadFile(path)
	if code != ExitWrite || !strings.Contains(stderr, "overwrite a workspace input") || !bytes.Equal(before, after) || !strings.Contains(stdout, `"status": "failed"`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestConfigCompletionOffersKeysAfterKeySubcommands(t *testing.T) {
	bash, _ := completionScript("bash")
	if !strings.Contains(bash, `1) case "$last" in get|set|unset) COMPREPLY=($(compgen -W "theme sort mouse`) {
		t.Fatalf("bash completion does not limit keys to get, set, and unset:\n%s", bash)
	}
	zsh, _ := completionScript("zsh")
	if !strings.Contains(zsh, `'2::key:{[[ ${words[2]} == (get|set|unset) ]] && compadd -- theme`) {
		t.Fatal("zsh completion does not offer keys after get, set, and unset")
	}
	fish, _ := completionScript("fish")
	if !strings.Contains(fish, `-n '__fish_seen_subcommand_from config; and __fish_seen_subcommand_from get set unset' -a 'theme`) {
		t.Fatal("fish completion does not offer keys after get, set, and unset")
	}
}
