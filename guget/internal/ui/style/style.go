// Package style provides local terminal text and box styling. Sizes always
// include borders and padding. Render clips content to its allocated cells.
package style

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"

	text "github.com/nulifyer/guget/internal/ui/text"
)

type NoColor struct{}

func (NoColor) RGBA() (uint32, uint32, uint32, uint32) { return 0, 0, 0, 0 }
func Color(s string) color.Color {
	s = strings.TrimPrefix(s, "#")
	if len(s) == 6 {
		v, e := strconv.ParseUint(s, 16, 32)
		if e == nil {
			return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
		}
	}
	return NoColor{}
}
func HasDarkBackground(_ ...*os.File) bool {
	// COLORFGBG is supplied by terminals that advertise their background. Keep
	// detection side-effect free: probing the terminal would race the key reader.
	parts := strings.Split(os.Getenv("COLORFGBG"), ";")
	if n, e := strconv.Atoi(parts[len(parts)-1]); e == nil {
		return n < 7 || n == 8
	}
	return true
}
func Width(s string) int {
	w := 0
	for _, l := range strings.Split(s, "\n") {
		w = max(w, text.StringWidth(l))
	}
	return w
}
func Height(s string) int { return strings.Count(s, "\n") + 1 }

type Border struct{ Top, Bottom, Left, Right, TopLeft, TopRight, BottomLeft, BottomRight string }

func RoundedBorder() Border { return Border{"─", "─", "│", "│", "╭", "╮", "╰", "╯"} }
func NormalBorder() Border  { return Border{"─", "─", "│", "│", "┌", "┐", "└", "┘"} }

type Style struct {
	fg, bg, borderFG         color.Color
	bold, underline          bool
	width, height            int
	top, right, bottom, left bool
	border                   Border
	pt, pr, pb, pl           int
}

