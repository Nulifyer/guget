package input

import (
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
)

func TestEditingKeepsGraphemesIntactAndCursorVisible(t *testing.T) {
	m := New()
	m.SetWidth(8)
	m.Focus()
	m.SetValue("Café你好👩‍💻")
	m, _ = m.Update(ui.KeyPress(ui.Key{Code: ui.KeyBackspace}))
	if m.Value() != "Café你好" {
		t.Fatalf("backspace split grapheme: %q", m.Value())
	}
	m, _ = m.Update(ui.KeyPress(ui.Key{Code: ui.KeyLeft}))
	m, _ = m.Update(ui.KeyPress(ui.Key{Code: ui.KeyBackspace}))
	if m.Value() != "Café好" {
		t.Fatalf("cursor edit=%q", m.Value())
	}
	if !strings.Contains(m.View(), "\x1b[7m") || text.StringWidth(m.View()) > 8 {
		t.Fatalf("cursor not visible in bounds: %q", m.View())
	}
}
func TestPasteIsSanitizedAndLimited(t *testing.T) {
	m := New()
	m.CharLimit = 5
	m.Focus()
	m, _ = m.Update(ui.PasteEvent{Text: "a\nb\x00cdextra"})
	if m.Value() != "a bcd" {
		t.Fatalf("paste=%q", m.Value())
	}
}

func TestWideCursorScrollsWholeGraphemesIntoView(t *testing.T) {
	m := New()
	m.SetWidth(6)
	m.Focus()
	m.SetValue("abc你d")
	m, _ = m.Update(ui.KeyPress(ui.Key{Code: ui.KeyLeft}))
	m, _ = m.Update(ui.KeyPress(ui.Key{Code: ui.KeyLeft}))
	if !strings.Contains(m.View(), "\x1b[7m你") {
		t.Fatalf("wide cursor hidden: %q", m.View())
	}
}
