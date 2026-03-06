package main

import (
	"strings"
)

func (m *App) renderProjectPanel(w int) string {
	focused := m.focus == focusProjects
	innerW := w - 2 // border only, no padding
	visibleH := m.projectListHeight()

	var lines []string

	// Title
	lines = append(lines, " "+styleSubtleBold.Render("Projects"))
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	end := m.projects.scroll + visibleH
	if end > len(m.projects.items) {
		end = len(m.projects.items)
	}

	for i := m.projects.scroll; i < end; i++ {
		item := m.projects.items[i]
		selected := i == m.projects.cursor

		title := item.Title()
		desc := item.Description()

		title = truncate(title, innerW-3)
		desc = truncate(desc, innerW-5)

		if selected {
			lines = append(lines, " "+styleAccentBold.Render(title))
			lines = append(lines, "   "+styleSubtle.Render(desc))
		} else {
			lines = append(lines, " "+styleText.Render(title))
			lines = append(lines, "   "+styleMuted.Render(desc))
		}
		if i < end-1 {
			lines = append(lines, "")
		}
	}

	content := strings.Join(lines, "\n")

	s := stylePanelNoPad
	if focused {
		s = s.BorderForeground(colorAccent)
	}
	return renderToPanel(s, w, m.bodyOuterHeight(), content)
}
