package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
)

func newWorkspaceTestApp(t *testing.T, files map[string]string) (*App, string) {
	t.Helper()
	isolateNuGetUser(t)
	root := t.TempDir()
	for name, content := range files {
		mustWriteFile(t, filepath.Join(root, name), content)
	}
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="local" value="./packages"/></packageSources></configuration>`)
	snapshot, err := scanWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewApp(root, snapshot, nil, BuiltFlags{SortBy: "status:asc"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.lifecycle = ctx
	m.ctx.Width, m.ctx.Height = 140, 40
	m.focus = focusPackages
	m.rebuildPackageRows()
	m.SetSender(func(ui.Event) {})
	return m, root
}

// d on a row of a shared props file that holds no reference used to open the
// confirmation and then answer only "<package> unchanged". It now says why
// before asking, and still removes a reference the file does hold.
func TestRemoveOnSharedPropsRowExplainsWhatItCannotRemove(t *testing.T) {
	m, root := newWorkspaceTestApp(t, map[string]string{
		"Directory.Packages.props": `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><GlobalPackageReference Include="G" Version="1.0.0"/><PackageVersion Include="Polly" Version="8.4.0"/></ItemGroup></Project>`,
		"App/App.csproj":           `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Polly"/></ItemGroup></Project>`,
	})
	press := func() { m.packages.HandleKey(ui.KeyPress(ui.Key{Code: 'd'})) }

	m.selectProjectByPath(filepath.Join(root, "Directory.Packages.props"))
	if selected := m.selectedProject(); selected == nil || !m.isPropsProject(selected) {
		t.Fatal("the shared props file is not selectable")
	}
	m.rebuildPackageRows()

	m.selectPackageByName("Polly")
	press()
	if m.confirmRemove.active || !m.ctx.StatusIsErr || m.ctx.StatusLine != "▲ Not removed: Directory.Packages.props only sets Polly's version; remove its reference from a project" {
		t.Fatalf("version-only row: confirm=%v status=%q", m.confirmRemove.active, m.ctx.StatusLine)
	}

	m.selectPackageByName("G")
	press()
	if m.confirmRemove.active || m.ctx.StatusLine != "▲ Not removed: G is a global reference; remove it from All Projects" {
		t.Fatalf("global row: confirm=%v status=%q", m.confirmRemove.active, m.ctx.StatusLine)
	}

	// A narrow terminal points at the log, which has the whole reason.
	m.ctx.Width = 80
	m.selectPackageByName("Polly")
	press()
	if !strings.HasPrefix(m.ctx.StatusLine, "▲ Not removed (l for logs): Directory.Packages.props only sets") {
		t.Fatalf("narrow status %q", m.ctx.StatusLine)
	}
	m.ctx.Width = 140

	// The project's own reference and All Projects still ask to confirm.
	m.selectProjectByPath(filepath.Join(root, "App", "App.csproj"))
	m.rebuildPackageRows()
	m.selectPackageByName("Polly")
	press()
	if !m.confirmRemove.active {
		t.Fatalf("project reference was refused: %q", m.ctx.StatusLine)
	}
	m.confirmRemove.active = false
	m.selectProjectByPath("")
	m.rebuildPackageRows()
	m.selectPackageByName("G")
	press()
	if !m.confirmRemove.active {
		t.Fatalf("All Projects global removal was refused: %q", m.ctx.StatusLine)
	}
}

func TestRemoveOnSharedPropsRowWithAReferenceStillConfirms(t *testing.T) {
	m, root := newWorkspaceTestApp(t, map[string]string{
		"Directory.Build.props": `<Project><ItemGroup><PackageReference Include="S" Version="1.0.0"/></ItemGroup></Project>`,
		"App/App.csproj":        `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`,
	})
	m.selectProjectByPath(filepath.Join(root, "Directory.Build.props"))
	if selected := m.selectedProject(); selected == nil || !m.isPropsProject(selected) {
		t.Skip("the props file is not listed as a project in this layout")
	}
	m.rebuildPackageRows()
	m.selectPackageByName("S")
	m.packages.HandleKey(ui.KeyPress(ui.Key{Code: 'd'}))
	if !m.confirmRemove.active {
		t.Fatalf("a reference the props file holds was refused: %q", m.ctx.StatusLine)
	}
}
