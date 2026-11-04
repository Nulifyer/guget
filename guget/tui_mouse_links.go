package main

import (
	"strings"

	ui "github.com/nulifyer/guget/internal/ui"
	ansi "github.com/nulifyer/guget/internal/ui/text"
)

const (
	osc8OpenPrefix = "\x1b]8;;"
	osc8End        = "\x1b\\"
	osc8Close      = "\x1b]8;;\x1b\\"
)

// linkMouseRegions extracts OSC 8 links from the final rendered frame. Guget
// keeps emitting OSC 8 for terminals that handle links themselves; these
// regions provide the same behavior while terminal mouse reporting is active.
func linkMouseRegions(content string) []mouseRegion {
	var regions []mouseRegion
	for offset := 0; offset < len(content); {
		relStart := strings.Index(content[offset:], osc8OpenPrefix)
		if relStart < 0 {
			break
		}
		start := offset + relStart
		urlStart := start + len(osc8OpenPrefix)
		relURLEnd := strings.Index(content[urlStart:], osc8End)
		if relURLEnd < 0 {
			break
		}
		urlEnd := urlStart + relURLEnd
		textStart := urlEnd + len(osc8End)
		url := content[urlStart:urlEnd]
		// Clipped panels emit standalone OSC 8 resets. They do not start a
		// link and must not consume the next real link while seeking a close.
		if url == "" {
			offset = textStart
			continue
		}
		relClose := strings.Index(content[textStart:], osc8Close)
		if relClose < 0 {
			break
		}
		closeStart := textStart + relClose
		end := closeStart + len(osc8Close)
		if strings.HasPrefix(content[end:], " ↗") {
			end += len(" ↗")
		}

		if url != "" {
			lineStart := strings.LastIndex(content[:start], "\n") + 1
			x := ansi.StringWidth(content[lineStart:start])
			y := strings.Count(content[:start], "\n")
			w := ansi.StringWidth(content[start:end])
			if w > 0 {
				linkURL := url
				regions = append(regions, mouseRegion{
					rect: mouseRect{x: x, y: y, w: w, h: 1},
					click: func(ui.MouseEvent) ui.Effect {
						return func() ui.Event { return openURLRequestedMsg{url: linkURL} }
					},
				})
			}
		}
		offset = end
	}
	return regions
}
