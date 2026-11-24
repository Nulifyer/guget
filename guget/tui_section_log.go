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

// appendLog adds a log entry. While the panel is shown, the entry's row count
// is recorded with it, so a refresh never has to revisit the buffer.
func (m *App) appendLog(line string) {
	m.ctx.LogLines = appendLogLine(m.ctx.LogLines, line)
	p := &m.log
	if !m.ctx.ShowLogs || p.heights == nil {
		p.heights = nil
		return
	}
	if len(p.heights) >= maxLogLines {
		drop := len(p.heights) - maxLogLines + 1
		for _, h := range p.heights[:drop] {
			p.rows -= h
		}
		copy(p.heights, p.heights[drop:])
		p.heights = p.heights[:maxLogLines-1]
	}
	h := len(logEntryRows(line, p.wrapWidth))
	p.heights = append(p.heights, h)
	p.rows += h
}

// updateLogView recounts every entry for the current panel width and
// refreshes the view. Opening the panel and a width change need it; new
// entries only need refreshLogView.
func (m *App) updateLogView() {
	m.log.heights = nil
	m.refreshLogView()
}

// refreshLogView shows the current log lines, following the tail unless the
// reader scrolled away from it. Long entries, such as an edit refusal or
// restore output with full paths, wrap so the panel can show the part the
// status row cut off. Only the visible rows are wrapped and styled (see
// logPanel.window): a refresh costs the entries added since the last one,
// not the whole buffer.
func (m *App) refreshLogView() {
	p := &m.log
	follow := p.vp.AtBottom()
	offset := p.vp.YOffset()
	if width := p.vp.Width(); p.heights == nil || p.wrapWidth != width || len(p.heights) != len(m.ctx.LogLines) {
		p.wrapWidth = width
		p.heights = make([]int, len(m.ctx.LogLines), max(len(m.ctx.LogLines), maxLogLines))
		p.rows = 0
		for i, line := range m.ctx.LogLines {
			p.heights[i] = len(logEntryRows(line, width))
			p.rows += p.heights[i]
		}
	}
	p.vp.SetWindow(p.rows, p.window)
	if follow {
		p.vp.GotoBottom()
	} else {
		p.vp.SetYOffset(offset)
	}
}

// window returns the n styled panel rows from row start. It finds the entry
// holding that row from whichever end of the buffer is nearer, by adding row
// counts, and wraps and styles only the entries it returns.
func (p *logPanel) window(start, n int) []string {
	lines := p.app.ctx.LogLines
	if n <= 0 || start < 0 || start >= p.rows || len(p.heights) != len(lines) {
		return nil
	}
	entry, skip := 0, start
	if start > p.rows/2 {
		entry, skip = len(lines), start-p.rows
		for skip < 0 {
			entry--
			skip += p.heights[entry]
		}
	} else {
		for skip >= p.heights[entry] {
			skip -= p.heights[entry]
			entry++
		}
	}
	out := make([]string, 0, n)
	for ; entry < len(lines) && len(out) < n; entry++ {
		lineStyle := logLineStyle(lines[entry])
		rows := logEntryRows(lines[entry], p.wrapWidth)
		for _, row := range rows[min(skip, len(rows)):] {
			if len(out) == n {
				break
			}
			out = append(out, lineStyle.Render(row))
		}
		skip = 0
	}
	return out
}

// logEntryRows returns the panel rows of one entry: its wrapped pieces, with
// an entry that carries its own line breaks split at them.
func logEntryRows(line string, width int) []string {
	rows := wrapLogLine(line, width)
	if len(rows) == 1 && strings.ContainsAny(rows[0], "\r\n") {
		return strings.Split(text.Normalize(rows[0]), "\n")
	}
	return rows
}

// wrapLogLine splits an entry wider than the panel at spaces, or mid-word
// when a path leaves no space. Printable-ASCII entries, the common case, take
// a byte-wise path; anything else gets the exact, slower wrap.
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
