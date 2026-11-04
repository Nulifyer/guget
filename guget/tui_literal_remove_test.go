package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
)

func TestDeclaredRemovalDoesNotStartSDK(t *testing.T) {
	for _, kind := range []string{"project", "inherited-all", "central"} {
		t.Run(kind, func(t *testing.T) {
			m, _, _ := newActionTestApp(t, "")
			root := t.TempDir()
			one, two := filepath.Join(root, "One.csproj"), filepath.Join(root, "Two.csproj")
			reference := `<PackageReference Include="A" Version="1.0.0"/>`
			body := `<ItemGroup>` + reference + `</ItemGroup>`
			owner := one
			if kind == "inherited-all" {
				owner = filepath.Join(root, "Shared.props")
				body = `<Import Project="Shared.props"/>`
				mustWriteFile(t, owner, `<Project><ItemGroup>`+reference+`</ItemGroup></Project>`)
			}
			if kind == "central" {
				owner = filepath.Join(root, "Directory.Packages.props")
				body = `<ItemGroup><PackageReference Include="A"/></ItemGroup>`
				mustWriteFile(t, owner, `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="A" Version="1.0.0"/></ItemGroup></Project>`)
			}
			document := `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup>` + body + `</Project>`
			mustWriteFile(t, one, document)
			mustWriteFile(t, two, document)
			snapshot, err := scanWorkspace(root)
			if err != nil {
				t.Fatal(err)
			}
			m.applyWorkspaceSnapshot(snapshot)
			m.selectProjectByPath(one)
			if kind == "inherited-all" {
				m.projects.cursor = 0
			}
			m.rebuildPackageRows()
			m.selectPackageByName("A")
			ownerBefore, _ := os.ReadFile(owner)
			twoBefore, _ := os.ReadFile(two)
			t.Setenv("PATH", t.TempDir())
			m.HandleEvent(ui.KeyPress(ui.Key{Code: 'd'}))
			if !m.confirmRemove.active {
				t.Fatal("remove confirmation did not open")
			}
			executeActionCmd(t, m, m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyEnter})))
			if m.ctx.StatusIsErr || m.ctx.Saving || m.ctx.Reloading || !strings.Contains(m.ctx.StatusLine, "Removed A") {
				t.Fatalf("remove did not finish locally: %q", m.ctx.StatusLine)
			}
			ownerAfter, _ := os.ReadFile(owner)
			twoAfter, _ := os.ReadFile(two)
			if kind == "central" {
				oneAfter, _ := os.ReadFile(one)
				if string(ownerAfter) != string(ownerBefore) || string(twoAfter) != string(twoBefore) || strings.Contains(string(oneAfter), `Include="A"`) {
					t.Fatal("central removal changed shared version or another consumer")
				}
			} else if kind == "inherited-all" {
				if strings.Contains(string(ownerAfter), `Include="A"`) || string(twoAfter) != string(twoBefore) {
					t.Fatal("shared removal did not edit only shared declaration")
				}
				for _, project := range m.ctx.ParsedProjects {
					for ref := range project.Packages {
						if ref.Name == "A" {
							t.Fatal("shared consumer retained removed reference")
						}
					}
				}
			} else if strings.Contains(string(ownerAfter), `Include="A"`) || string(twoAfter) != string(twoBefore) {
				t.Fatal("project removal changed wrong declaration")
			}
		})
	}
}
