// Package text measures and clips styled terminal text by grapheme cells.
package text

import (
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

type token struct {
	s      string
	width  int
	escape bool
}

func tokens(s string) []token {
	var out []token
	for len(s) > 0 {
		if s[0] == 27 {
			n := 1
			if len(s) > 1 {
				switch s[1] {
				case '[':
					n = 2
					for n < len(s) {
						b := s[n]
						n++
						if b >= 0x40 && b <= 0x7e {
							break
						}
					}
				case ']':
					n = 2
					for n < len(s) {
						if s[n] == 7 {
							n++
							break
						}
						if s[n] == 27 && n+1 < len(s) && s[n+1] == '\\' {
							n += 2
							break
						}
						n++
					}
				default:
					n = 2
				}
			}
			out = append(out, token{s[:n], 0, true})
			s = s[n:]
			continue
		}
		end := strings.IndexByte(s, 27)
		if end < 0 {
			end = len(s)
		}
		g := uniseg.NewGraphemes(s[:end])
		for g.Next() {
			out = append(out, token{g.Str(), g.Width(), false})
		}
		s = s[end:]
	}
	return out
}
func Strip(s string) string {
	var b strings.Builder
	for _, t := range tokens(s) {
		if !t.escape {
			b.WriteString(t.s)
		}
	}
	return b.String()
}
func StringWidth(s string) int {
	w := 0
	for _, t := range tokens(s) {
		if !t.escape {
			w += t.width
		}
	}
	return w
}
func Truncate(s string, width int, tail string) string {
	if width <= 0 {
		return ""
	}
	if StringWidth(s) <= width {
		return s
	}
	if StringWidth(tail) > width {
		tail = ""
	}
	return Cut(s, 0, width-StringWidth(tail)) + tail
}

// Cut retains style transitions before the first visible cell, preventing a
// scrolled viewport from losing its color or OSC 8 hyperlink target.
func Cut(s string, start, end int) string {
	if end <= start {
		return ""
	}
	var b strings.Builder
	pos := 0
	for _, t := range tokens(s) {
		if t.escape {
			if pos < end {
				b.WriteString(t.s)
			}
			continue
		}
		if pos >= end {
			break
		}
		if pos >= start && pos+t.width <= end {
			b.WriteString(t.s)
		}
		pos += t.width
	}
	b.WriteString("\x1b[0m\x1b]8;;\x1b\\")
	return b.String()
}

// Normalize converts external text to terminal layout characters. CRLF release
// notes must not emit carriage returns that move the cursor through panel borders.
func Normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\t", "    ")
}

// Wrap prefers word boundaries while retaining indentation and ANSI state.
func Wrap(s string, width int) []string {
	if width < 1 {
		return []string{""}
	}
	var out []string
	for _, line := range strings.Split(Normalize(s), "\n") {
		total := StringWidth(line)
		if total <= width {
			out = append(out, line)
			continue
		}
		ts := tokens(line)
		start := 0
		for start < total {
			if total-start <= width {
				out = append(out, Cut(line, start, total))
				break
			}
			pos, lastFit, lastSpace := 0, start, -1
			for _, t := range ts {
				if t.escape {
					continue
				}
				if pos >= start && pos+t.width <= start+width {
					lastFit = pos + t.width
					if t.s == " " && pos > start {
						lastSpace = pos
					}
				}
				if pos+t.width > start+width {
					if t.s == " " && pos > start {
						lastSpace = pos
					}
					break
				}
				pos += t.width
			}
			end := lastFit
			if lastSpace > start {
				end = lastSpace
			}
			if end <= start {
				start++
				continue
			}
			out = append(out, Cut(line, start, end))
			start = end
			pos = 0
			for _, t := range ts {
				if t.escape {
					continue
				}
				if pos == start && t.s == " " {
					start += t.width
				}
				pos += t.width
				if pos > start {
					break
				}
			}
		}
	}
	return out
}

func Sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteByte(' ')
		} else if r >= 32 && r != 127 && !(r >= 128 && r < 160) && r != utf8.RuneError {
			b.WriteRune(r)
		}
	}
	return b.String()
}