func NewStyle() Style                          { return Style{} }
func (s Style) Foreground(c color.Color) Style { s.fg = c; return s }
func (s Style) Background(c color.Color) Style { s.bg = c; return s }
func (s Style) Bold(v bool) Style              { s.bold = v; return s }
func (s Style) Underline(v bool) Style         { s.underline = v; return s }
func (s Style) Width(v int) Style              { s.width = max(0, v); return s }
func (s Style) Height(v int) Style             { s.height = max(0, v); return s }
func (s Style) Border(b Border) Style {
	s.border = b
	s.top = true
	s.right = true
	s.bottom = true
	s.left = true
	return s
}
func (s Style) BorderStyle(b Border) Style                 { s.border = b; return s }
func (s Style) BorderForeground(c color.Color) Style       { s.borderFG = c; return s }
func (s Style) BorderTopForeground(c color.Color) Style    { return s.BorderForeground(c) }
func (s Style) BorderBottomForeground(c color.Color) Style { return s.BorderForeground(c) }
func (s Style) BorderTop(v bool) Style                     { s.top = v; return s }
func (s Style) BorderBottom(v bool) Style                  { s.bottom = v; return s }
func (s Style) BorderLeft(v bool) Style                    { s.left = v; return s }
func (s Style) BorderRight(v bool) Style                   { s.right = v; return s }
func (s Style) Padding(v ...int) Style {
	if len(v) == 1 {
		s.pt = v[0]
		s.pr = v[0]
		s.pb = v[0]
		s.pl = v[0]
	}
	if len(v) == 2 {
		s.pt = v[0]
		s.pb = v[0]
		s.pr = v[1]
		s.pl = v[1]
	}
	if len(v) == 4 {
		s.pt = v[0]
		s.pr = v[1]
		s.pb = v[2]
		s.pl = v[3]
	}
	return s
}
func bit(v bool) int {
	if v {
		return 1
	}
	return 0
}
func (s Style) GetHorizontalFrameSize() int { return bit(s.left) + bit(s.right) + s.pl + s.pr }
func (s Style) GetVerticalFrameSize() int   { return bit(s.top) + bit(s.bottom) + s.pt + s.pb }
func colorCode(c color.Color, bg bool) string {
	if c == nil {
		return ""
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return ""
	}
	n := 38
	if bg {
		n = 48
	}
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", n, r>>8, g>>8, b>>8)
}
func (s Style) paint(v string) string {
	prefix := colorCode(s.fg, false) + colorCode(s.bg, true)
	if s.bold {
		prefix += "\x1b[1m"
	}
	if s.underline {
		prefix += "\x1b[4m"
	}
	if prefix == "" {
		return v
	}
	return prefix + strings.ReplaceAll(v, "\x1b[0m", "\x1b[0m"+prefix) + "\x1b[0m"
}
func (s Style) Render(content string) string {
	content = text.Normalize(content)
	fw := s.GetHorizontalFrameSize()
	w := s.width - fw
	var lines []string
	if s.width > 0 {
		w = max(0, w)
		lines = text.Wrap(content, max(1, w))
	} else {
		lines = strings.Split(content, "\n")
		w = Width(content)
	}
	if s.height > 0 {
		h := max(0, s.height-s.GetVerticalFrameSize())
		if len(lines) > h {
			lines = lines[:h]
		}
		for len(lines) < h {
			lines = append(lines, "")
		}
	}
	padded := make([]string, 0, len(lines)+s.pt+s.pb)
	for i := 0; i < s.pt; i++ {
		padded = append(padded, "")
	}
	padded = append(padded, lines...)
	for i := 0; i < s.pb; i++ {
		padded = append(padded, "")
	}
	innerW := w + s.pl + s.pr
	borderStyle := NewStyle().Foreground(s.borderFG)
	var out []string
	borderLine := func(left, fill, right string) string {
		return borderStyle.paint(left + strings.Repeat(fill, innerW) + right)
	}
	corner := func(v string, enabled bool) string {
		if enabled {
			return v
		}
		return ""
	}
	if s.top {
		out = append(out, borderLine(corner(s.border.TopLeft, s.left), s.border.Top, corner(s.border.TopRight, s.right)))
	}
	for _, line := range padded {
		line = text.Truncate(line, w, "")
		line = strings.Repeat(" ", s.pl) + line + strings.Repeat(" ", max(0, w-Width(line))+s.pr)
		line = s.paint(line)
		if s.left {
			line = borderStyle.paint(s.border.Left) + line
		}
		if s.right {
			line += borderStyle.paint(s.border.Right)
		}
		out = append(out, line)
	}
	if s.bottom {
		out = append(out, borderLine(corner(s.border.BottomLeft, s.left), s.border.Bottom, corner(s.border.BottomRight, s.right)))
	}
	return strings.Join(out, "\n")
}

type Position float64

const (
	Left   Position = 0
	Top    Position = 0
	Center Position = .5
)

func JoinHorizontal(_ Position, parts ...string) string {
	height := 0
	for _, p := range parts {
		height = max(height, Height(p))
	}
	out := make([]string, height)
	for _, p := range parts {
		lines := strings.Split(p, "\n")
		w := Width(p)
		for i := range out {
			l := ""
			if i < len(lines) {
				l = lines[i]
			}
			out[i] += l + strings.Repeat(" ", max(0, w-Width(l)))
		}
	}
	return strings.Join(out, "\n")
}
func JoinVertical(_ Position, parts ...string) string { return strings.Join(parts, "\n") }
func Place(w, h int, x, y Position, content string) string {
	lines := strings.Split(content, "\n")
	cw := Width(content)
	left := max(0, int(float64(w-cw)*float64(x)))
	top := max(0, int(float64(h-len(lines))*float64(y)))
	out := make([]string, max(0, h))
	for i := range out {
		l := ""
		if i >= top && i-top < len(lines) {
			l = strings.Repeat(" ", left) + lines[i-top]
		}
		l = text.Truncate(l, w, "")
		out[i] = l + strings.Repeat(" ", max(0, w-Width(l)))
	}
	return strings.Join(out, "\n")
}
