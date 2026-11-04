package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestProjectFrameworkLabelsStayStableAcrossRedraws(t *testing.T) {
	frameworks := NewSet[TargetFramework]()
	for _, framework := range []string{"netstandard2.1", "netstandard2.0"} {
		frameworks.Add(ParseTargetFramework(framework))
	}
	project := &ParsedProject{FileName: "SQLLinger.csproj", TargetFrameworks: frameworks}
	app := NewApp(".", &workspaceSnapshot{ParsedProjects: []*ParsedProject{project}}, nil, BuiltFlags{})
	app.ctx.Width, app.ctx.Height = 140, 40
	const want = "netstandard2.0, netstandard2.1"
	var firstPanel string
	for redraw := range 256 {
		if got := app.projects.items[1].Description(); got != want {
			t.Fatalf("redraw %d: framework label=%q, want %q", redraw, got, want)
		}
		// Narrow panels show only the first framework. This is where a change
		// in map iteration order looks like a framework version changing.
		got := app.renderProjectPanel(30)
		if redraw == 0 {
			firstPanel = got
			if !strings.Contains(got, "netstandard2.0") {
				t.Fatal("narrow panel hides the first target framework")
			}
		}
		if got != firstPanel {
			t.Fatalf("redraw %d: unchanged project produced a different panel", redraw)
		}
	}
	if frameworks.Len() != 2 {
		t.Fatal("rendering changed the project's frameworks")
	}
}

func TestPackageAuthorLabelsStayStableAcrossRedraws(t *testing.T) {
	authors := NewSet[string]()
	for _, author := range []string{"Zoe", "Ada", "Mina"} {
		authors.Add(author)
	}
	row := packageRow{info: &PackageInfo{ID: "Example", Authors: authors}}
	app := NewApp(".", &workspaceSnapshot{}, nil, BuiltFlags{})
	for redraw := range 256 {
		if got := app.renderDetailHeader(row, 80); !strings.Contains(got, "Ada, Mina, Zoe") {
			t.Fatalf("redraw %d: author label is not stable: %q", redraw, got)
		}
	}
}

func TestProjectListsGroupByParentAndPreserveExistingOrder(t *testing.T) {
	project := func(dir, name string) *ParsedProject {
		return &ParsedProject{FilePath: filepath.Join("workspace", dir, name), FileName: name}
	}
	apiZ := project("src/api", "Zeta.csproj")
	apiA := project("src/api", "Alpha.fsproj")
	workerZ := project("src/worker", "Zeta.csproj")
	workerA := project("src/worker", "Alpha.vbproj")
	testAPI := project("tests/api", "Api.Tests.csproj")
	apiProps := project("src/api", "Directory.Build.props")
	workerProps := project("src/worker", "Dependencies.props")
	rootProps := project("", "Directory.Packages.props")
	parsed := []*ParsedProject{workerZ, apiZ, testAPI, apiA, workerA}
	props := []*ParsedProject{apiProps, rootProps, workerProps}
	originalParsed, originalProps := slices.Clone(parsed), slices.Clone(props)
	want := []*ParsedProject{rootProps, apiZ, apiA, apiProps, workerZ, workerA, workerProps, testAPI}
	app := NewApp("workspace", &workspaceSnapshot{ParsedProjects: parsed, PropsProjects: props}, nil, BuiltFlags{})
	if len(app.projects.items) != len(want)+1 || app.projects.items[0].project != nil {
		t.Fatal("All Projects must stay first")
	}
	for i, p := range want {
		if got := app.projects.items[i+1].project; got != p {
			t.Fatalf("project %d = %s, want %s", i, got.FilePath, p.FilePath)
		}
	}
	app.openProjectPicker("Example.Package", "1.0.0")
	if len(app.projectPick.items) != len(want) {
		t.Fatal("project picker lost a project")
	}
	for i, p := range want {
		if got := app.projectPick.items[i].project; got != p {
			t.Fatalf("picker project %d = %s, want %s", i, got.FilePath, p.FilePath)
		}
	}
	if !slices.Equal(parsed, originalParsed) || !slices.Equal(props, originalProps) {
		t.Fatal("display ordering mutated workspace project slices")
	}
}

func TestEmptyProjectListKeepsAllProjectsEntry(t *testing.T) {
	items := buildProjectItems(nil, nil)
	if len(items) != 1 || items[0].project != nil {
		t.Fatal("empty workspace lost All Projects entry")
	}
}
