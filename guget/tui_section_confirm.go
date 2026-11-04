package main

import (
	lipgloss "charm.land/lipgloss/v2"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
)

func newConfirmRemove(m *App, pkgName string) confirmRemove {
	return confirmRemove{
		sectionBase: sectionBase{app: m, baseWidth: 48, minWidth: 36, maxMargin: 4, active: true},
		pkgName:     pkgName,
	}
}

func newConfirmUpdate(m *App, pkgName, newVersion string, project *ParsedProject) confirmUpdate {
	return confirmUpdate{
		sectionBase: sectionBase{app: m, baseWidth: 52, minWidth: 40, maxMargin: 4, active: true},
		pkgName:     pkgName,
		newVersion:  newVersion,
		project:     project,
	}
}

func (s *confirmRemove) FooterKeys() []kv {
	return []kv{{"enter/y", "confirm"}, {"esc", "cancel"}}
}

func (s *confirmRemove) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		return nil
	case "]":
		s.Resize(4)
		return nil
	case "esc", "n", "q":
		s.closeOverlay()
	case "enter", "y":
		s.closeOverlay()
		return s.app.removePackage(s.pkgName)
	}
	return nil
}

func (s *confirmUpdate) FooterKeys() []kv {
	return []kv{{"enter/y", "confirm"}, {"esc", "cancel"}}
}

func (s *confirmUpdate) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		return nil
	case "]":
		s.Resize(4)
		return nil
	case "esc", "n", "q":
		s.closeOverlay()
	case "enter", "y":
		s.closeOverlay()
		return s.app.applyVersionWithSelection(s.pkgName, s.newVersion, s.project, s.selection)
	}
	return nil
}

// applyOrConfirmUpdate calls applyVersion directly, or opens the lock-confirm
// overlay if the currently-installed version is pinned with [x.y.z].
func (m *App) applyOrConfirmUpdate(pkgName, newVersion string, project *ParsedProject) bubble_tea.Cmd {
	return m.applyOrConfirmUpdateWithSelection(pkgName, newVersion, project, nil)
}

func (m *App) applyOrConfirmUpdateWithSelection(pkgName, newVersion string, project *ParsedProject, selection *reloadSelection) bubble_tea.Cmd {
	if !m.canMutate() {
		return nil
	}
	if project != nil {
		for _, row := range m.packages.rows {
			if strings.EqualFold(row.ref.Name, pkgName) && row.ref.Locked {
				m.confirmUpdate = newConfirmUpdate(m, pkgName, newVersion, project)
				m.confirmUpdate.selection = selection
				return nil
			}
		}
	}
	return m.applyVersionWithSelection(pkgName, newVersion, project, selection)
}

func (s *confirmRemove) Render() string {
	w := s.Width()
	innerW := w - 6
	lines := []string{
		overlayTitleLine("Remove package?", innerW),
		styleSubtle.Render(truncate(s.pkgName, innerW)),
		"",
	}
	buttonLine, buttonRegions := renderOverlayButtons(innerW, len(lines), s.HandleKey,
		overlayButton{label: "Cancel", code: bubble_tea.KeyEscape},
		overlayButton{label: "Remove", code: bubble_tea.KeyEnter},
	)
	lines = append(lines, buttonLine)
	regions := []mouseRegion{overlayCloseRegion(w, s.HandleKey)}
	regions = append(regions, buttonRegions...)
	box := styleOverlayDanger.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return s.centerOverlayInteractive(box, regions)
}

func (s *confirmUpdate) Render() string {
	w := s.Width()
	innerW := w - 6
	pinnedVer := ""
	for _, row := range s.app.packages.rows {
		if strings.EqualFold(row.ref.Name, s.pkgName) {
			pinnedVer = row.ref.Version.String()
			break
		}
	}
	pinnedVer = truncate(pinnedVer, max(1, innerW/2-2))
	lines := []string{
		overlayTitleLine("Version is pinned", innerW),
		styleSubtle.Render(truncate(s.pkgName, max(1, innerW-lipgloss.Width(pinnedVer)-4))) + "  " + styleYellow.Render("["+pinnedVer+"]"),
		"",
		styleSubtle.Render(truncate("Update to "+s.newVersion+" anyway?", innerW)),
		"",
	}
	if s.app.overlayHeight() < 10 {
		lines = []string{lines[0], lines[1], lines[3]}
	}
	buttonLine, buttonRegions := renderOverlayButtons(innerW, len(lines), s.HandleKey,
		overlayButton{label: "Cancel", code: bubble_tea.KeyEscape},
		overlayButton{label: "Update", code: bubble_tea.KeyEnter},
	)
	lines = append(lines, buttonLine)
	regions := []mouseRegion{overlayCloseRegion(w, s.HandleKey)}
	regions = append(regions, buttonRegions...)
	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return s.centerOverlayInteractive(box, regions)
}
