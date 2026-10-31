package main

import (
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
