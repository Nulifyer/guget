package main

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

func (m Model) renderSourcesOverlay() string {
	w := clampW(90+m.overlayWidthOffset, 40, m.ctx.Width-4)
	innerW := w - 6 // border (2) + padding (2*2)

	var lines []string
	lines = append(lines,
		styleAccentBold.Render("NuGet Sources"),
	)
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	if len(m.ctx.Sources) == 0 {
		lines = append(lines,
			styleMuted.Render("No sources detected"),
		)
	} else {
		for _, src := range m.ctx.Sources {
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

	return lipgloss.Place(m.ctx.Width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}
