package main

import (
	"path/filepath"
	"strings"

	"github.com/nulifyer/guget/internal/ui"
	"github.com/nulifyer/guget/internal/ui/style"
)

func (p *projectPanel) Draw(r ui.Rect) string {
	m := p.app
	w := r.W
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

		title := projectTitle(item, innerW-3)
		desc := item.Description()

		desc = truncate(desc, innerW-5)

		if selected {
			lines = append(lines, " "+styleAccentBold.Render(title))
			lines = append(lines, "   "+styleSubtle.Render(desc))
		} else {
			lines = append(lines, " "+styleText.Render(title))
			lines = append(lines, "   "+styleSubtle.Render(desc))
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
	return renderToPanel(s, w, r.H, content)
}

// Keep project identities distinguishable when a compact panel cannot fit the
// extension. Middle truncation retains distinguishing suffixes such as Lib/Worker.
func projectTitle(item projectItem, width int) string {
	title := item.Title()
	if style.Width(title) <= width {
		return title
	}
	if item.project == nil {
		return truncate("◈ All", width)
	}
	name := strings.TrimSuffix(item.name, filepath.Ext(item.name))
	return "◦ " + truncateMiddle(name, max(0, width-2))
}
