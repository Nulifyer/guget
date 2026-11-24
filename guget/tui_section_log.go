package main

import (
	"github.com/nulifyer/guget/internal/ui"
	"strings"

	style "github.com/nulifyer/guget/internal/ui/style"
	text "github.com/nulifyer/guget/internal/ui/text"
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
	// Wrap long entries, such as an edit refusal or restore output with full
	// paths, so the panel can show the part the status row cut off.
	width := m.log.vp.Width()
	var colored []string
	for _, line := range m.ctx.LogLines {
		lineStyle := logLineStyle(line)
		for _, piece := range wrapLogLine(line, width) {
			colored = append(colored, lineStyle.Render(piece))
		}
	}
	m.log.vp.SetContent(strings.Join(colored, "\n"))
	if follow {
		m.log.vp.GotoBottom()
	} else {
		m.log.vp.SetYOffset(offset)
	}
}

// wrapLogLine splits an entry wider than the panel at spaces, or mid-word
// when a path leaves no space. The whole buffer is rewrapped on every log
// refresh, so the common printable-ASCII entry takes a byte-wise path that
// stays cheap for a full buffer; anything else gets the exact, slower wrap.
func wrapLogLine(line string, width int) []string {
	if width <= 0 || len(line) <= width {
		return []string{line}
	}
	for i := 0; i < len(line); i++ {
		if line[i] < 0x20 || line[i] >= 0x7f {
			return text.Wrap(line, width)
		}
	}
	var pieces []string
	for len(line) > width {
		cut := strings.LastIndexByte(line[:width+1], 0x20)
		if cut <= 0 {
			pieces = append(pieces, line[:width])
			line = line[width:]
			continue
		}
		pieces = append(pieces, line[:cut])
		line = line[cut+1:]
	}
	return append(pieces, line)
}

func logLineStyle(line string) style.Style {
	switch {
	case strings.HasPrefix(line, "[TRACE]"):
		return styleMuted
	case strings.HasPrefix(line, "[DEBUG]"):
		return styleCyan
	case strings.HasPrefix(line, "[INFO]"):
		return styleGreen
	case strings.HasPrefix(line, "[WARN]"):
		return styleYellow
	case strings.HasPrefix(line, "[ERROR]"), strings.HasPrefix(line, "[FATAL]"):
		return styleRed
	default:
		return styleText
	}
}

func (p *logPanel) Draw(r ui.Rect) string {
	m := p.app
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
	content := style.JoinVertical(style.Left, parts...)

	return renderToPanel(s, r.W, r.H, content)
}
