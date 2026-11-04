package main

import (
	"strings"

	ui "github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
)

func (s *helpOverlay) FooterKeys() []kv {
	return []kv{{"↑↓", "scroll"}, {"esc", "close"}}
}

func (s *helpOverlay) HandleKey(msg ui.KeyEvent) ui.Effect {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		s.refreshView()
	case "]":
		s.Resize(4)
		s.refreshView()
	case "esc", "?", "q":
		s.closeOverlay()
	default:
		var cmd ui.Effect
		s.vp, cmd = s.vp.Update(msg)
		return cmd
	}
	return nil
}

func (s *helpOverlay) refreshView() {
	w := s.Width()
	innerW := w - 6
	sections := mainKeyHelpSections()
	sections = append(sections, keyHelpSection{
		title: "Mouse", rows: [][2]string{
			{"left click", "select rows, tabs, links, and buttons"},
			{"wheel", "navigate or scroll under the pointer"},
			{"Shift + mouse", "native terminal selection"},
		},
	})
	sections = append(sections, []keyHelpSection{
		{
			title: "Version picker  (v)",
			rows: [][2]string{
				{"↑ / ↓  or  j / k", "move cursor"},
				{"u", "apply version (this project)"},
				{"U", "apply version (all projects)"},
				{"enter", "apply version"},
				{"esc / q", "close picker"},
			},
		},
		{
			title: "Dependency tree  (t / T)",
			rows: [][2]string{
				{"↑ / ↓  or  j / k", "scroll content"},
				{"esc", "close panel"},
			},
		},
		{
			title: "Release notes  (n)",
			rows: [][2]string{
				{"tab", "switch focus between releases and notes"},
				{"↑ / ↓  or  j / k", "navigate releases (left) or scroll notes (right)"},
				{"esc", "close panel"},
			},
		},
	}...)

	keyStyle := styleAccentBold
	titleStyle := styleAccentBold
	descStyle := styleSubtle
	dimStyle := styleBorder

	// Compute key column width across all sections.
	maxKeyW := 0
	for _, sec := range sections {
		for _, row := range sec.rows {
			if w := style.Width(row[0]); w > maxKeyW {
				maxKeyW = w
			}
		}
	}
	maxKeyW += 2

	var lines []string

	for _, sec := range sections {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, titleStyle.Render(sec.title))
		lines = append(lines, dimStyle.Render(strings.Repeat("─", innerW)))
		for _, row := range sec.rows {
			k := keyStyle.Render(padRight(row[0], maxKeyW))
			description := strings.Split(wordWrap(row[1], max(1, innerW-maxKeyW-2)), "\n")
			lines = append(lines, k+"  "+descStyle.Render(description[0]))
			for _, continuation := range description[1:] {
				lines = append(lines, strings.Repeat(" ", maxKeyW+2)+descStyle.Render(continuation))
			}
		}
	}

	if s.app.overlayHeight() < 12 && len(lines) > 0 {
		lines = lines[1:]
	}
	content := strings.Join(lines, "\n")
	// Available height for scrollable content inside the overlay box after the
	// fixed title and divider rows.
	maxH := s.app.overlayHeight() - s.boxStyle().GetVerticalFrameSize() - 2
	if maxH < 1 {
		maxH = 1
	}

	s.vp.SetWidth(innerW)
	s.vp.SetHeight(maxH)
	s.vp.SetContent(content)
}

func (s *helpOverlay) boxStyle() style.Style {
	if s.app.overlayHeight() < 12 {
		return styleOverlay.Padding(0, 2)
	}
	return styleOverlay
}

func (s *helpOverlay) Render() string {
	w := s.Width()
	innerW := w - 6
	s.vp.SetWidth(innerW)
	s.vp.SetHeight(max(1, s.app.overlayHeight()-6))

	content := strings.Join([]string{
		overlayTitleLine("Keybindings", innerW),
		styleBorder.Render(strings.Repeat("─", innerW)),
		s.vp.View(),
	}, "\n")

	box := s.boxStyle().
		Width(w).
		Render(content)
	close := overlayCloseRegion(w, s.HandleKey)
	if s.app.overlayHeight() < 12 {
		close.rect.y = 1
	}
	regions := []mouseRegion{
		close,
		{
			rect:  mouseRect{x: 1, y: 1, w: w - 2, h: style.Height(box) - 2},
			wheel: verticalWheelHandler(s.HandleKey),
		},
	}

	return s.centerOverlayInteractive(box, regions)
}
