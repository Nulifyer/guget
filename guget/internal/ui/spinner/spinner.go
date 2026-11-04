package spinner

import (
	"time"

	"github.com/nulifyer/guget/internal/ui"
	"github.com/nulifyer/guget/internal/ui/style"
)

var Dot = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type TickMsg struct{}
type Model struct {
	Spinner []string
	Style   style.Style
	frame   int
}

func New() Model               { return Model{Spinner: Dot} }
func (m Model) Tick() ui.Event { return TickMsg{} }
func (m Model) Update(_ TickMsg) (Model, ui.Effect) {
	m.frame++
	return m, ui.Tick(100*time.Millisecond, func(time.Time) ui.Event { return TickMsg{} })
}
func (m Model) View() string {
	frames := m.Spinner
	if len(frames) == 0 {
		frames = Dot
	}
	return m.Style.Render(frames[m.frame%len(frames)])
}
