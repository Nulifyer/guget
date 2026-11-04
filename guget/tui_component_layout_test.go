package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
)

func TestCompactLogsRemainReadableAndBlockHiddenPackageActions(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 12})
	m.handleKey(ui.KeyPress(ui.Key{Code: 'l'}))
	frame := text.Strip(m.Render().Content)
	if !strings.Contains(frame, "Navigation remains available") || m.focus != focusLog {
		t.Fatalf("logs inaccessible: %q", frame)
	}
	if effect := m.handleKey(ui.KeyPress(ui.Key{Code: 'u'})); effect != nil || m.ctx.Saving {
		t.Fatal("hidden package action ran")
	}
	m.handleKey(ui.KeyPress(ui.Key{Code: 'l'}))
	if m.ctx.ShowLogs || m.focus != focusPackages {
		t.Fatal("cannot return to package panel")
	}
}
func TestResizeKeepsSelectedProjectAndPackageVisible(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.movePackageCursor(7)
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 12})
	frame := text.Strip(m.Render().Content)
	if !strings.Contains(frame, "◦ App") || !strings.Contains(frame, m.packages.rows[m.packages.cursor].ref.Name) {
		t.Fatalf("selection not visible: %q", frame)
	}
}
func TestCompactReleaseNotesKeepBodyAndBorderVisible(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 12})
	setComponentVisualState(m, "notes")
	frame := text.Strip(m.Render().Content)
	if !strings.Contains(frame, "This release fixes") || !strings.Contains(frame, "╰") || !strings.Contains(frame, "╯") {
		t.Fatalf("compact notes clipped: %q", frame)
	}
	// A click on the compact tabs uses the same reduced padding as drawing.
	for _, r := range m.releaseNotes.MouseRegions() {
		if r.rect.y >= m.overlayHeight() {
			t.Fatalf("hit region outside overlay: %+v", r)
		}
	}
}

func TestStaleModalFrameCannotApplyToAnotherOpening(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.confirmRemove = newConfirmRemove(m, "Newtonsoft.Json")
	view := m.Render()
	regions := m.confirmRemove.MouseRegions()
	button := regions[len(regions)-1]
	m.confirmRemove = newConfirmRemove(m, "Dapper")
	if effect := view.OnMouse(ui.MouseClick(ui.Mouse{X: button.rect.x, Y: button.rect.y, Button: ui.MouseLeft})); effect != nil || m.ctx.Saving {
		t.Fatal("stale modal frame applied a package operation")
	}
	if !m.confirmRemove.active || m.confirmRemove.pkgName != "Dapper" {
		t.Fatal("stale click changed the new dialog")
	}
}

func TestCompactPinnedConfirmationKeepsButtonsAndBorder(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 12})
	setComponentVisualState(m, "update")
	frame := text.Strip(m.Render().Content)
	if !strings.Contains(frame, "[ Update ]") || !strings.Contains(frame, "╰") || !strings.Contains(frame, "╯") {
		t.Fatalf("confirmation clipped: %q", frame)
	}
	for _, region := range m.confirmUpdate.MouseRegions() {
		if region.rect.y >= m.overlayHeight() {
			t.Fatalf("confirmation button hidden: %+v", region)
		}
	}
}

func TestRepositoryProjectPickerAcceptsPhysicalSpaceKey(t *testing.T) {
	snapshot, err := scanWorkspace("../test-dotnet")
	if err != nil {
		t.Fatal(err)
	}
	m := NewApp(snapshot.ProjectDir, snapshot, nil, BuiltFlags{NoUpdateCheck: true})
	m.openProjectPicker("Newtonsoft.Json", "13.0.4")
	found := false
	for i, it := range m.projectPick.items {
		if it.selectable() {
			m.projectPick.cursor = i
			found = true
			break
		}
	}
	if !found {
		t.Fatal("original projects have no selectable target")
	}
	m.projectPick.HandleKey(ui.KeyPress(ui.Key{Code: ' '}))
	if m.projectPick.selectedCount() != 1 {
		t.Fatal("Space did not select original-project target")
	}
	m.projectPick.HandleKey(ui.KeyPress(ui.Key{Code: ' '}))
	if m.projectPick.selectedCount() != 0 {
		t.Fatal("Space did not deselect original-project target")
	}
}
func TestGitHubCRLFNotesCannotOverwriteComponentBorders(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.HandleEvent(ui.ResizeEvent{Width: 120, Height: 30})
	m.releaseNotes = newReleaseNotesOverlay(m, "Newtonsoft.Json — Release Notes")
	m.releaseNotes.ghAvailable = true
	m.releaseNotes.ghReleases = []GitHubRelease{{TagName: "13.0.4"}}
	m.releaseNotes.ghNotes = "## What's Changed\r\n\r\n* Fix - Fixed TimeOnly deserialization to support more formats\r\n"
	m.releaseNotes.resizeViewport()
	frame := m.Render()
	if strings.Contains(frame.Content, "\r") {
		t.Fatal("CRLF notes emitted carriage returns into frame")
	}
	for i, line := range strings.Split(text.Strip(frame.Content), "\n") {
		if i > 0 && i < m.overlayHeight()-1 && !strings.HasPrefix(line, "│") {
			t.Fatalf("row %d lost left border: %q", i, line)
		}
	}
}

