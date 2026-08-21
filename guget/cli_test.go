package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
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
	if !plan.DryRun || len(plan.Changes) != 1 || plan.Changes[0].Path != project {
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
	if code != ExitPartial || stdout != "" || stderr != "" {
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
