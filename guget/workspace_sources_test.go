package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const minimalProject = `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`

func parseTestProjects(t *testing.T, paths ...string) []*ParsedProject {
	t.Helper()
	var projects []*ParsedProject
	for _, path := range paths {
		project, err := ParseCsproj(path)
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, project)
	}
	return projects
}

func sourceSummary(sources []NugetSource) []string {
	var summary []string
	for _, source := range sources {
		summary = append(summary, source.Name+"="+source.URL)
	}
	return summary
}

func TestResolveWorkspaceSourcesScopesProjectsByTheirOwnConfiguration(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "nuget.config"), `<configuration><packageSources><clear/><add key="shared" value="https://shared.test/index.json"/></packageSources></configuration>`)
	mustWriteFile(t, filepath.Join(root, "tools", "nuget.config"), `<configuration><packageSources><add key="tools" value="https://tools.test/index.json"/></packageSources>
		<packageSourceMapping><packageSource key="tools"><package pattern="Tools.*"/></packageSource><packageSource key="shared"><package pattern="*"/></packageSource></packageSourceMapping></configuration>`)
	app := filepath.Join(root, "app", "App.csproj")
	tool := filepath.Join(root, "tools", "Tool", "Tool.csproj")
	mustWriteFile(t, app, minimalProject)
	mustWriteFile(t, tool, minimalProject)

	resolved, err := resolveWorkspaceSources(parseTestProjects(t, app, tool))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sourceSummary(resolved.Sources), []string{"shared=https://shared.test/index.json", "tools=https://tools.test/index.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	mapping := resolved.Mapping
	shared, tools := resolved.Sources[0], resolved.Sources[1]
	// The app project has no mapping, so the shared source may supply anything;
	// the tools project limits its own feed to Tools.* packages.
	for _, check := range []struct {
		source  NugetSource
		id      string
		allowed bool
	}{
		{shared, "Tools.Cli", true},
		{tools, "Tools.Cli", true},
		{tools, "Other.Package", false},
		{shared, "Other.Package", true},
	} {
		if got := mapping.Allows(check.source.Name, check.source.URL, check.id); got != check.allowed {
			t.Errorf("Allows(%s, %s) = %v, want %v", check.source.Name, check.id, got, check.allowed)
		}
	}
	if got := mapping.Patterns(tools); !reflect.DeepEqual(got, []string{"tools.*"}) {
		t.Fatalf("Patterns(tools) = %v", got)
	}
}