func TestCompactOriginalProjectNamesStayDistinct(t *testing.T) {
	names := []string{"ProjectA.csproj", "ProjectB.csproj", "CPMProject.Lib.csproj", "CPMProject.Worker.csproj"}
	seen := map[string]bool{}
	for _, name := range names {
		label := projectTitle(projectItem{name: name, project: &ParsedProject{}}, 10)
		if seen[label] {
			t.Fatalf("compact label hides project identity: %q", label)
		}
		seen[label] = true
		if text.StringWidth(label) > 10 {
			t.Fatalf("compact label exceeds allocation: %q", label)
		}
	}
}
func TestNarrowDetailsKeepFullPackageAndProjectVersions(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 24})
	name := "Microsoft.Extensions.DependencyInjection"
	row := packageRow{ref: PackageReference{Name: name, Version: ParseSemVer("1.0.0")}, info: m.ctx.Results[name].pkg}
	content := m.renderDetail(row)
	for _, line := range strings.Split(content, "\n") {
		if text.StringWidth(line) > m.detail.vp.Width() {
			t.Fatalf("detail field will be clipped: %q", line)
		}
	}
	joined := strings.ReplaceAll(text.Strip(content), "\n", "")
	if !strings.Contains(joined, name) {
		t.Fatalf("long package identity lost: %q", joined)
	}
}

func TestCompactSearchFailureKeepsDialogBorderAndInput(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 12})
	m.openSearch()
	m.search.input.SetValue("zz-guget-no-such-package-20261104")
	m.search.err = fmt.Errorf("HTTP 401 for https://nuget.pkg.github.com/Nulifyer/query?prerelease=false&q=zz-guget-no-such-package-20261104&semVerLevel=2.0.0&take=50")
	frame := text.Strip(m.Render().Content)
	if !strings.Contains(frame, "HTTP 401") || !strings.Contains(frame, "...") || !strings.Contains(frame, "╰") || !strings.Contains(frame, "╯") {
		t.Fatalf("search error clipped dialog: %q", frame)
	}
	if m.search.input.Value() != "zz-guget-no-such-package-20261104" {
		t.Fatal("search failure lost input")
	}
}

func TestResizeReflowsLongPackageDetailsWithoutResettingScroll(t *testing.T) {
	initTheme("auto-dark", false)
	m := newComponentVisualApp(t)
	m.HandleEvent(ui.ResizeEvent{Width: 180, Height: 24})
	name := "Microsoft.Extensions.DependencyInjection"
	for i, row := range m.packages.rows {
		if row.ref.Name == name {
			m.packages.cursor = i
			break
		}
	}
	m.refreshDetail()
	m.detail.vp.SetYOffset(3)
	if m.detail.vp.YOffset() != 3 {
		t.Fatal("fixture does not have scrollable detail content")
	}
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 24})
	if m.detail.vp.YOffset() != 3 {
		t.Fatal("resize reset detail scroll")
	}
	for _, line := range strings.Split(m.detail.vp.GetContent(), "\n") {
		if text.StringWidth(line) > m.detail.vp.Width() {
			t.Fatalf("resized content stayed at its old width: %q", line)
		}
	}
	if !strings.Contains(strings.ReplaceAll(text.Strip(m.detail.vp.GetContent()), "\n", ""), name) {
		t.Fatal("resize lost full package identity")
	}
}
