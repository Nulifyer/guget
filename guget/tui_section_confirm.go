package main

import (
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
		return s.app.applyVersion(s.pkgName, s.newVersion, s.project)
	}
	return nil
}

// applyOrConfirmUpdate calls applyVersion directly, or opens the lock-confirm
// overlay if the currently-installed version is pinned with [x.y.z].
func (m *App) applyOrConfirmUpdate(pkgName, newVersion string, project *ParsedProject) bubble_tea.Cmd {
	if project != nil {
		for _, row := range m.packages.rows {
			if strings.EqualFold(row.ref.Name, pkgName) && row.ref.Locked {
				m.confirmUpdate = newConfirmUpdate(m, pkgName, newVersion, project)
				return nil
			}
		}
	}
	return m.applyVersion(pkgName, newVersion, project)
}

func (s *confirmRemove) Render() string {
	w := s.Width()
	lines := []string{
		styleRedBold.Render("Remove package?"),
		styleSubtle.Render(s.pkgName),
	}
	box := styleOverlayDanger.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return s.centerOverlay(box)
}

func (s *confirmUpdate) Render() string {
	w := s.Width()
	pinnedVer := ""
	for _, row := range s.app.packages.rows {
		if strings.EqualFold(row.ref.Name, s.pkgName) {
			pinnedVer = row.ref.Version.String()
			break
		}
	}
	lines := []string{
		styleYellowBold.Render("Version is pinned"),
		styleSubtle.Render(s.pkgName) + "  " + styleYellow.Render("["+pinnedVer+"]"),
		"",
		styleMuted.Render("Update to " + s.newVersion + " anyway?"),
	}
	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return s.centerOverlay(box)
}