func TestResolveWorkspaceSourcesAppliesProjectRestoreSources(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "nuget.config"), `<configuration><packageSources><clear/>
		<add key="company" value="https://company.test/index.json"/><add key="nuget.org" value="https://api.nuget.org/v3/index.json"/></packageSources>
		<packageSourceCredentials><company><add key="Username" value="ci"/><add key="ClearTextPassword" value="company-token"/></company></packageSourceCredentials></configuration>`)
	mustWriteFile(t, filepath.Join(root, "Directory.Build.props"), `<Project><PropertyGroup><RestoreSources>https://company.test/index.json</RestoreSources></PropertyGroup></Project>`)
	project := filepath.Join(root, "src", "App.csproj")
	mustWriteFile(t, project, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup>
		<TargetFramework>net8.0</TargetFramework>
		<LocalFeed>../feed</LocalFeed>
		<RestoreSources>$(RestoreSources);$(LocalFeed)</RestoreSources>
		<RestoreAdditionalProjectSources>$(MSBuildThisFileDirectory)unresolved</RestoreAdditionalProjectSources>
	</PropertyGroup></Project>`)

	resolved, err := resolveWorkspaceSources(parseTestProjects(t, project))
	if err != nil {
		t.Fatal(err)
	}
	feed := filepath.Join(root, "feed")
	want := []NugetSource{
		{Name: "company", URL: "https://company.test/index.json", Username: "ci", Password: "company-token"},
		{Name: feed, URL: feed},
	}
	if !reflect.DeepEqual(resolved.Sources, want) {
		t.Fatalf("sources = %+v, want %+v", resolved.Sources, want)
	}
}

func TestResolveWorkspaceSourcesExpandsEnvironmentCredentials(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GUGET_TEST_FEED_USER", "ci-user")
	t.Setenv("GUGET_TEST_FEED_TOKEN", "config-token-value")
	t.Setenv("NuGetPackageSourceCredentials_env", "Username=env-user;Password=env-token-value")
	mustWriteFile(t, filepath.Join(root, "nuget.config"), `<configuration><packageSources><clear/>
		<add key="config" value="https://config.test/index.json"/><add key="env" value="https://env.test/index.json"/></packageSources>
		<packageSourceCredentials><config><add key="Username" value="%GUGET_TEST_FEED_USER%"/><add key="ClearTextPassword" value="%GUGET_TEST_FEED_TOKEN%"/></config></packageSourceCredentials></configuration>`)
	project := filepath.Join(root, "App.csproj")
	mustWriteFile(t, project, minimalProject)

	resolved, err := resolveWorkspaceSources(parseTestProjects(t, project))
	if err != nil {
		t.Fatal(err)
	}
	want := []NugetSource{
		{Name: "config", URL: "https://config.test/index.json", Username: "ci-user", Password: "config-token-value"},
		{Name: "env", URL: "https://env.test/index.json", Username: "env-user", Password: "env-token-value"},
	}
	if !reflect.DeepEqual(resolved.Sources, want) {
		t.Fatalf("sources = %+v, want %+v", resolved.Sources, want)
	}
	if message := redactLogMessage("config-token-value env-token-value"); strings.Contains(message, "token-value") {
		t.Fatalf("resolved passwords must be redacted from logs, got %q", message)
	}
}

func TestResolveWorkspaceSourcesRejectsInvalidConfigurationSafely(t *testing.T) {
	for index, content := range []string{
		"",
		`<configuration><packageSources><clear/><add key="private" value="https://private.example/index.json"/>`,
		`<configuration/><configuration/>`,
		`<configuration/>credential-secret`,
		`<credential-secret/>`,
		`<configuration><packageSourceCredentials><private><add key="ClearTextPassword" value="credential-secret"/></private></packageSourceCredentials></configuration>`,
	} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "nuget.config")
			mustWriteFile(t, config, content)
			project := filepath.Join(root, "App.csproj")
			mustWriteFile(t, project, minimalProject)
			resolved, err := resolveWorkspaceSources(parseTestProjects(t, project))
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if !strings.Contains(err.Error(), config) || strings.Contains(err.Error(), "credential-secret") || strings.Contains(err.Error(), "private.example") {
				t.Fatalf("unsafe or unhelpful diagnostic: %v", err)
			}
			// The consumed revision lets the scanner retry a save in progress
			// instead of reporting it as a broken configuration.
			if resolved.InputRevisions[config] != sha256.Sum256([]byte(content)) {
				t.Fatal("consumed revision of the invalid configuration was not reported")
			}
		})
	}
}

func TestScanWorkspaceSourcesMalformedSaveThenRepair(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "NuGet.Config")
	valid := `<configuration><packageSources><clear/><add key="local" value="./packages"/></packageSources></configuration>`
	mustWriteFile(t, config, valid)
	mustWriteFile(t, filepath.Join(root, "App.csproj"), minimalProject)
	first, err := scanWorkspace(root)
	if err != nil || len(first.Sources) != 1 || first.Sources[0].URL != filepath.Join(root, "packages") {
		t.Fatalf("initial scan: %+v, %v", first, err)
	}
	if first.InputRevisions[config] != sha256.Sum256([]byte(valid)) {
		t.Fatal("consumed configuration revision missing")
	}
	mustWriteFile(t, config, `<configuration><packageSources>`)
	if _, err := scanWorkspace(root); err == nil || !strings.Contains(err.Error(), "incomplete source configuration") {
		t.Fatalf("truncated save produced a usable workspace: %v", err)
	}
	mustWriteFile(t, config, valid)
	repaired, err := scanWorkspace(root)
	if err != nil || !reflect.DeepEqual(repaired.Sources, first.Sources) {
		t.Fatalf("repaired scan: %+v, %v", repaired, err)
	}
}

func TestResolveWorkspaceSourcesHonorsExplicitlyEmptySources(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), "\xef\xbb\xbf"+`<?xml version="1.0" encoding="utf-8"?><configuration><packageSources><clear/></packageSources></configuration><!-- saved by editor -->`)
	project := filepath.Join(root, "App.csproj")
	mustWriteFile(t, project, minimalProject)
	resolved, err := resolveWorkspaceSources(parseTestProjects(t, project))
	if err != nil || len(resolved.Sources) != 0 {
		t.Fatalf("explicitly cleared sources must stay empty: %+v, %v", resolved.Sources, err)
	}
}

func TestResolveWorkspaceSourcesReportsNonRegularConfigurationOnce(t *testing.T) {
	root := t.TempDir()
	upper := filepath.Join(root, "NuGet.Config")
	if err := os.Mkdir(upper, 0o755); err != nil {
		t.Fatal(err)
	}
	lower := filepath.Join(root, "nuget.config")
	upperInfo, _ := os.Stat(upper)
	if lowerInfo, err := os.Stat(lower); err != nil || !os.SameFile(upperInfo, lowerInfo) {
		if err := os.Symlink(upper, lower); err != nil {
			t.Skipf("configuration alias unavailable: %v", err)
		}
	}
	project := filepath.Join(root, "App.csproj")
	mustWriteFile(t, project, minimalProject)
	_, err := resolveWorkspaceSources(parseTestProjects(t, project))
	if err == nil || strings.Count(err.Error(), "not a regular file") != 1 {
		t.Fatalf("aliased non-regular configuration should be diagnosed once: %v", err)
	}
}

func TestAuthTransportSurfacesForbiddenWithoutProvider(t *testing.T) {
	t.Setenv("NUGET_PLUGIN_PATHS", "")
	t.Setenv("NUGET_NETCORE_PLUGIN_PATHS", "")
	t.Setenv("NUGET_CREDENTIALPROVIDER_PLUGIN_PATHS", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	transport := newAuthTransport(NugetSource{Name: "feed", URL: server.URL, Username: "u", Password: "p"})
	request, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden || sawAuth == "" {
		t.Fatalf("status = %d, auth sent = %v; want the 403 surfaced after Basic auth", response.StatusCode, sawAuth != "")
	}
}

func TestAuthTransportDefaultsGitHubPackagesUser(t *testing.T) {
	github := newAuthTransport(NugetSource{Name: "github", URL: "https://nuget.pkg.github.com/owner/index.json"})
	if github.username != "nobody" || github.password != "" {
		t.Fatalf("GitHub Packages default credentials = %q/%q", github.username, github.password)
	}
	configured := newAuthTransport(NugetSource{Name: "github", URL: "https://nuget.pkg.github.com/owner/index.json", Username: "me", Password: "token"})
	if configured.username != "me" {
		t.Fatalf("configured GitHub credentials were replaced: %q", configured.username)
	}
	if other := newAuthTransport(NugetSource{Name: "other", URL: "https://feed.test/index.json"}); other.username != "" {
		t.Fatalf("non-GitHub source got default username %q", other.username)
	}
}
