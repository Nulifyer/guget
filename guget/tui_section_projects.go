package main

import (
	"strings"
)

func (m Model) renderProjectPanel(w int) string {
	focused := m.focus == focusProjects
	innerW := w - 2 // border only, no padding
	visibleH := m.projectListHeight()

	var lines []string

	// Title
	lines = append(lines, " "+styleSubtleBold.Render("Projects"))
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	end := m.projectOffset + visibleH
	if end > len(m.projectItems) {
		end = len(m.projectItems)
	}

	for i := m.projectOffset; i < end; i++ {
		item := m.projectItems[i]
		selected := i == m.projectCursor

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
