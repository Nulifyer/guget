package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCLIProject(t *testing.T, root, name, contents string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".csproj")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCLIForTest(t *testing.T, args ...string) (ExitCode, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := dispatchCLI(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestDispatchCLI_VersionHasCleanStdout(t *testing.T) {
	oldVersion := version
	version = "1.2.3-test"
	t.Cleanup(func() { version = oldVersion })

	code, stdout, stderr := runCLIForTest(t, "version")
	if code != ExitSuccess {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if stdout != "guget 1.2.3-test\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestDispatchCLI_UnknownCommandIsUsageError(t *testing.T) {
	code, stdout, stderr := runCLIForTest(t, "definitely-not-a-command")
	if code != ExitUsage {
		t.Fatalf("code = %d", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestDispatchCLI_ListJSONPreservesRequestedExpression(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
  <ItemGroup><PackageReference Include="Example.Core" Version="[1.2.0,2.0.0)" /></ItemGroup>
</Project>
`)

	code, stdout, stderr := runCLIForTest(t, "list", "--project", root, "--format", "json")
	if code != ExitPartial {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "guget: warning:") {
		t.Fatalf("missing partial-result warning: %q", stderr)
	}
	var document listDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if document.SchemaVersion != 1 || len(document.Packages) != 1 {
		t.Fatalf("document = %#v", document)
	}
	row := document.Packages[0]
	if row.RequestedExpression != "[1.2.0,2.0.0)" {
		t.Fatalf("requested = %q", row.RequestedExpression)
	}
	if row.ProjectPath != project {
		t.Fatalf("project path = %q, want %q", row.ProjectPath, project)
	}
}

func TestDispatchCLI_AddDryRunAndApply(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>
</Project>
`)
	original, err := os.ReadFile(project)
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLIForTest(t, "add", "Example.Core", "--file", project, "--version", "1.4.0", "--project", root, "--dry-run", "--format", "json")
	if code != ExitSuccess {
		t.Fatalf("dry-run code = %d, stderr = %q", code, stderr)
	}
	var plan planDocument
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatalf("invalid plan JSON: %v", err)
	}
	if !plan.DryRun || len(plan.Changes) != 1 || plan.Changes[0].Path != canonicalTestOwnerPath(t, project) {
		t.Fatalf("plan = %#v", plan)
	}
	afterDryRun, _ := os.ReadFile(project)
	if !bytes.Equal(original, afterDryRun) {
		t.Fatal("dry-run changed the project")
	}

	code, stdout, stderr = runCLIForTest(t, "add", "--project", root, "--version", "1.4.0", "Example.Core", "--file", project)
	if code != ExitSuccess {
		t.Fatalf("apply code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	updated, _ := os.ReadFile(project)
	if !bytes.Contains(updated, []byte(`PackageReference Include="Example.Core" Version="1.4.0"`)) {
		t.Fatalf("project was not updated:\n%s", updated)
	}
}

func TestDispatchCLI_AddIgnoresDisabledCentralPackageFile(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
	if err := os.WriteFile(filepath.Join(root, "Directory.Packages.props"), []byte(`<Project><PropertyGroup><ManagePackageVersionsCentrally>false</ManagePackageVersionsCentrally></PropertyGroup></Project>`), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLIForTest(t, "add", "Example.Core", "--file", project, "--version", "1.4.0", "--project", root, "--dry-run", "--format", "json")
	if code != ExitSuccess {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var plan planDocument
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Path != canonicalTestOwnerPath(t, project) || plan.Changes[0].Version != "1.4.0" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestRestoreProjectsPassesInteractiveFlag(t *testing.T) {
	project := &ParsedProject{FilePath: filepath.Join(t.TempDir(), "App.csproj")}
	for _, test := range []struct {
		name        string
		interactive bool
		wantArgs    []string
	}{
		{name: "noninteractive", wantArgs: []string{"restore", project.FilePath}},
		{name: "interactive", interactive: true, wantArgs: []string{"restore", project.FilePath, "--interactive"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gotName string
			var gotArgs []string
			runtime := cliRuntime{
				stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard,
				runCommand: func(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
					gotName = name
					gotArgs = append([]string(nil), args...)
					return nil
				},
			}
			if code := runtime.restoreProjects(context.Background(), []*ParsedProject{project}, test.interactive); code != ExitSuccess {
				t.Fatalf("code = %d", code)
			}
			if gotName != "dotnet" || strings.Join(gotArgs, "\x00") != strings.Join(test.wantArgs, "\x00") {
				t.Fatalf("command = %s %q, want dotnet %q", gotName, gotArgs, test.wantArgs)
			}
		})
	}
}

func TestDispatchCLI_MutationRejectsUnusedInteractiveFlag(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk" />`)
	code, _, stderr := runCLIForTest(t, "add", "Example.Core", "--file", project, "--version", "1.0.0", "--project", root, "--interactive")
	if code != ExitUsage || !strings.Contains(stderr, "--interactive requires --restore") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestDispatchCLI_UpdateAndRemoveRequireExplicitScope(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk">
  <ItemGroup><PackageReference Include="Example.Core" Version="1.0.0" /></ItemGroup>
</Project>
`)

	code, _, stderr := runCLIForTest(t, "update", "Example.Core", "--version", "2.0.0", "--project", root)
	if code != ExitUsage || !strings.Contains(stderr, "exactly one mutation scope") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	code, _, stderr = runCLIForTest(t, "update", "--file", project, "Example.Core", "--version", "2.0.0", "--project", root)
	if code != ExitSuccess {
		t.Fatalf("update code = %d, stderr = %q", code, stderr)
	}
	updated, _ := os.ReadFile(project)
	if !bytes.Contains(updated, []byte(`Version="2.0.0"`)) {
		t.Fatalf("project was not updated:\n%s", updated)
	}

	code, _, stderr = runCLIForTest(t, "remove", "Example.Core", "--project", root, "--file", project)
	if code != ExitSuccess {
		t.Fatalf("remove code = %d, stderr = %q", code, stderr)
	}
	removed, _ := os.ReadFile(project)
	if bytes.Contains(removed, []byte("Example.Core")) {
		t.Fatalf("package was not removed:\n%s", removed)
	}
}

func TestDispatchCLI_OutputFileIsWrittenAfterSuccessfulRender(t *testing.T) {
	root := t.TempDir()
	writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="Example.Core" Version="1.0.0" /></ItemGroup></Project>`)
	output := filepath.Join(root, "packages.json")

	code, stdout, stderr := runCLIForTest(t, "list", "--project", root, "--format", "json", "--output", output)
	if code != ExitPartial || stdout != "" || !strings.Contains(stderr, "guget: warning:") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var document listDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
}

func TestDispatchCLI_FailedRenderPreservesExistingOutputFile(t *testing.T) {
	root := t.TempDir()
	writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk"><ItemGroup><PackageReference Include="Example.Core" Version="1.0.0" /></ItemGroup></Project>`)
	output := filepath.Join(root, "packages.out")
	if err := os.WriteFile(output, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, _ := runCLIForTest(t, "list", "--project", root, "--format", "invalid", "--output", output)
	if code != ExitUsage {
		t.Fatalf("code = %d", code)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep-me" {
		t.Fatalf("output changed to %q", data)
	}
}

func TestDispatchCLI_SearchUsesContextAwareConfiguredSource(t *testing.T) {
	root := t.TempDir()
	writeCLIProject(t, root, "App", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
	config := `<configuration><packageSources><clear/><add key="test" value="https://packages.example.test/index.json"/></packageSources></configuration>`
	if err := os.WriteFile(filepath.Join(root, "NuGet.Config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{"totalHits":1,"data":[{"id":"Example.Core","version":"2.0.0","description":"Example package","authors":["Example"]}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})
	var stdout, stderr bytes.Buffer
	runtime := cliRuntime{
		stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr,
		newService: func(_ context.Context, source NugetSource) (*NugetService, error) {
			return &NugetService{sourceName: source.Name, sourceURL: source.URL, searchBase: "https://packages.example.test/search", regBase: "https://packages.example.test/registration/", client: &http.Client{Transport: transport}}, nil
		},
	}
	code := runtime.runSearch(context.Background(), []string{"Example", "--project", root, "--format", "json"})
	if code != ExitSuccess || stderr.String() != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	var document searchDocument
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Results) != 1 || document.Results[0].Package != "Example.Core" || document.Results[0].Source != "test" {
		t.Fatalf("document = %#v", document)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDispatchCLI_CanceledContextReturnsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := dispatchCLI(ctx, []string{"search", "Example"}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitInterrupted {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
}

func TestMutationOutputIsValidatedBeforeWrites(t *testing.T) {
	for _, mode := range []string{"invalid-format", "missing-parent", "workspace-input", "invalid-version"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
			before, _ := os.ReadFile(project)
			args := []string{"update", "Example", "--file", project, "--version", "2.0.0", "--project", root}
			expected := ExitWrite
			switch mode {
			case "invalid-format":
				args = append(args, "--format", "invalid")
				expected = ExitUsage
			case "missing-parent":
				args = append(args, "--output", filepath.Join(root, "missing", "result.json"))
			case "workspace-input":
				args = append(args, "--output", project)
			case "invalid-version":
				args = append(args, "--version", "not-a-version")
				expected = ExitRefused
			}
			code, _, diagnostic := runCLIForTest(t, args...)
			if code != expected {
				t.Fatalf("code=%d expected=%d diagnostic=%s", code, expected, diagnostic)
			}
			after, _ := os.ReadFile(project)
			if !bytes.Equal(before, after) {
				t.Fatal("validation failure changed project")
			}
		})
	}
}

func TestMutationApplyReturnsChangedAndNoOpResults(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	args := []string{"update", "Example", "--file", project, "--version", "2.0.0", "--project", root, "--format", "json"}
	for _, expected := range []string{"changed", "unchanged"} {
		code, stdout, diagnostic := runCLIForTest(t, args...)
		if code != ExitSuccess {
			t.Fatalf("code=%d diagnostic=%s", code, diagnostic)
		}
		var result planDocument
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		if result.DryRun || result.Status != expected || result.Restore != "not-requested" {
			t.Fatalf("result=%+v", result)
		}
		if expected == "changed" && len(result.Changed) != 1 || expected == "unchanged" && len(result.Changed) != 0 {
			t.Fatalf("changed=%v", result.Changed)
		}
	}
}

func TestSearchFailurePreservesOutputAndReportsWarnings(t *testing.T) {
	root := t.TempDir()
	writeCLIProject(t, root, "App", `<Project />`)
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="test" value="https://packages.example.test/index.json"/></packageSources></configuration>`)
	output := filepath.Join(root, "results.json")
	mustWriteFile(t, output, "previous-results")
	var stdout, stderr bytes.Buffer
	runtime := cliRuntime{stdout: &stdout, stderr: &stderr, newService: func(context.Context, NugetSource) (*NugetService, error) { return nil, errors.New("offline") }}
	code := runtime.runSearch(context.Background(), []string{"Example", "--project", root, "--output", output})
	if code != ExitSource || !strings.Contains(stderr.String(), "source test unavailable") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	after, _ := os.ReadFile(output)
	if string(after) != "previous-results" || stdout.Len() != 0 {
		t.Fatal("fatal search replaced output")
	}
	stderr.Reset()
	code = runtime.runSearch(context.Background(), []string{"Example", "--project", root, "--source", "test", "--source", "typo"})
	if code != ExitSource || !strings.Contains(stderr.String(), `unknown --source "typo"`) {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestSearchHelpNeedsNoQuery(t *testing.T) {
	code, output, diagnostic := runCLIForTest(t, "search", "--help")
	if code != ExitSuccess || !strings.Contains(output, "Usage: guget search") || diagnostic != "" {
		t.Fatalf("code=%d output=%s diagnostic=%s", code, output, diagnostic)
	}
}

type failingCLIWriter struct{}

func (failingCLIWriter) Write([]byte) (int, error) { return 0, errors.New("closed result stream") }

func TestMutationReportingFailureExplainsCommittedChanges(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	var stderr bytes.Buffer
	runtime := cliRuntime{stdout: failingCLIWriter{}, stderr: &stderr}
	code := runtime.runMutation(context.Background(), "update", []string{"Example", "--project", root, "--file", project, "--version", "2.0.0", "--format", "json"})
	data, _ := os.ReadFile(project)
	if code != ExitWrite || !strings.Contains(string(data), `Version="2.0.0"`) || !strings.Contains(stderr.String(), "package changes were applied") {
		t.Fatalf("code=%d project=%s stderr=%s", code, data, stderr.String())
	}
}

func TestMutationRestoreFailureHasMachineResult(t *testing.T) {
	root := t.TempDir()
	project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
	var stdout, stderr bytes.Buffer
	runtime := cliRuntime{stdout: &stdout, stderr: &stderr, runCommand: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
		return errors.New("restore unavailable")
	}}
	code := runtime.runMutation(context.Background(), "update", []string{"Example", "--project", root, "--file", project, "--version", "2.0.0", "--restore", "--format", "jsonl"})
	var result planDocument
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if code != ExitRestore || result.Status != "changed" || result.Restore != "failed" || len(result.Changed) != 1 {
		t.Fatalf("code=%d result=%+v stderr=%s", code, result, stderr.String())
	}
}

func TestUnsupportedReadAndRestoreFlagsAreRejected(t *testing.T) {
	for _, args := range [][]string{
		{"restore", "--all", "--format", "json"},
		{"restore", "--all", "--output", "result.json"},
		{"restore", "--all", "--framework", "net8.0"},
		{"sources", "--source", "typo"},
		{"search", "Example", "--framework", "net8.0"},
	} {
		code, _, diagnostic := runCLIForTest(t, args...)
		if code != ExitUsage || !strings.Contains(diagnostic, "unknown flag") {
			t.Fatalf("args=%v code=%d diagnostic=%s", args, code, diagnostic)
		}
	}
}

func TestMutationOutputCannotOverwriteNuGetConfiguration(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		root := t.TempDir()
		project := writeCLIProject(t, root, "App", `<Project><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`)
		config := filepath.Join(root, "NuGet.Config")
		content := `<configuration><packageSources><clear /></packageSources></configuration>`
		if preexisting {
			mustWriteFile(t, config, content)
		}
		before, _ := os.ReadFile(project)
		code, _, diagnostic := runCLIForTest(t, "update", "Example", "--project", root, "--file", project, "--version", "2.0.0", "--output", config)
		if code != ExitWrite || !strings.Contains(diagnostic, "overwrite a workspace input") {
			t.Fatalf("code=%d diagnostic=%s", code, diagnostic)
		}
		after, _ := os.ReadFile(project)
		if !bytes.Equal(before, after) {
			t.Fatal("output refusal edited project")
		}
		data, err := os.ReadFile(config)
		if preexisting && (err != nil || string(data) != content) || !preexisting && !os.IsNotExist(err) {
			t.Fatalf("configuration changed: data=%s err=%v", data, err)
		}
	}
}

func canonicalTestOwnerPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
