// Package ui owns the terminal event loop and component lifecycle. Domain
// components mutate their own state on the loop and return effects for slow work.
package ui

import (
	"strings"
	"time"
	"unicode"
)

type Event = any

// Effect runs off the event loop and returns a result for its owning component.
type Effect func() Event
type Effects []Effect

func Batch(effects ...Effect) Effect {
	var pending Effects
	for _, e := range effects {
		if e != nil {
			pending = append(pending, e)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if len(pending) == 1 {
		return pending[0]
	}
	return func() Event { return pending }
}
func Tick(d time.Duration, result func(time.Time) Event) Effect {
	return func() Event { timer := time.NewTimer(d); defer timer.Stop(); return result(<-timer.C) }
}

type QuitEvent struct{}

func Quit() Event { return QuitEvent{} }

type ResizeEvent struct{ Width, Height int }
type PasteEvent struct{ Text string }

type Modifier uint8

const (
	ModCtrl Modifier = 1 << iota
	ModShift
	ModAlt
)
const (
	KeyEscape rune = -1 - iota
	KeyEnter
	KeyTab
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDown
	KeyBackspace
	KeyDelete
)

type Key struct {
	Code     rune
	Text     string
	Mod      Modifier
	IsRepeat bool
}

func (k Key) String() string {
	names := map[rune]string{KeyEscape: "esc", KeyEnter: "enter", KeyTab: "tab", KeyUp: "up", KeyDown: "down", KeyLeft: "left", KeyRight: "right", KeyHome: "home", KeyEnd: "end", KeyPgUp: "pgup", KeyPgDown: "pgdown", KeyBackspace: "backspace", KeyDelete: "delete"}
	s := names[k.Code]
	if s == "" {
		s = k.Text
		if s == "" {
			s = string(k.Code)
		}
	}
	var prefix []string
	if k.Mod&ModCtrl != 0 {
		prefix = append(prefix, "ctrl")
	}
	if k.Mod&ModAlt != 0 {
		prefix = append(prefix, "alt")
	}
	if k.Mod&ModShift != 0 && !unicode.IsUpper(k.Code) {
		prefix = append(prefix, "shift")
	}
	return strings.Join(append(prefix, s), "+")
}

type KeyEvent interface {
	String() string
	Key() Key
}
type KeyPress Key

func (k KeyPress) String() string { return Key(k).String() }
func (k KeyPress) Key() Key       { return Key(k) }

type KeyRelease Key

func (k KeyRelease) String() string { return Key(k).String() }
func (k KeyRelease) Key() Key       { return Key(k) }

type MouseButton int

const (
	MouseLeft MouseButton = 1 + iota
	MouseMiddle
	MouseRight
	MouseWheelUp
	MouseWheelDown
)

type Mouse struct {
	X, Y   int
	Button MouseButton
}
type MouseEvent interface{ Mouse() Mouse }
type MouseClick Mouse

func (m MouseClick) Mouse() Mouse { return Mouse(m) }

type MouseWheel Mouse

func (m MouseWheel) Mouse() Mouse { return Mouse(m) }

type MouseMode int

const (
	MouseModeNone MouseMode = iota
	MouseModeCellMotion
)

// Frame is a committed render with input hit regions captured by its owner.
// The runtime retains it until the next frame so clicks use visible geometry.
type Frame struct {
	Content   string
	MouseMode MouseMode
	OnMouse   func(MouseEvent) Effect
}

func NewFrame(content string) Frame  { return Frame{Content: content} }
func (f *Frame) SetContent(s string) { f.Content = s }

// Component has a lifecycle, encapsulated state, event handling, and rendering.
// Effects may return events, but they never render or mutate the component.
type Component interface {
	Connect() Effect
	HandleEvent(Event) Effect
	Render() Frame
	Disconnect()
}
