package main

import (
	"fmt"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
)

func timeAgo(t time.Time) string {
	// NuGet uses 1900-01-01 as a sentinel "no publish date" value.
	// Treat anything before 2005 as unknown rather than showing nonsense like "126 years ago".
	if t.IsZero() || t.Year() < 2005 {
		return ""
	}
	d := time.Since(t)
	days := int(d.Hours() / 24)
	if days < 1 {
		return "today"
	}
	months := days / 30
	years := days / 365
	if years > 0 {
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
	if months > 0 {
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	}
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

func clampW(w, minW, maxW int) int {
	if w < minW {
		w = minW
	}
	if w > maxW {
		w = maxW
	}
	if w < 10 {
		w = 10
	}
	return w
}

func padRight(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func truncateStyled(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	var visible int
	var result strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			result.WriteRune(r)
			continue
		}
		if inEsc {
			result.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if visible >= n {
			break
		}
		result.WriteRune(r)
		visible++
	}
	result.WriteString("\x1b[0m")
	return result.String()
}

// hyperlinkEnabled controls whether OSC 8 escape codes are emitted.
// Disabled when --no-color is active.
var hyperlinkEnabled = true

// hyperlink wraps text in an OSC 8 terminal hyperlink.
// Unsupported terminals silently ignore the escape codes.
func hyperlink(url, text string) string {
	if !hyperlinkEnabled || url == "" {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\ ↗"
}

// advisoryLabel extracts a short display label from an advisory URL.
// e.g. "https://github.com/advisories/GHSA-5crp-9r3c-p9vr" → "GHSA-5crp-9r3c-p9vr"
func advisoryLabel(url string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 && i < len(url)-1 {
		return url[i+1:]
	}
	return url
}

func wordWrap(s string, width int) string {
	words := strings.Fields(s)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len()+len(w)+1 > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteString(" ")
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "\n")
}

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// renderToPanel finalizes pre-rendered content into a panel at exact outer
// dimensions. Content lines are truncated or padded vertically to fit exactly
// within the style's content area (outerH − vertical frame). This runs BEFORE
// lipgloss Render so that alignTextVertical (which does not truncate) receives
// content that already fits, producing deterministic panel heights.
func renderToPanel(s lipgloss.Style, outerW, outerH int, content string) string {
	contentH := outerH - s.GetVerticalFrameSize()
	if contentH < 1 {
		contentH = 1
	}
	lines := strings.Split(content, "\n")
	if len(lines) > contentH {
		lines = lines[:contentH]
	}
	for len(lines) < contentH {
		lines = append(lines, "")
	}
	return s.Width(outerW).Height(outerH).Render(strings.Join(lines, "\n"))
}
