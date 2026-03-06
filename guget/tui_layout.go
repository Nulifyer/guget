package main

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

func (m *App) footerKeys() []kv {
	// Overlay contexts — delegate to active overlay.
	for _, o := range m.overlays() {
		if o.IsActive() {
			return o.FooterKeys()
		}
	}

	// Main screen — varies by focused panel.
	isAllProjects := m.selectedProject() == nil

	switch m.focus {
	case focusProjects:
		return []kv{
			{"tab/↑↓", "nav"},
			{"enter", "packages"},
			{"r/R", "restore/all"},
			{"T", "deps"},
			{"/", "add"},
			{"?", "help"},
			{"esc/q", "quit"},
		}

	case focusPackages:
		if isAllProjects {
			return []kv{
				{"tab/↑↓", "nav"},
				{"u/U", "up compat"},
				{"a/A", "up stable"},
				{"v", "version"},
				{"d", "del"},
				{"o/O", "sort/dir"},
				{"t/T", "deps"},
				{"n", "notes"},
				{"r/R", "restore"},
				{"/", "add"},
				{"?", "help"},
				{"esc/q", "quit"},
			}
		}
		return []kv{
			{"tab/↑↓", "nav"},
			{"u/U", "update/all"},
			{"a/A", "stable/all"},
			{"v", "version"},
			{"d", "del"},
			{"o/O", "sort/dir"},
			{"t/T", "deps"},
			{"n", "notes"},
			{"r/R", "restore/all"},
			{"/", "add"},
			{"?", "help"},
			{"esc/q", "quit"},
		}

	case focusDetail:
		return []kv{
			{"tab", "focus"},
			{"↑↓", "scroll"},
			{"v", "version"},
			{"n", "notes"},
			{"r/R", "restore/all"},
			{"?", "help"},
			{"esc/q", "quit"},
		}

	case focusLog:
		return []kv{
			{"tab", "focus"},
			{"↑↓", "scroll"},
			{"l", "close"},
			{"?", "help"},
			{"esc/q", "quit"},
		}
	}

	return []kv{{"?", "help"}, {"esc/q", "quit"}}
}

func (m *App) footerLines() int {
	keys := m.footerKeys()
	w := m.layoutWidth() - 4
	lines, curW  := 1, 0
	for _, pair := range keys {
		ew := lipgloss.Width(pair.k) + 1 + lipgloss.Width(pair.v)
		needed := ew
		if curW > 0 {
			needed += 3 // matches renderFooter() sepW
		}
		if curW+needed > w && curW > 0 {
			lines++
			curW = ew
		} else {
			curW += needed
		}
	}
	return lines + 1 // +1 for status row
}

// bodyOuterHeight returns the outer height for each main panel.
// In lipgloss v2, .Height(h) is the outer height (borders + padding + content).
// alignTextVertical does NOT truncate overflow, so content must fit exactly.
func (m *App) bodyOuterHeight() int {
	// footer is rendered without .Height(), so its rendered height is
	// footerLines() content + 1 top border.
	h := m.ctx.Height - m.footerLines() - 1
	if m.ctx.ShowLogs {
		h -= logPanelOuterHeight
	}
	return imax(4, h)
}

// panelContentHeight returns the usable content lines inside a panel.
// Panels use stylePanel/stylePanelNoPad with BorderTop(false), so
// vertical border = 1 (bottom). Content = outer - 1.
func (m *App) panelContentHeight() int {
	return m.bodyOuterHeight() - 1
}

func (m *App) packageListHeight() int {
	// content height minus column header (1) + divider (1)
	return imax(1, m.panelContentHeight()-2)
}

func (m *App) projectListHeight() int {
	// content height minus title row (1) + divider row (1)
	// each item = 3 lines (title + desc + spacing), last item needs only 2
	avail := m.panelContentHeight() - 2
	if avail < 2 {
		return 1
	}
	return 1 + (avail-2)/3
}

func (m *App) clampProjectOffset() {
	clampListScroll(m.projects.cursor, &m.projects.scroll, m.projectListHeight(), len(m.projects.items), 0)
}

func (m *App) relayout() {
	_, _, rightW := m.panelWidths()
	// viewport height = panel content height - title(1) - divider(1)
	innerH := m.panelContentHeight() - 2
	m.detail.vp.SetWidth(rightW - 4)
	m.detail.vp.SetHeight(innerH)
	if m.ctx.ShowLogs {
		m.log.vp.SetWidth(m.layoutWidth() - 4) // stylePanel: hBorder(2) + hPadding(2) = 4
		m.log.vp.SetHeight(logPanelLines)
	}
}

func (m *App) panelWidths() (left, mid, right int) {
	lw := m.layoutWidth()
	// lipgloss v2 .Width(w) is outer (border-box) — border chars are inside w.
	// So the three panel widths must sum to exactly lw.

	left = m.projects.baseWidth + m.projects.widthOffset
	right = m.detail.baseWidth + m.detail.widthOffset
	if left < m.projects.minWidth {
		left = m.projects.minWidth
	}
	if right < m.detail.minWidth {
		right = m.detail.minWidth
	}
	mid = lw - left - right

	if mid > 130 {
		mid = 130
		right = lw - left - mid
	}

	// Shrink panels when the terminal is too narrow.
	const minW = 10
	if mid < minW {
		right = lw - left - minW
		if right < minW {
			right = minW
		}
		mid = lw - left - right
		if mid < minW {
			left = lw - minW - right
			if left < minW {
				left = minW
			}
			mid = lw - left - right
		}
		if mid < minW {
			mid = minW
		}
	}

	return
}

func (m *App) overlayHeight() int {
	return m.ctx.Height - m.footerLines() - 1 // footer content + top border
}

func (m *App) renderFooter() string {
	keys := m.footerKeys()

	w := m.layoutWidth() - 4 // padding
	var lines []string
	var cur []string
	curW := 0
	sep := styleMuted.Render(" · ")
	sepW := 3

	for _, pair := range keys {
		k := styleAccentBold.Render(pair.k)
		v := styleSubtle.Render(pair.v)
		entry := k + " " + v
		entryW := lipgloss.Width(pair.k) + 1 + lipgloss.Width(pair.v)

		needed := entryW
		if len(cur) > 0 {
			needed += sepW
		}
		if curW+needed > w && len(cur) > 0 {
			lines = append(lines, strings.Join(cur, sep))
			cur = nil
			curW = 0
		}
		cur = append(cur, entry)
		if curW > 0 {
			curW += sepW
		}
		curW += entryW
	}
	if len(cur) > 0 {
		lines = append(lines, strings.Join(cur, sep))
	}
	keybinds := strings.Join(lines, "\n")

	// Status line — always reserve the row so height is stable.
	statusStr := ""
	if m.ctx.Restoring {
		statusStr = m.ctx.Spinner.View() + styleAccent.Render(" restoring...")
	} else if m.ctx.StatusLine != "" {
		s := styleGreen
		if m.ctx.StatusIsErr {
			s = styleRed
		}
		statusStr = s.Render(m.ctx.StatusLine)
	}

	return styleFooterBar.
		Width(m.layoutWidth()).
		Render(statusStr + "\n" + keybinds)
}
