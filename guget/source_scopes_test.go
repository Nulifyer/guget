package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
)

// isolateNuGetUser keeps user and machine NuGet configuration out of a test.
func isolateNuGetUser(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	t.Setenv("DOTNET_CLI_HOME", home)
	t.Setenv("NUGET_COMMON_APPLICATION_DATA", t.TempDir())
	t.Setenv("GUGET_CONFIG", filepath.Join(home, "guget.toml"))
}

const contosoProject = `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Contoso.Lib" Version="1.0.0" /></ItemGroup></Project>`

// scopedWorkspace is two projects with their own NuGet configuration. The
// private feed has Contoso.Lib 1.0.0 and 1.1.0, the public feed 99.0.0 (and
// 1.1.0 when publicHasShared). secure/ uses private (with mapping: private
// and public, mapping Contoso.* to private); other/ uses only public. Secure
// references Contoso.Lib 1.0.0, and Other does when otherReferences.
func scopedWorkspace(t *testing.T, mapping, otherReferences, publicHasShared bool) (root, secure, other string, feeds *testFeeds) {
	t.Helper()
	isolateNuGetUser(t)
	feeds = newTestFeeds(t)
	feeds.add("private", "Contoso.Lib", testLeaf{Version: "1.0.0"}, testLeaf{Version: "1.1.0"})
	feeds.add("public", "Contoso.Lib", testLeaf{Version: "99.0.0"})
	if publicHasShared {
		feeds.add("public", "Contoso.Lib", testLeaf{Version: "1.1.0"})
	}
	feeds.add("public", "Shared.Lib", testLeaf{Version: "1.0.0"}, testLeaf{Version: "2.0.0"})
	root = t.TempDir()
	if mapping {
		mustWriteFile(t, filepath.Join(root, "secure", "nuget.config"), feeds.config([]string{"private", "public"}, map[string][]string{"private": {"Contoso.*"}, "public": {"*"}}))
	} else {
		mustWriteFile(t, filepath.Join(root, "secure", "nuget.config"), feeds.config([]string{"private"}, nil))
	}
	mustWriteFile(t, filepath.Join(root, "other", "nuget.config"), feeds.config([]string{"public"}, nil))
	secure = writeCLIProject(t, filepath.Join(root, "secure"), "Secure", contosoProject)
	otherProject := `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`
	if otherReferences {
		otherProject = contosoProject
	}
	other = writeCLIProject(t, filepath.Join(root, "other"), "Other", otherProject)
	return root, secure, other, feeds
}

