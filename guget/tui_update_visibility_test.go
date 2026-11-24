package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
)

// A newer version that a framework cannot use keeps the ✓ icon, so the detail
// panel has to say that it exists and how to take it.
func TestDetailExplainsANewerVersionTheUpdateTargetSkips(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	setTestPackage(m, "A", "1.0.0", "2.0.0@net9.0")
	row := selectedRow(m)
	detail := text.Strip(m.renderDetail(row))
	if row.statusIcon() != "✓" || !strings.Contains(detail, "Newer version not offered") || !strings.Contains(detail, "2.0.0 is not usable") {
		t.Fatalf("icon %s detail:\n%s", row.statusIcon(), detail)
	}

	// An update with a newer stable version beyond it (⇑) gets the same note.
	setTestPackage(m, "A", "1.0.0", "2.0.0@net8.0", "3.0.0@net9.0")
	if row := selectedRow(m); row.statusIcon() != "⇑" || !strings.Contains(text.Strip(m.renderDetail(row)), "3.0.0 is not usable") {
		t.Fatalf("icon %s detail:\n%s", row.statusIcon(), text.Strip(m.renderDetail(row)))
	}

	// Nothing is skipped when the newest version is usable, when the project
	// already uses the skipped version, or under the stable policy.
	setTestPackage(m, "A", "1.0.0", "2.0.0@net8.0")
	if row := selectedRow(m); row.skippedStable() != nil {
		t.Fatal("a usable newest version is reported as skipped")
	}
	setTestPackage(m, "A", "0.5.0@net8.0", "1.0.0@net9.0")
	if row := selectedRow(m); row.skippedStable() != nil || strings.Contains(m.renderDetail(row), "not offered") {
		t.Fatal("the installed version is reported as skipped")
	}
	m.updateTarget = UpdateTargetStable
	setTestPackage(m, "A", "1.0.0", "2.0.0@net9.0")
	if row := selectedRow(m); row.skippedStable() != nil || row.statusIcon() != "↑" {
		t.Fatalf("stable policy: icon %s skipped %v", row.statusIcon(), row.skippedStable())
	}
}

func TestHelpNamesTheActiveUpdateTargetAndTheListConventions(t *testing.T) {
	for policy, summary := range map[UpdateTarget]string{
		UpdateTargetCompatibleStable: "newest stable version that every target framework can use",
		UpdateTargetCompatible:       "prereleases included",
		UpdateTargetStable:           "whatever the frameworks",
	} {
		m, _, _ := newActionTestApp(t, "")
		m.updateTarget = policy
		m.HandleEvent(ui.KeyPress(ui.Key{Code: '?'}))
		if !m.help.active {
			t.Fatal("help did not open")
		}
		help := strings.Join(strings.Fields(text.Strip(m.help.vp.GetContent())), " ")
		for _, want := range []string{
			"update-target " + string(policy) + ":", summary,
			"guget config", "GUGET_<KEY>",
			"Package list", "the update target (u), then the latest stable version (a)",
			"update to the update target",
			"latest stable, whatever the frameworks",
		} {
			if !strings.Contains(help, want) {
				t.Fatalf("%s help lacks %q:\n%s", policy, want, help)
			}
		}
		if strings.Contains(help, "latest compatible") {
			t.Fatalf("%s help still describes u as latest compatible", policy)
		}
	}
}

func TestAllProjectsFooterNamesWhatUAimsFor(t *testing.T) {
	for policy, want := range map[UpdateTarget]string{
		UpdateTargetCompatibleStable: "up compat",
		UpdateTargetCompatible:       "up compat+pre",
		UpdateTargetStable:           "up stable",
	} {
		m, _, _ := newActionTestApp(t, "")
		m.updateTarget = policy
		m.projects.cursor = 0
		var label string
		for _, pair := range m.footerKeys() {
			if pair.k == "u/U" {
				label = pair.v
			}
		}
		if label != want {
			t.Fatalf("%s footer u/U = %q, want %q", policy, label, want)
		}
	}
}

func TestQuickUpdateNamesWhatThePolicyLookedFor(t *testing.T) {
	for policy, want := range map[UpdateTarget]string{
		UpdateTargetCompatibleStable: "No compatible stable version for A",
		UpdateTargetCompatible:       "No compatible version for A",
	} {
		m, _, _ := newActionTestApp(t, "")
		m.updateTarget = policy
		setTestPackage(m, "A", "2.0.0@net9.0", "3.0.0@net9.0")
		if row := selectedRow(m); availableVersionText(row) != "- (3.0.0)" || row.statusIcon() != "✓" {
			t.Fatalf("%s: icon %s available %q", policy, row.statusIcon(), availableVersionText(row))
		}
		m.updatePackage(false, scopeSelected)
		if !strings.HasPrefix(m.ctx.StatusLine, want+";") {
			t.Fatalf("%s status %q", policy, m.ctx.StatusLine)
		}
	}
}

// The new notes and markers must not push any frame past the terminal.
func TestUpdateVisibilityFrames(t *testing.T) {
	for _, theme := range []string{"auto-dark", "auto-light"} {
		for _, size := range [][2]int{{160, 42}, {120, 30}, {80, 24}, {80, 12}, {60, 10}} {
			for _, policy := range []UpdateTarget{UpdateTargetCompatibleStable, UpdateTargetCompatible, UpdateTargetStable} {
				for _, state := range []string{"main", "all", "help", "versions", "logs"} {
					t.Run(fmt.Sprintf("%s-%s-%s-%dx%d", theme, policy, state, size[0], size[1]), func(t *testing.T) {
						initTheme(theme, false)
						m, _, _ := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0" PrivateAssets="all"/><PackageReference Include="B" Version="9.0.0-preview.1.24080.9"/></ItemGroup></Project>`)
						m.updateTarget = policy
						setTestPackage(m, "B", "9.0.0-preview.1.24080.9", "10.0.0", "11.0.0-preview.7.26381.103")
						info := testPackage("1.0.0", "2.0.0@net8.0", "2.1.0-dev-02453@net8.0", "3.0.0@net9.0")
						info.ID = "A"
						info.Versions[len(info.Versions)-1].Deprecation = &PackageDeprecation{Message: "1.0.0 is legacy", AlternatePackageID: "A2"}
						m.ctx.Results["A"] = nugetResult{pkg: info, source: "local"}
						m.ctx.LogLines = []string{"[WARN] Save failed: " + strings.Repeat("a long refusal with a path ", 12)}
						m.HandleEvent(ui.ResizeEvent{Width: size[0], Height: size[1]})
						if state == "all" {
							m.projects.cursor = 0
						}
						m.rebuildPackageRows()
						m.selectPackageByName("A")
						m.refreshDetail()
						switch state {
						case "help":
							m.HandleEvent(ui.KeyPress(ui.Key{Code: '?'}))
						case "versions":
							m.openVersionPicker()
						case "logs":
							m.HandleEvent(ui.KeyPress(ui.Key{Code: 'l'}))
						}
						m.relayout()
						lines := strings.Split(m.Render().Content, "\n")
						if len(lines) > size[1] {
							t.Fatalf("frame has %d rows, terminal %d", len(lines), size[1])
						}
						for i, line := range lines {
							if w := text.StringWidth(line); w > size[0] {
								t.Fatalf("row %d has %d cells, terminal %d", i, w, size[0])
							}
						}
						m.Disconnect()
					})
				}
			}
		}
	}
}
