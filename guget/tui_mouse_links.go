package main

import (
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
		relClose := strings.Index(content[textStart:], osc8Close)
		if relClose < 0 {
			break
		}
		closeStart := textStart + relClose
		end := closeStart + len(osc8Close)
		if strings.HasPrefix(content[end:], " ↗") {
			end += len(" ↗")
		}

		url := content[urlStart:urlEnd]
		if url != "" {
			lineStart := strings.LastIndex(content[:start], "\n") + 1
			x := ansi.StringWidth(content[lineStart:start])
			y := strings.Count(content[:start], "\n")
			w := ansi.StringWidth(content[start:end])
			if w > 0 {
				linkURL := url
				regions = append(regions, mouseRegion{
					rect: mouseRect{x: x, y: y, w: w, h: 1},
					click: func(bubble_tea.MouseMsg) bubble_tea.Cmd {
						return func() bubble_tea.Msg { return openURLRequestedMsg{url: linkURL} }
					},
				})
			}
		}
		offset = end
	}
	return regions
}
