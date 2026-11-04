package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	ui "github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
	text "github.com/nulifyer/guget/internal/ui/text"
)

func TestCompactOverlaysKeepBordersAndMouseTargetsVisible(t *testing.T) {
	initTheme("auto-dark", false)
	const name = "Microsoft.Extensions.DependencyInjection.Abstractions"
	const version = "12.0.0-preview.7.12345"
	for _, height := range []int{12, 13, 16, 30} {
		for _, kind := range []string{"remove", "pinned", "pinned-long", "projects", "locations", "versions", "notes", "dependencies", "help"} {
			t.Run(fmt.Sprintf("%s/%d", kind, height), func(t *testing.T) {
				m, _, _ := newActionTestApp(t, "")
				m.ctx.Width, m.ctx.Height = 80, height
				m.relayout()
				var overlay interface{ MouseRegions() []mouseRegion }
				switch kind {
				case "remove":
					m.confirmRemove = newConfirmRemove(m, name)
					overlay = &m.confirmRemove
				case "pinned", "pinned-long":
					if kind == "pinned-long" {
						m.packages.rows[0].ref.Name = name
						m.packages.rows[0].ref.Version = ParseSemVer("1.0.0-preview." + strings.Repeat("1", 70))
					}
					m.confirmUpdate = newConfirmUpdate(m, name, version, m.selectedProject())
					overlay = &m.confirmUpdate
				case "projects":
					p := m.ctx.ParsedProjects[0]
					p.FileName = "Company.Application.Integration.Tests.csproj"
					m.openProjectPicker(name, version)
					m.projectPick.items[0].currentVersion = "11.0.0-preview.5.12345"
					overlay = &m.projectPick
				case "locations":
					p := m.ctx.ParsedProjects[0]
					p.AddTargets = []AddTarget{{Kind: AddTargetImportedProps, FilePath: "Company.Shared.Dependency.Configuration.props", Description: "Shared by multiple projects"}}
					m.locationPick = newLocationPicker(m, name, version, p)
					overlay = &m.locationPick
				case "versions":
					info := m.ctx.Results["A"].pkg
					info.Deprecated = true
					info.AlternatePackageID = name
					m.ctx.Results[name] = nugetResult{pkg: info}
					m.picker = newVersionPicker(m, name, info.Versions, nil, m.selectedProject(), false)
					overlay = &m.picker
				case "notes":
					m.releaseNotes = newReleaseNotesOverlay(m, name+" Release Notes")
					m.releaseNotes.activeTab = tabNuSpec
					m.releaseNotes.nsVersions = []string{version, "11.0.0"}
					m.releaseNotes.nsNotes = "Visible release notes"
					m.releaseNotes.updateViewportContent()
					overlay = &m.releaseNotes
				case "dependencies":
					m.depTree = newDepTreeOverlay(m, name, false)
					m.depTree.vp.SetContent("Visible dependency")
					overlay = &m.depTree
				case "help":
					m.help.active = true
					m.help.refreshView()
					overlay = &m.help
				}
				view := m.Render().Content
				if style.Width(view) > m.ctx.Width || style.Height(view) > height || !strings.Contains(view, "╰") {
					t.Fatalf("overlay exceeds terminal or loses its bottom border:\n%s", text.Strip(view))
				}
				lines := strings.Split(text.Strip(view), "\n")
				for _, region := range overlay.MouseRegions() {
					if region.click == nil {
						continue
					}
					r := region.rect
					if r.x < 0 || r.y < 0 || r.x+r.w > m.ctx.Width || r.y+r.h > m.overlayHeight() {
						t.Fatalf("click target outside visible overlay: %+v", r)
					}
					if kind != "notes" && kind != "dependencies" && kind != "help" && r.h == 1 && r.w != 3 && r.w < 15 {
						label := text.Strip(text.Cut(lines[r.y], r.x, r.x+r.w))
						if !strings.HasPrefix(label, "[ ") || !strings.HasSuffix(label, " ]") {
							t.Fatalf("button target misses rendered label: %+v, text=%q", r, label)
						}
					}
				}
				if kind == "notes" && !strings.Contains(view, "Visible release notes") {
					t.Fatal("short notes panel hides body behind duplicate headings")
				}
				if kind == "dependencies" && !strings.Contains(view, "Visible dependency") {
					t.Fatal("short dependency viewport hides content")
				}
				if kind == "help" && !strings.Contains(view, "focus next panel") {
					t.Fatal("short help viewport only shows headings")
				}
			})
		}
	}
}

