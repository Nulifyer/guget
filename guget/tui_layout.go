package main

import (
	"fmt"
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

	// Leave enough space for a log entry and both panel borders on short terminals.
	if m.ctx.ShowLogs && m.ctx.Height < 16 {
		return []kv{{"tab", "focus"}, {"↑↓", "nav/scroll"}, {"l", "close logs"}, {"?", "help"}, {"esc/q", "quit"}}
	}

	// Main screen — varies by focused panel.
	isAllProjects := m.selectedProject() == nil

	switch m.focus {
	case focusProjects:
		return []kv{
			{"tab/↑↓", "nav"},
			{"enter", "packages"},
			{"^r", "reload"},
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
				{"^r", "reload"},
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
			{"^r", "reload"},
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
			{"^r", "reload"},
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
	lines, curW := 1, 0
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
		h -= m.logOuterHeight()
	}
	return imax(5, h)
}

// logOuterHeight keeps both the log border and the main panels on screen.
func (m *App) logOuterHeight() int {
	return min(logPanelOuterHeight, max(4, m.ctx.Height-m.footerLines()-1-5))
}

func (m *App) logContentHeight() int {
	frame := 4 // two borders, title, divider
	if m.logOuterHeight() < 5 {
		frame = 3 // omit the divider to keep a log entry and a package row visible
	}
	return max(1, m.logOuterHeight()-frame)
}

// panelContentHeight returns the usable content lines inside a panel.
// Panels have top and bottom borders, so content = outer - 2.
func (m *App) panelContentHeight() int {
	return m.bodyOuterHeight() - 2
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
	if m.depTree.active {
		w, h := m.depTreeOverlaySize()
		m.depTree.vp.SetWidth(max(1, w-6))
		m.depTree.vp.SetHeight(max(1, h-2))
	}
	if m.ctx.ShowLogs {
		follow := m.log.vp.AtBottom()
		m.log.vp.SetWidth(m.layoutWidth() - 4) // stylePanel: hBorder(2) + hPadding(2) = 4
		m.log.vp.SetHeight(m.logContentHeight())
		if follow {
			m.log.vp.GotoBottom()
		}
	}
	m.clampOffset()
	m.clampProjectOffset()
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

	// Keep package names and requested versions readable on narrow terminals.
	// Share the remaining columns between the side panels in their chosen ratio.
	const minW = 10
	minPackageW := 40
	if lw >= 100 {
		minPackageW = 64
	}
	if mid < minPackageW {
		sideW := lw - minPackageW
		left = max(minW, min(sideW-minW, sideW*left/max(1, left+right)))
		right = sideW - left
		mid = minPackageW
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
	} else if m.ctx.Saving {
		statusStr = m.ctx.Spinner.View() + styleAccent.Render(" saving...")
	} else if m.ctx.Reloading {
		statusStr = m.ctx.Spinner.View() + styleAccent.Render(" reloading...")
	} else if m.ctx.LoadingDone < m.ctx.LoadingTotal {
		statusStr = m.ctx.Spinner.View() + styleAccent.Render(
			fmt.Sprintf(" loading packages... (%d/%d)", m.ctx.LoadingDone, m.ctx.LoadingTotal),
		)
	} else if m.ctx.StatusLine != "" {
		s := styleGreen
		if m.ctx.StatusIsErr {
			s = styleRed
		}
		statusStr = s.Render(m.ctx.StatusLine)
	}

	busy := m.ctx.Restoring || m.ctx.Saving || m.ctx.Reloading || m.ctx.LoadingDone < m.ctx.LoadingTotal
	if !busy && m.updateVersion != "" && (m.ctx.StatusLine == "" || !m.ctx.StatusIsErr) {
		notice := fmt.Sprintf("guget %s available · guget self-update", m.updateVersion)
		if statusStr == "" {
			notice += " · ctrl+x dismiss"
		}
		notice = truncate(notice, w)
		if statusStr != "" {
			remaining := w - lipgloss.Width(notice) - sepW
			if remaining > 0 {
				statusStr = truncate(statusStr, remaining) + sep
			} else {
				statusStr = ""
			}
		}
		statusStr += styleSubtle.Render(notice)
	}

	statusStr = truncateStyled(statusStr, w)
	return styleFooterBar.
		Width(m.layoutWidth()).
		Render(statusStr + "\n" + keybinds)
}
