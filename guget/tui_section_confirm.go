package main

import (
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (m *Model) handleConfirmKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		m.overlayWidthOffset -= 4
		return nil
	case "]":
		m.overlayWidthOffset += 4
		return nil
	case "esc", "n", "q":
		m.overlayWidthOffset = 0
		m.confirm.active = false
		m.ctx.StatusLine = ""
	case "enter", "y":
		m.overlayWidthOffset = 0
		m.confirm.active = false
		return m.removePackage(m.confirm.pkgName)
	}
	return nil
}

func (m *Model) handleConfirmUpdateKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		m.overlayWidthOffset -= 4
		return nil
	case "]":
		m.overlayWidthOffset += 4
		return nil
	case "esc", "n", "q":
		m.overlayWidthOffset = 0
		m.confirmUpdate.active = false
		m.ctx.StatusLine = ""
	case "enter", "y":
		m.overlayWidthOffset = 0
		cu := m.confirmUpdate
		m.confirmUpdate.active = false
		return m.applyVersion(cu.pkgName, cu.newVersion, cu.project)
	}
	return nil
}

// applyOrConfirmUpdate calls applyVersion directly, or opens the lock-confirm
// overlay if the currently-installed version is pinned with [x.y.z].
func (m *Model) applyOrConfirmUpdate(pkgName, newVersion string, project *ParsedProject) bubble_tea.Cmd {
	if project != nil {
		for _, row := range m.packageRows {
			if strings.EqualFold(row.ref.Name, pkgName) && row.ref.Locked {
				m.confirmUpdate = confirmUpdate{
					active:     true,
					pkgName:    pkgName,
					newVersion: newVersion,
					project:    project,
				}
				return nil
			}
		}
	}
	return m.applyVersion(pkgName, newVersion, project)
}

func (m Model) renderConfirmOverlay() string {
	w := clampW(48+m.overlayWidthOffset, 36, m.ctx.Width-4)
	lines := []string{
		styleRedBold.Render("Remove package?"),
		styleSubtle.Render(m.confirm.pkgName),
	}
	box := styleOverlayDanger.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.ctx.Width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m Model) renderConfirmUpdateOverlay() string {
	w := clampW(52+m.overlayWidthOffset, 40, m.ctx.Width-4)
	cu := m.confirmUpdate
	pinnedVer := ""
	for _, row := range m.packageRows {
		if strings.EqualFold(row.ref.Name, cu.pkgName) {
			pinnedVer = row.ref.Version.String()
			break
		}
	}
	lines := []string{
		styleYellowBold.Render("Version is pinned"),
		styleSubtle.Render(cu.pkgName) + "  " + styleYellow.Render("["+pinnedVer+"]"),
		"",
		styleMuted.Render("Update to " + cu.newVersion + " anyway?"),
	}
	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.ctx.Width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}
