package main

import (
	"strings"

	ui "github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
)

func (s *sourcesOverlay) FooterKeys() []kv {
	return []kv{{"↑↓", "scroll"}, {"esc", "close"}}
}

func (s *sourcesOverlay) HandleKey(msg ui.KeyEvent) ui.Effect {
	switch msg.String() {
	case "[":
		s.Resize(-4)
	case "]":
		s.Resize(4)
	case "esc", "s", "q":
		s.closeOverlay()
	default:
		var cmd ui.Effect
		s.vp, cmd = s.vp.Update(msg)
		return cmd
	}
	return nil
}

func (s *sourcesOverlay) Render() string {
	w := s.Width()
	innerW := w - 6 // border (2) + padding (2*2)

	var lines []string
	lines = append(lines,
		overlayTitleLine("NuGet Sources", innerW),
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
			nameW := innerW
			if src.Username != "" {
				nameW = max(1, innerW/2)
			}
			name := nameStyle.Render(truncate(src.Name, nameW))
			auth := ""
			if src.Username != "" {
				auth = "  " + styleSubtle.Render(truncate("[auth] "+src.Username, max(1, innerW-style.Width(name)-2)))
			}
			lines = append(lines, name+auth)
			lines = append(lines,
				"  "+hyperlink(src.URL, styleSubtle.Render(truncate(src.URL, innerW-2))),
			)
			lines = append(lines, "")
		}
	}

	s.vp.SetWidth(innerW)
	s.vp.SetHeight(max(1, min(len(lines)-2, s.app.overlayHeight()-6)))
	s.vp.SetContent(strings.Join(lines[2:], "\n"))
	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines[:2], "\n") + "\n" + s.vp.View())

	return s.centerOverlayInteractive(box, []mouseRegion{
		overlayCloseRegion(w, s.HandleKey),
		{rect: mouseRect{x: 1, y: 1, w: w - 2, h: style.Height(box) - 2}, wheel: verticalWheelHandler(s.HandleKey)},
	})
}
