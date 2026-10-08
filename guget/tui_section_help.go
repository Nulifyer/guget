package main

import (
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (s *helpOverlay) FooterKeys() []kv {
	return []kv{{"↑↓", "scroll"}, {"esc", "close"}}
}

func (s *helpOverlay) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
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
		var cmd bubble_tea.Cmd
		s.vp, cmd = s.vp.Update(msg)
		return cmd
	}
	return nil
}

func (s *helpOverlay) refreshView() {
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
			if w := lipgloss.Width(row[0]); w > maxKeyW {
				maxKeyW = w
			}
		}
	}
	maxKeyW += 2

	var lines []string

	for _, sec := range sections {
		lines = append(lines, "")
		lines = append(lines, titleStyle.Render(sec.title))
		lines = append(lines, dimStyle.Render(strings.Repeat("─", maxKeyW+32)))
		for _, row := range sec.rows {
			k := keyStyle.Render(padRight(row[0], maxKeyW))
			d := descStyle.Render(row[1])
			lines = append(lines, k+"  "+d)
		}
	}
	w := s.Width()

	content := strings.Join(lines, "\n")
	// Available height for scrollable content inside the overlay box after the
	// fixed title and divider rows.
	maxH := s.app.overlayHeight() - 8
	if maxH < 8 {
		maxH = 8
	}

	s.vp.SetWidth(w - 4)
	s.vp.SetHeight(maxH)
	s.vp.SetContent(content)
}

func (s *helpOverlay) Render() string {
	w := s.Width()
	innerW := w - 6

	content := strings.Join([]string{
		overlayTitleLine("Keybindings", innerW),
		styleBorder.Render(strings.Repeat("─", innerW)),
		s.vp.View(),
	}, "\n")

	box := styleOverlay.
		Width(w).
		Render(content)
	regions := []mouseRegion{
		overlayCloseRegion(w, s.HandleKey),
		{
			rect:  mouseRect{x: 1, y: 1, w: w - 2, h: lipgloss.Height(box) - 2},
			wheel: verticalWheelHandler(s.HandleKey),
		},
	}

	return s.centerOverlayInteractive(box, regions)
}