func TestResizeKeepsSelectedRowsVisibleAndReflowsDetails(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, "")
	for i := range 30 {
		m.packages.rows = append(m.packages.rows, packageRow{ref: PackageReference{Name: fmt.Sprintf("Package-%d", i)}})
		m.projects.items = append(m.projects.items, projectItem{name: fmt.Sprintf("P%d", i), project: m.ctx.ParsedProjects[0]})
	}
	m.packages.cursor = len(m.packages.rows) - 1
	m.projects.cursor = len(m.projects.items) - 1
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 16})
	if !strings.Contains(m.Render().Content, "Package-29") || !strings.Contains(m.Render().Content, "P29") {
		t.Fatal("resizing hid a selected row")
	}
	m.packages.cursor = 0
	info := m.packages.rows[0].info
	info.Description = "This description should reflow after the package detail viewport expands to a wider terminal."
	m.refreshDetail()
	before := m.detail.vp.TotalLineCount()
	m.HandleEvent(ui.ResizeEvent{Width: 180, Height: 40})
	m.HandleEvent(resizeDebounceMsg{id: m.resizeDebounceID})
	if m.detail.vp.TotalLineCount() >= before {
		t.Fatal("detail content kept its narrow wrapping after resize")
	}
}

func TestLogsFollowLatestEntryWithoutClippingLastRow(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Width, m.ctx.Height = 80, 12
	m.ctx.ShowLogs = true
	m.focus = focusLog
	m.relayout()
	m.ctx.LogLines = []string{"[INFO] First entry", "[ERROR] Latest entry"}
	m.updateLogView()
	view := m.Render().Content
	if !strings.Contains(view, "Latest entry") || strings.Count(view, "╰") != 1 {
		t.Fatalf("log entry or panel border clipped:\n%s", text.Strip(view))
	}
	layout := m.mainMouseLayout(10, 40, 30)
	if layout.log.h != m.logOuterHeight() {
		t.Fatal("log mouse bounds differ from rendered height")
	}
}

func TestTypicalTerminalKeepsAvailableVersionsVisible(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Width = 120
	_, mid, _ := m.panelWidths()
	if !m.packageColumns(mid).showAvail {
		t.Fatal("available versions hidden at 120 columns")
	}
}

func TestDependencyConstraintsSurviveLongNamesAndResize(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, "")
	v := &PackageVersion{DependencyGroups: []dependencyGroup{{TargetFramework: "net8.0", Dependencies: []packageDependency{{ID: "Microsoft.Extensions.DependencyInjection.Abstractions", Range: "[8.0.0, )"}}}}}
	m.ctx.Width, m.ctx.Height = 80, 20
	m.depTree = newDepTreeOverlay(m, "A", false)
	m.depTree.packageVersion = v
	m.relayout()
	m.depTree.refreshContent()
	narrow := m.depTree.vp.GetContent()
	if !strings.Contains(narrow, ">= 8.0.0") || style.Width(narrow) > m.depTree.vp.Width() {
		t.Fatalf("constraint hidden: %s", text.Strip(narrow))
	}
	for range 4 {
		m.depTree.HandleKey(ui.KeyPress(ui.Key{Code: '['}))
	}
	if m.depTree.vp.Width() != m.depTree.Width()-6 || !strings.Contains(m.depTree.vp.GetContent(), ">= 8.0.0") {
		t.Fatal("overlay width shortcut leaves stale dependency columns")
	}

	m.HandleEvent(ui.ResizeEvent{Width: 160, Height: 40})
	m.HandleEvent(resizeDebounceMsg{id: m.resizeDebounceID})
	if !strings.Contains(m.depTree.vp.GetContent(), "Microsoft.Extensions.DependencyInjection.Abstractions") {
		t.Fatal("expanded dependency view retains truncated names")
	}
}

func TestDetailWrapsLongErrorsAndUnicodeWithoutLosingText(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, "")
	m.detail.vp.SetWidth(18)
	message := "Cannot reach https://packages.example.test/very/long/path/index.json"
	rendered := m.renderDetail(packageRow{err: errors.New(message)})
	if style.Width(rendered) > 18 || !strings.Contains(strings.ReplaceAll(text.Strip(rendered), "\n", ""), "index.json") {
		t.Fatal("detail error loses its tail")
	}
	rendered = wordWrap("漢字の長い説明とバージョン情報", 10)
	if style.Width(rendered) > 10 || strings.ReplaceAll(text.Strip(rendered), "\n", "") != "漢字の長い説明とバージョン情報" {
		t.Fatal("Unicode wrapping loses text or exceeds cell width")
	}
}

func TestLogTailSurvivesOpeningAndFocusChanges(t *testing.T) {
	initTheme("auto-dark", false)
	for _, height := range []int{12, 16, 20} {
		t.Run(fmt.Sprint(height), func(t *testing.T) {
			m, _, _ := newActionTestApp(t, "")
			m.ctx.Width, m.ctx.Height = 80, height
			m.packages.rows = m.packages.rows[:1]
			m.relayout()
			for i := range 20 {
				m.ctx.LogLines = append(m.ctx.LogLines, fmt.Sprintf("[INFO] Entry%d", i))
			}
			m.HandleEvent(ui.KeyPress(ui.Key{Code: 'l'}))
			for range 8 {
				view := m.Render().Content
				// Compact logs occupy the full body; taller layouts retain all panels.
				borders := 4
				if m.compactLogs() {
					borders = 1
				} else if !strings.Contains(view, "1.0.0") {
					t.Fatal("package version hidden beside logs")
				}
				if !strings.Contains(view, "Entry19") || strings.Count(view, "╰") != borders {
					t.Fatalf("latest log or border hidden at focus %v:\n%s", m.focus, text.Strip(m.Render().Content))
				}
				if m.log.vp.Height() != m.logContentHeight() {
					t.Fatal("viewport height differs from visible log rows")
				}
				m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyTab}))
			}
		})
	}
}

