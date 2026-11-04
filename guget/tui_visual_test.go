package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
)

// Deterministic data for rendered checks. No public feeds or real projects are
// touched. The same app can be driven through the real runtime in a PTY.
func newComponentVisualApp(t *testing.T) *App {
	t.Helper()
	names := []string{"Newtonsoft.Json", "Serilog", "Microsoft.Extensions.DependencyInjection", "Dapper", "FluentValidation", "Polly", "System.Text.Json", "Acme.Platform.Observability.OpenTelemetry.Instrumentation"}
	var xml strings.Builder
	xml.WriteString(`<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup>`)
	for _, name := range names {
		fmt.Fprintf(&xml, `<PackageReference Include="%s" Version="1.0.0"/>`, name)
	}
	xml.WriteString(`</ItemGroup></Project>`)
	m, _, _ := newActionTestApp(t, xml.String())
	m.updateChecksDisabled = true
	m.ctx.Results = make(map[string]nugetResult)
	for i, name := range names {
		info := &PackageInfo{ID: name, Description: "Reliable application infrastructure for services, background workers, and command-line tools. Supports structured diagnostics, dependency injection, and production troubleshooting. Unicode metadata: café, 日本語.", Authors: NewSet[string](), Tags: NewSet[string](), LatestVersion: "3.0.0"}
		info.Authors.Add("Example maintainers")
		info.Tags.Add("dotnet")
		info.Tags.Add("infrastructure")
		for j := 24; j >= 0; j-- {
			v := PackageVersion{SemVer: ParseSemVer(fmt.Sprintf("%d.0.0", j+1)), Published: time.Date(2025, 1, 1+j, 0, 0, 0, 0, time.UTC)}
			info.Versions = append(info.Versions, v)
		}
		if i == 0 {
			info.Versions[len(info.Versions)-1].Vulnerabilities = []PackageVulnerability{{AdvisoryURL: "https://example.test/advisory/GHSA-example", Severity: 2}}
		}
		if i == 1 {
			info.Deprecated = true
			info.DeprecationMessage = "Use the maintained logging package for new applications."
			info.AlternatePackageID = "Serilog.Sinks.Console"
		}
		m.ctx.Results[name] = nugetResult{pkg: info, source: "fixture-feed"}
	}
	m.ctx.Sources = []NugetSource{{Name: "fixture-feed", URL: "https://packages.example.test/nuget/v3/index.json"}, {Name: "local-cache", URL: "/tmp/guget-visual/packages"}}
	m.ctx.NugetServices = nil
	m.ctx.LogLines = []string{"[INFO] Found 3 projects", "[INFO] Reading package metadata", "[WARN] Newtonsoft.Json 1.0.0 has a known advisory", "[ERROR] Example private feed is unavailable", "[INFO] Navigation remains available while loading"}
	m.rebuildPackageRows()
	m.refreshDetail()
	return m
}
func setComponentVisualState(m *App, state string) {
	switch state {
	case "help":
		m.help.active = true
		m.help.refreshView()
	case "sources":
		m.sources.active = true
	case "versions":
		m.openVersionPicker()
	case "search":
		m.openSearch()
		m.search.input.SetValue("OpenTelemetry")
		m.search.loading = false
		m.search.results = []SearchResult{{ID: "OpenTelemetry", Version: "1.12.0", Description: "Instrumentation and telemetry for distributed applications", TotalDownloads: 1700000, Source: "fixture-feed"}, {ID: "OpenTelemetry.Extensions.Hosting", Version: "1.12.0", Description: "Hosting integration with dependency injection", Source: "fixture-feed"}}
	case "remove":
		m.confirmRemove = newConfirmRemove(m, m.packages.rows[0].ref.Name)
	case "update":
		m.confirmUpdate = newConfirmUpdate(m, m.packages.rows[0].ref.Name, "25.0.0", m.selectedProject())
	case "projects":
		m.openProjectPicker(m.packages.rows[0].ref.Name, "25.0.0")
	case "locations":
		m.locationPick = newLocationPicker(m, m.packages.rows[0].ref.Name, "25.0.0", m.selectedProject())
	case "dependencies":
		m.openDepTree()
	case "notes":
		m.releaseNotes = newReleaseNotesOverlay(m, "Newtonsoft.Json • Release notes")
		m.releaseNotes.activeTab = tabNuSpec
		m.releaseNotes.nsAvailable = true
		m.releaseNotes.nsVersions = []string{"25.0.0", "24.0.0", "23.0.0", "22.0.0"}
		m.releaseNotes.nsNotes = strings.Repeat("This release fixes serialization edge cases and improves diagnostics.\n\n", 20)
		m.releaseNotes.updateViewportContent()
	case "loading":
		m.ctx.LoadingDone = 2
		m.ctx.LoadingTotal = 8
		m.packages.rows[0].loading = true
		m.refreshDetail()
	case "error":
		m.ctx.StatusLine = "Feed unavailable. Check sources with s, then retry with ctrl+r."
		m.ctx.StatusIsErr = true
	case "empty":
		m.packages.rows = nil
		m.detail.vp.SetContent("Select a package to inspect its details.")
	case "logs":
		m.ctx.ShowLogs = true
		m.updateLogView()
	}
	m.relayout()
}
func TestComponentVisualFrames(t *testing.T) {
	dir := os.Getenv("GUGET_VISUAL_DIR")
	states := []string{"main", "help", "sources", "versions", "search", "remove", "update", "projects", "locations", "dependencies", "notes", "loading", "error", "empty", "logs"}
	for _, size := range [][2]int{{160, 42}, {120, 30}, {80, 24}, {80, 12}, {60, 10}} {
		for _, theme := range []string{"auto-dark", "auto-light"} {
			for _, state := range states {
				name := fmt.Sprintf("%s-%s-%dx%d", theme, state, size[0], size[1])
				t.Run(name, func(t *testing.T) {
					initTheme(theme, false)
					m := newComponentVisualApp(t)
					m.HandleEvent(ui.ResizeEvent{Width: size[0], Height: size[1]})
					setComponentVisualState(m, state)
					frame := m.Render()
					if lines := strings.Split(frame.Content, "\n"); len(lines) > size[1] {
						t.Fatalf("frame has %d rows, terminal %d", len(lines), size[1])
					} else {
						for i, line := range lines {
							if w := text.StringWidth(line); w > size[0] {
								t.Fatalf("row %d has %d cells, terminal %d", i, w, size[0])
							}
						}
					}
					if dir != "" {
						if err := os.MkdirAll(dir, 0755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(frame.Content), 0644); err != nil {
							t.Fatal(err)
						}
					}
					m.Disconnect()
				})
			}
		}
	}
}
func TestComponentVisualSession(t *testing.T) {
	if os.Getenv("GUGET_VISUAL_LIVE") == "" {
		t.Skip("set GUGET_VISUAL_LIVE=1 in a PTY to inspect the runtime")
	}
	theme := os.Getenv("GUGET_VISUAL_THEME")
	if theme == "" {
		theme = "auto-dark"
	}
	initTheme(theme, false)
	m := newComponentVisualApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.lifecycle = ctx
	setComponentVisualState(m, os.Getenv("GUGET_VISUAL_STATE"))
	runtime := ui.NewRuntime(ctx, m)
	m.SetSender(runtime.Send)
	if err := runtime.Run(); err != nil {
		t.Fatal(err)
	}
}
func TestSearchPasteSchedulesOneQueryAndNeverTriggersShortcuts(t *testing.T) {
	m := newComponentVisualApp(t)
	m.openSearch()
	effect := m.HandleEvent(ui.PasteEvent{Text: "hello\nworld/?q"})
	if m.search.input.Value() != "hello world/?q" || m.help.active || effect == nil || m.search.debounceID != 1 {
		t.Fatalf("paste state=%q debounce=%d", m.search.input.Value(), m.search.debounceID)
	}
}
