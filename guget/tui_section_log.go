package main

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

const maxLogLines = 2000

func appendLogLine(lines []string, line string) []string {
	if len(lines) >= maxLogLines {
		copy(lines, lines[len(lines)-maxLogLines+1:])
		lines = lines[:maxLogLines-1]
	}
	return append(lines, line)
}

func (m *App) updateLogView() {
	follow := m.log.vp.AtBottom()
	offset := m.log.vp.YOffset()
	var colored []string
	for _, line := range m.ctx.LogLines {
		colored = append(colored, colorizeLogLine(line))
	}
	m.log.vp.SetContent(strings.Join(colored, "\n"))
	if follow {
		m.log.vp.GotoBottom()
	} else {
		m.log.vp.SetYOffset(offset)
	}
}

func colorizeLogLine(line string) string {
	switch {
	case strings.HasPrefix(line, "[TRACE]"):
		return styleMuted.Render(line)
	case strings.HasPrefix(line, "[DEBUG]"):
		return styleCyan.Render(line)
	case strings.HasPrefix(line, "[INFO]"):
		return styleGreen.Render(line)
	case strings.HasPrefix(line, "[WARN]"):
		return styleYellow.Render(line)
	case strings.HasPrefix(line, "[ERROR]"), strings.HasPrefix(line, "[FATAL]"):
		return styleRed.Render(line)
	default:
		return styleText.Render(line)
	}
}

func (m *App) renderLogPanel() string {
	s := stylePanel
	if m.focus == focusLog {
		s = s.BorderForeground(colorAccent)
	}

	title := styleAccentBold.Render("Logs")
	div := styleBorder.Render(strings.Repeat("─", m.layoutWidth()-4))
	parts := []string{title}
	if m.logOuterHeight() >= 5 {
		parts = append(parts, div)
	}
	parts = append(parts, m.log.vp.View())
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	return renderToPanel(s, m.layoutWidth(), m.logOuterHeight(), content)
}
