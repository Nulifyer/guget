package main

import (
	"context"
	"path/filepath"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
)

// Consecutive saves to different files must not make the earlier file look
// like an external edit when the watcher reports both changes together, as it
// does after a held update key sweeps several projects.
func TestConsecutiveSavesToDifferentFilesDoNotReload(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "First", "First.csproj")
	second := filepath.Join(root, "Second", "Second.csproj")
	mustWriteFile(t, first, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
	mustWriteFile(t, second, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="local" value="./packages"/></packageSources></configuration>`)
	snapshot, err := scanWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := NewApp(root, snapshot, nil, BuiltFlags{SortBy: "name:asc"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.lifecycle = ctx
	m.ctx.Width, m.ctx.Height = 140, 40
	m.focus = focusPackages
	for _, id := range []string{"A", "B"} {
		m.ctx.Results[id] = nugetResult{pkg: &PackageInfo{ID: id, Versions: []PackageVersion{
			{SemVer: ParseSemVer("2.0.0")},
			{SemVer: ParseSemVer("1.0.0")},
		}}}
	}
	m.rebuildPackageRows()
	m.refreshDetail()
	messages := make(chan ui.Event, 30)
	m.SetSender(func(msg ui.Event) { messages <- msg })

	for _, name := range []string{"A", "B"} {
		m.selectPackageByName(name)
		executeActionCmd(t, m, m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})))
		if m.ctx.Saving || m.ctx.Reloading {
			t.Fatalf("update of %s did not reconcile locally", name)
		}
	}
	states := make(map[string]watchedFileState)
	for _, path := range []string{first, second} {
		state, exists, err := readWatchedFileState(path)
		if err != nil || !exists {
			t.Fatalf("read %s: %v", path, err)
		}
		states[path] = state
	}
	m.handleWatchedWorkspaceChange(watchedWorkspaceChangeMsg{paths: []string{first, second}, states: states})
	if m.ctx.Reloading {
		t.Fatal("guget's own consecutive saves were treated as external disk changes")
	}

	// Content guget did not write and is not displaying still reloads, whether
	// it is a new edit or a revert to the content before the save.
	for name, content := range map[string]string{
		"external edit":   `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="9.0.0"/></ItemGroup></Project>`,
		"external revert": `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`,
	} {
		t.Run(name, func(t *testing.T) {
			if m.ctx.Reloading {
				finishActionReload(t, m, messages)
			}
			mustWriteFile(t, first, content)
			state, exists, err := readWatchedFileState(first)
			if err != nil || !exists {
				t.Fatalf("read %s: %v", first, err)
			}
			m.handleWatchedWorkspaceChange(watchedWorkspaceChangeMsg{paths: []string{first}, states: map[string]watchedFileState{first: state}})
			if !m.ctx.Reloading {
				t.Fatal("an external change to a file guget saved earlier did not reload")
			}
			finishActionReload(t, m, messages)
		})
	}
}