func TestTinyTerminalQuitHintsWork(t *testing.T) {
	for _, code := range []rune{'q', ui.KeyEscape} {
		m, _, _ := newActionTestApp(t, "")
		m.ctx.Width, m.ctx.Height = 79, 11
		cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: code}))
		if cmd == nil {
			t.Fatal("displayed quit shortcut does nothing")
		}
		if _, ok := cmd().(ui.QuitEvent); !ok {
			t.Fatal("displayed quit shortcut does not quit")
		}
	}
}

func TestTransitiveDependencyVersionsSurviveNarrowLayouts(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Width, m.ctx.Height = 80, 20
	m.depTree = newDepTreeOverlay(m, "Transitive dependencies", true)
	raw := `Project 'Company.Application.Integration.Tests' has the following package references
 [net8.0]:
 Top-level Package Requested Resolved
 > Microsoft.Extensions.DependencyInjection.Abstractions 8.0.0 8.0.5
 Transitive Package Resolved
 > Microsoft.Extensions.Configuration.Abstractions 9.0.1`
	m.HandleEvent(depTreeReadyMsg{requestID: m.depTree.requestID, content: raw})
	for _, version := range []string{"8.0.0", "8.0.5", "9.0.1"} {
		if !strings.Contains(m.depTree.vp.GetContent(), version) {
			t.Fatalf("dependency table hides version %s", version)
		}
	}
	if style.Width(m.depTree.vp.GetContent()) > m.depTree.vp.Width() {
		t.Fatal("transitive table exceeds viewport width")
	}
	m.HandleEvent(ui.ResizeEvent{Width: 160, Height: 40})
	m.HandleEvent(resizeDebounceMsg{id: m.resizeDebounceID})
	if !strings.Contains(m.depTree.vp.GetContent(), "Microsoft.Extensions.DependencyInjection.Abstractions") {
		t.Fatal("expanded transitive view retains truncated names")
	}
}

func TestResizeFramesStayWithinTerminalBeforeAndAfterDebounce(t *testing.T) {
	initTheme("auto-dark", false)
	for _, kind := range []string{"main", "logs", "help", "notes", "dependencies", "sources"} {
		t.Run(kind, func(t *testing.T) {
			m, _, _ := newActionTestApp(t, "")
			m.ctx.Width, m.ctx.Height = 180, 45
			m.relayout()
			m.refreshDetail()
			m.setStatus(strings.Repeat("Long operation status ", 8), false)
			switch kind {
			case "logs":
				m.ctx.ShowLogs = true
				m.ctx.LogLines = []string{"[INFO] Latest log"}
				m.relayout()
				m.updateLogView()
			case "help":
				m.help.active = true
				m.help.refreshView()
			case "notes":
				m.releaseNotes = newReleaseNotesOverlay(m, "Package Release Notes")
				m.releaseNotes.activeTab = tabNuSpec
				m.releaseNotes.nsVersions = []string{"12.0.0"}
				m.releaseNotes.nsNotes = strings.Repeat("Long release notes ", 40)
				m.releaseNotes.updateViewportContent()
			case "dependencies":
				m.depTree = newDepTreeOverlay(m, "Package Dependencies", false)
				m.depTree.vp.SetContent(strings.Repeat("Long dependency ", 10))
			case "sources":
				m.sources.active = true
				m.ctx.Sources = []NugetSource{{Name: strings.Repeat("Long source ", 10), URL: "https://example.test/feed"}}
			}
			m.Render()
			for _, size := range [][2]int{{120, 30}, {100, 20}, {80, 12}, {120, 18}, {80, 20}} {
				m.HandleEvent(ui.ResizeEvent{Width: size[0], Height: size[1]})
				for phase := range 2 {
					view := m.Render().Content
					if style.Width(view) > size[0] || style.Height(view) > size[1] || !strings.Contains(view, "╰") {
						t.Fatalf("%v, phase%d: wrapped frame or missing bottom border:\n%s", size, phase, text.Strip(view))
					}
					if style.Height(m.renderFooter()) != m.footerLines()+1 {
						t.Fatal("status wrapped into an unbudgeted footer row")
					}
					m.HandleEvent(resizeDebounceMsg{id: m.resizeDebounceID})
				}
			}
		})
	}
}

func TestNarrowPanelDoesNotWrapItsBorderOrHeading(t *testing.T) {
	initTheme("auto-dark", false)
	panel := renderToPanel(stylePanel, 10, 8, "Package Detail\n"+strings.Repeat("long content ", 20))
	if style.Width(panel) != 10 || style.Height(panel) != 8 {
		t.Fatalf("panel exceeded its frame: %dx%d", style.Width(panel), style.Height(panel))
	}
}