func forEachSourceScoping(t *testing.T, test func(t *testing.T, mapping bool)) {
	for _, mapping := range []bool{true, false} {
		name := "without mapping"
		if mapping {
			name = "with mapping"
		}
		t.Run(name, func(t *testing.T) { test(t, mapping) })
	}
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
	if got, want := strings.Join(sourceSummary(resolved.Sources), " "), "shared=https://shared.test/index.json tools=https://tools.test/index.json"; got != want {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	mapping := resolved.Mapping
	shared, tools := resolved.Sources[0], resolved.Sources[1]
	// Each project uses only its own sources and mapping: the app project
	// cannot use the tools feed at all, and the tools project gets Tools.*
	// packages only from the tools feed, as its mapping's most specific
	// pattern decides.
	for _, check := range []struct {
		project string
		source  NugetSource
		id      string
		allowed bool
	}{
		{app, shared, "Tools.Cli", true},
		{app, tools, "Tools.Cli", false},
		{app, shared, "Other.Package", true},
		{tool, shared, "Tools.Cli", false},
		{tool, tools, "Tools.Cli", true},
		{tool, tools, "Other.Package", false},
		{tool, shared, "Other.Package", true},
	} {
		if got := mapping.AllowsFor(check.project, check.source.Name, check.source.URL, check.id); got != check.allowed {
			t.Errorf("AllowsFor(%s, %s, %s) = %v, want %v", filepath.Base(check.project), check.source.Name, check.id, got, check.allowed)
		}
	}
	// The workspace view (sources listing, All Projects search) is the union.
	if !mapping.Allows(shared.Name, shared.URL, "Tools.Cli") || mapping.Allows(tools.Name, tools.URL, "Other.Package") {
		t.Fatal("workspace view is not the union of project scopes")
	}
	if got := mapping.Patterns(tools); strings.Join(got, ",") != "tools.*" {
		t.Fatalf("Patterns(tools) = %v", got)
	}
}

func TestListOutdatedAssessesEachProjectFromItsOwnSources(t *testing.T) {
	forEachSourceScoping(t, func(t *testing.T, mapping bool) {
		root, secure, other, _ := scopedWorkspace(t, mapping, true, false)
		code, stdout, stderr := runHermetic(t, "list", "--project", root, "--outdated", "--format", "json")
		if code != ExitSuccess && code != ExitPartial {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
		targets := map[string]string{}
		for _, row := range decodeList(t, stdout).Packages {
			if row.UpdateTarget != nil {
				targets[row.ProjectPath] = *row.UpdateTarget
			}
		}
		if targets[secure] != "1.1.0" || targets[other] != "99.0.0" {
			t.Fatalf("update targets = %v; want Secure 1.1.0 from its private feed and Other 99.0.0 from public\n%s", targets, stderr)
		}
	})
}

func TestUpdateLatestUsesOnlyTheProjectsOwnSources(t *testing.T) {
	forEachSourceScoping(t, func(t *testing.T, mapping bool) {
		root, secure, other, _ := scopedWorkspace(t, mapping, true, false)
		code, stdout, stderr := runHermetic(t, "update", "Contoso.Lib", "--latest", "--file", secure, "--dry-run", "--project", root, "--format", "json")
		if code != ExitSuccess {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
		var plan planDocument
		if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
			t.Fatal(err)
		}
		if len(plan.Changes) != 1 || plan.Changes[0].Version != "1.1.0" {
			t.Fatalf("secure plan = %+v; want 1.1.0, never the public 99.0.0", plan.Changes)
		}

		code, stdout, stderr = runHermetic(t, "update", "Contoso.Lib", "--latest", "--all", "--dry-run", "--project", root, "--format", "json")
		if code != ExitSuccess {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
		plan = planDocument{}
		if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
			t.Fatal(err)
		}
		versions := map[string]string{}
		for _, change := range plan.Changes {
			versions[filepath.Base(change.Path)] = change.Version
		}
		if versions[filepath.Base(secure)] != "1.1.0" || versions[filepath.Base(other)] != "99.0.0" {
			t.Fatalf("--all plan = %v", versions)
		}
	})
}

func TestCLIMetadataFetchesOncePerSourceSet(t *testing.T) {
	isolateNuGetUser(t)
	feeds := newTestFeeds(t)
	feeds.add("feed", "Example", testLeaf{Version: "1.0.0"}, testLeaf{Version: "2.0.0"})
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), feeds.config([]string{"feed"}, nil))
	project := `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Example" Version="1.0.0" /></ItemGroup></Project>`
	writeCLIProject(t, root, "One", project)
	writeCLIProject(t, root, "Two", project)
	code, _, stderr := runHermetic(t, "list", "--project", root, "--metadata", "--format", "json")
	if code != ExitSuccess && code != ExitPartial {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	feeds.mu.Lock()
	defer feeds.mu.Unlock()
	if feeds.requests["feed"] != 1 {
		t.Fatalf("one source configuration made %d registration requests for one package, want 1", feeds.requests["feed"])
	}
}

func TestScopedMetadataSharesFetchesForTheSameSources(t *testing.T) {
	root, secure, other, feeds := scopedWorkspace(t, true, true, false)
	snapshot, err := scanWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	services := feeds.services(t, "private", "public")
	mapping := snapshot.SourceMapping
	scopes := mapping.scopesForPaths(secure, other)
	if len(scopes) != 2 {
		t.Fatalf("scopes = %v", scopes)
	}
	// Secure maps Shared.Lib to public and Other has only public: one fetch.
	byScope := fetchScopedPackageMetadata(context.Background(), services, mapping, "Shared.Lib", scopes)
	feeds.mu.Lock()
	requests := feeds.requests["public"] + feeds.requests["private"]
	feeds.mu.Unlock()
	if requests != 1 || byScope[scopes[0]].Info == nil || byScope[scopes[0]].Info != byScope[scopes[1]].Info {
		t.Fatalf("identical source sets made %d requests: %+v", requests, byScope)
	}
}

// fetchTUIMetadata loads root into an App and runs its package metadata
// fetch to completion.
func fetchTUIMetadata(t *testing.T, root string) *App {
	t.Helper()
	snapshot, err := loadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewApp(root, snapshot, nil, BuiltFlags{SortBy: "status:asc"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.lifecycle = ctx
	m.ctx.Width, m.ctx.Height = 140, 40
	messages := make(chan ui.Event, 64)
	m.SetSender(func(msg ui.Event) { messages <- msg })
	names := distinctPackageNames(snapshot.ParsedProjects, snapshot.PropsProjects)
	m.startPackageFetch(names, true)
	deadline := time.After(10 * time.Second)
	for ready := 0; ready < len(names); {
		select {
		case msg := <-messages:
			if _, ok := msg.(packageReadyMsg); ok {
				ready++
			}
			m.HandleEvent(msg)
		case <-deadline:
			t.Fatal("metadata fetch did not finish")
		}
	}
	return m
}

func resultVersions(result nugetResult) string {
	if result.pkg == nil {
		return "error: " + result.err.Error()
	}
	var versions []string
	for _, v := range result.pkg.Versions {
		versions = append(versions, v.SemVer.String())
	}
	return strings.Join(versions, " ")
}

func TestTUIResultsHoldOnlyVersionsEveryReferencingScopeCanUse(t *testing.T) {
	forEachSourceScoping(t, func(t *testing.T, mapping bool) {
		root, _, _, _ := scopedWorkspace(t, mapping, false, false)
		m := fetchTUIMetadata(t, root)
		result := m.ctx.Results["Contoso.Lib"]
		if got := resultVersions(result); got != "1.1.0 1.0.0" {
			t.Fatalf("Secure-only package versions = %s; the public 99.0.0 is not Secure's", got)
		}
		m.rebuildPackageRows()
		row := m.rowByName("Contoso.Lib")
		if row == nil || row.info != result.pkg {
			t.Fatalf("Contoso.Lib row = %+v", row)
		}
		if row.updateTarget == nil || row.updateTarget.SemVer.String() != "1.1.0" {
			t.Fatalf("row update target = %v, want 1.1.0", row.updateTarget)
		}

		root, _, _, _ = scopedWorkspace(t, mapping, true, true)
		m = fetchTUIMetadata(t, root)
		if got := resultVersions(m.ctx.Results["Contoso.Lib"]); got != "1.1.0" {
			t.Fatalf("versions shared by Secure and Other = %s, want only 1.1.0", got)
		}

		root, _, _, _ = scopedWorkspace(t, mapping, true, false)
		m = fetchTUIMetadata(t, root)
		if result := m.ctx.Results["Contoso.Lib"]; result.pkg != nil || result.err == nil {
			t.Fatalf("no common version, but Results = %s", resultVersions(result))
		}
	})
}

func TestSearchAddRefusesASourceTheTargetProjectCannotUse(t *testing.T) {
	forEachSourceScoping(t, func(t *testing.T, mapping bool) {
		root, secure, other, feeds := scopedWorkspace(t, mapping, false, false)
		snapshot, err := loadWorkspace(root)
		if err != nil {
			t.Fatal(err)
		}
		m := NewApp(root, snapshot, nil, BuiltFlags{SortBy: "status:asc"})
		m.lifecycle = context.Background()
		var secureProject, otherProject *ParsedProject
		for _, project := range snapshot.ParsedProjects {
			switch project.FilePath {
			case secure:
				secureProject = project
			case other:
				otherProject = project
			}
		}
		private := feeds.source("private")

		m.selectProjectByPath(other)
		if m.searchAllows()(private.Name, private.URL, "Contoso.Lib") {
			t.Fatal("search offers Other a result from a source its configuration does not list")
		}
		m.selectProjectByPath(secure)
		if !m.searchAllows()(private.Name, private.URL, "Contoso.Lib") {
			t.Fatal("search hides Secure's own source")
		}
		m.selectProjectByPath("")
		if !m.searchAllows()(private.Name, private.URL, "Contoso.Lib") {
			t.Fatal("All Projects search hides a source some project may use")
		}

		before, _ := os.ReadFile(other)
		m.search.fetchedInfo = &PackageInfo{ID: "Contoso.Lib", Versions: []PackageVersion{{SemVer: ParseSemVer("1.1.0")}}}
		m.search.fetchedSource = "private"
		target := AddTarget{Kind: AddTargetProject, FilePath: other}
		m.addPackageToTargets("Contoso.Lib", "1.1.0", []mutationTarget{{project: otherProject, addTarget: &target}})
		if !m.ctx.StatusIsErr || !strings.Contains(m.ctx.StatusLine, "Not added") || !strings.Contains(m.ctx.StatusLine, "private") || m.ctx.Saving {
			t.Fatalf("add to Other from private: status=%q saving=%v", m.ctx.StatusLine, m.ctx.Saving)
		}
		if after, _ := os.ReadFile(other); string(after) != string(before) {
			t.Fatal("refused add wrote the project")
		}
		if m.addSourceProblem("Contoso.Lib", "1.1.0", []mutationTarget{{project: secureProject}}) != "" {
			t.Fatal("add to Secure from its own source refused")
		}
	})
}
