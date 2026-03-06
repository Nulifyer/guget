package main

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

func (m *Model) updateLogView() {
	var colored []string
	for _, line := range m.ctx.LogLines {
		colored = append(colored, colorizeLogLine(line))
	}
	m.logView.SetContent(strings.Join(colored, "\n"))
	m.logView.GotoBottom()
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

func (m Model) renderLogPanel() string {
	s := stylePanel
	if m.focus == focusLog {
		s = s.BorderForeground(colorAccent)
	}

	title := styleAccentBold.Render("Logs")
	div := styleBorder.Render(strings.Repeat("─", m.layoutWidth()-4))
	content := lipgloss.JoinVertical(lipgloss.Left, title, div, m.logView.View())

	return renderToPanel(s, m.layoutWidth(), logPanelOuterHeight, content)
}
