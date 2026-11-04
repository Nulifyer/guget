// Package input provides a local single-line text component with Unicode editing.
package input

import (
	"strings"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
	"github.com/rivo/uniseg"
)

type Model struct {
	Placeholder string
	CharLimit   int
	width       int
	value       []string
	cursor      int
	focused     bool
}

func New() Model                { return Model{width: 44} }
func (m *Model) SetWidth(w int) { m.width = max(1, w) }
func (m *Model) SetValue(s string) {
	m.value = nil
	g := uniseg.NewGraphemes(text.Sanitize(s))
	for g.Next() {
		m.value = append(m.value, g.Str())
	}
	if m.CharLimit > 0 && len(m.value) > m.CharLimit {
		m.value = m.value[:m.CharLimit]
	}
	m.cursor = len(m.value)
}
func (m Model) Value() string     { return strings.Join(m.value, "") }
func (m *Model) Reset()           { m.value = nil; m.cursor = 0 }
func (m *Model) Focus() ui.Effect { m.focused = true; return nil }
func (m *Model) Blur()            { m.focused = false }
func (m *Model) insert(s string) {
	g := uniseg.NewGraphemes(text.Sanitize(s))
	for g.Next() {
		if m.CharLimit > 0 && len(m.value) >= m.CharLimit {
			break
		}
		m.value = append(m.value, "")
		copy(m.value[m.cursor+1:], m.value[m.cursor:])
		m.value[m.cursor] = g.Str()
		m.cursor++
	}
}
func (m Model) Update(event ui.Event) (Model, ui.Effect) {
	if !m.focused {
		return m, nil
	}
	switch k := event.(type) {
	case ui.PasteEvent:
		m.insert(k.Text)
	case ui.KeyPress:
		switch k.String() {
		case "left", "ctrl+b":
			m.cursor = max(0, m.cursor-1)
		case "right", "ctrl+f":
			m.cursor = min(len(m.value), m.cursor+1)
		case "home", "ctrl+a":
			m.cursor = 0
		case "end", "ctrl+e":
			m.cursor = len(m.value)
		case "backspace":
			if m.cursor > 0 {
				m.value = append(m.value[:m.cursor-1], m.value[m.cursor:]...)
				m.cursor--
			}
		case "delete", "ctrl+d":
			if m.cursor < len(m.value) {
				m.value = append(m.value[:m.cursor], m.value[m.cursor+1:]...)
			}
		case "ctrl+u":
			m.value = m.value[m.cursor:]
			m.cursor = 0
		case "ctrl+k":
			m.value = m.value[:m.cursor]
		case "ctrl+w":
			for m.cursor > 0 && m.value[m.cursor-1] == " " {
				m.value = append(m.value[:m.cursor-1], m.value[m.cursor:]...)
				m.cursor--
			}
			for m.cursor > 0 && m.value[m.cursor-1] != " " {
				m.value = append(m.value[:m.cursor-1], m.value[m.cursor:]...)
				m.cursor--
			}
		default:
			if k.Mod&(ui.ModCtrl|ui.ModAlt) == 0 && k.Code >= 32 {
				s := k.Text
				if s == "" {
					s = string(k.Code)
				}
				m.insert(s)
			}
		}
	}
	return m, nil
}
func (m Model) View() string {
	w := max(1, m.width-2)
	prefix := "> "
	if len(m.value) == 0 {
		s := m.Placeholder
		if m.focused && len(s) > 0 {
			g := uniseg.NewGraphemes(s)
			if g.Next() {
				first := g.Str()
				s = "\x1b[7m" + first + "\x1b[0m" + s[len(first):]
			}
		}
		return prefix + text.Truncate(s, w, "")
	}
	start := 0
	cursorWidth := 1
	if m.cursor < len(m.value) {
		cursorWidth = max(1, text.StringWidth(m.value[m.cursor]))
	}
	for start < m.cursor && text.StringWidth(strings.Join(m.value[start:m.cursor], ""))+cursorWidth > w {
		start++
	}
	var b strings.Builder
	used := 0
	for j := start; j <= len(m.value); j++ {
		v := " "
		if j < len(m.value) {
			v = m.value[j]
		}
		n := text.StringWidth(v)
		if used+n > w {
			break
		}
		if j == m.cursor && m.focused {
			b.WriteString("\x1b[7m" + v + "\x1b[0m")
		} else {
			b.WriteString(v)
		}
		used += n
	}
	return prefix + b.String()
}
