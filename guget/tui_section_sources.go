package main

import (
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
)

func (s *sourcesOverlay) FooterKeys() []kv {
	return []kv{{"esc", "close"}}
}

func (s *sourcesOverlay) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
	case "]":
		s.Resize(4)
	case "esc", "s", "q":
		s.closeOverlay()
	}
	return nil
}

func (s *sourcesOverlay) Render() string {
	w := s.Width()
	innerW := w - 6 // border (2) + padding (2*2)

	var lines []string
	lines = append(lines,
		styleAccentBold.Render("NuGet Sources"),
	)
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	if len(s.app.ctx.Sources) == 0 {
		lines = append(lines,
			styleMuted.Render("No sources detected"),
		)
	} else {
		for _, src := range s.app.ctx.Sources {
			nameStyle := styleTextBold
			name := nameStyle.Render(truncate(src.Name, innerW-18))
			auth := ""
			if src.Username != "" {
				auth = "  " + styleMuted.Render("🔒 "+src.Username)
			}
			lines = append(lines, name+auth)
			lines = append(lines,
				"  "+hyperlink(src.URL, styleSubtle.Render(truncate(src.URL, innerW-2))),
			)
			lines = append(lines, "")
		}
	}

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))

	return s.centerOverlay(box)
}
