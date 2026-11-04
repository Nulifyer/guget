// Package viewport owns scroll position and clips styled content to a rectangle.
package viewport

import (
	"strings"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
)

type Model struct {
	width, height int
	yOffset       int
	content       string
	lines         []string
}
type Option func(*Model)

func WithWidth(v int) Option  { return func(m *Model) { m.SetWidth(v) } }
func WithHeight(v int) Option { return func(m *Model) { m.SetHeight(v) } }
func New(opts ...Option) Model {
	m := Model{}
	for _, o := range opts {
		o(&m)
	}
	return m
}
func (m *Model) SetWidth(v int)  { m.width = max(0, v) }
func (m *Model) SetHeight(v int) { m.height = max(0, v); m.clamp() }
func (m Model) Width() int       { return m.width }
func (m Model) Height() int      { return m.height }
func (m *Model) clamp()          { m.yOffset = max(0, min(m.yOffset, max(0, len(m.lines)-m.height))) }
func (m *Model) SetContent(v string) {
	m.content = text.Normalize(v)
	m.lines = strings.Split(m.content, "\n")
	m.clamp()
}
func (m Model) GetContent() string { return m.content }
func (m *Model) SetYOffset(v int)  { m.yOffset = v; m.clamp() }
func (m *Model) GotoTop()          { m.yOffset = 0 }
func (m *Model) GotoBottom()       { m.yOffset = max(0, len(m.lines)-m.height) }
func (m Model) AtBottom() bool     { return m.yOffset >= max(0, len(m.lines)-m.height) }
func (m Model) View() string {
	lines := make([]string, m.height)
	for i := range lines {
		if j := m.yOffset + i; j < len(m.lines) {
			lines[i] = text.Truncate(m.lines[j], m.width, "")
		}
		lines[i] += strings.Repeat(" ", max(0, m.width-text.StringWidth(lines[i])))
	}
	return strings.Join(lines, "\n")
}
func (m Model) Update(event ui.Event) (Model, ui.Effect) {
	delta := 0
	switch e := event.(type) {
	case ui.KeyPress:
		switch e.String() {
		case "up", "k":
			delta = -1
		case "down", "j":
			delta = 1
		case "pgup", "ctrl+u":
			delta = -max(1, m.height-1)
		case "pgdown", "ctrl+d", " ":
			delta = max(1, m.height-1)
		case "home", "g":
			m.GotoTop()
		case "end", "G":
			m.GotoBottom()
		}
	case ui.MouseWheel:
		switch e.Mouse().Button {
		case ui.MouseWheelUp:
			delta = -3
		case ui.MouseWheelDown:
			delta = 3
		}
	}
	m.SetYOffset(m.yOffset + delta)
	return m, nil
}

func (m Model) YOffset() int        { return m.yOffset }
func (m Model) TotalLineCount() int { return len(m.lines) }
