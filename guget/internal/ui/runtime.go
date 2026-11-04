package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nulifyer/guget/internal/ui/term"
	text "github.com/nulifyer/guget/internal/ui/text"
)

// Runtime serializes component mutations, runs effects asynchronously, and
// commits bounded frames. Send applies backpressure without spawning goroutines.
type Runtime struct {
	ctx    context.Context
	cancel context.CancelFunc
	root   Component
	events chan Event
}

func NewRuntime(parent context.Context, root Component) *Runtime {
	ctx, cancel := context.WithCancel(parent)
	return &Runtime{ctx: ctx, cancel: cancel, root: root, events: make(chan Event, 256)}
}
func (r *Runtime) Send(e Event) {
	if e == nil {
		return
	}
	select {
	case r.events <- e:
	case <-r.ctx.Done():
	}
}
func (r *Runtime) schedule(effect Effect) {
	if effect == nil {
		return
	}
	go func() {
		defer func() {
			if p := recover(); p != nil {
				r.Send(effectFailed{fmt.Errorf("background operation: %v", p)})
			}
		}()
		select {
		case <-r.ctx.Done():
			return
		default:
		}
		result := effect()
		switch e := result.(type) {
		case Effects:
			for _, effect := range e {
				r.schedule(effect)
			}

		default:
			r.Send(result)
		}
	}()
}
func (r *Runtime) Run() (err error) {
	terminal, err := term.OpenTerminal()
	if err != nil {
		r.cancel()
		return err
	}
	defer terminal.Restore()
	defer r.cancel()
	defer r.root.Disconnect()
	out := os.Stdout
	defer func() {
		_, _ = io.WriteString(out, "\x1b[0m\x1b]8;;\x1b\\\x1b[?1002;1006l\x1b[?2004l\x1b[?25h\x1b[?1049l")
	}()
	if _, err = io.WriteString(out, "\x1b[?1049h\x1b[?25l\x1b[?2004h"); err != nil {
		return err
	}
	resize, stopResize := term.WatchResize()
	defer stopResize()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	input, closeInput, err := openInput()
	if err != nil {
		return err
	}
	defer closeInput()
	reader := term.NewKeyReader(input)
	go func() {
		for {
			e, err := reader.Read()
			if err != nil {
				r.Send(inputClosed{err})
				return
			}
			if e := decodeInput(e); e != nil {
				r.Send(e)
			}
			select {
			case <-r.ctx.Done():
				return
			default:
			}
		}
	}()
	r.schedule(r.root.HandleEvent(ResizeEvent{terminal.Width, terminal.Height}))
	r.schedule(r.root.Connect())
	screen := NewScreen(out)
	var frame Frame
	draw := func() error { frame = r.root.Render(); return screen.Draw(frame, terminal.Width, terminal.Height) }
	if err = draw(); err != nil {
		return err
	}
	// A frame is committed at most every 16ms while input and result events keep
	// flowing. Continuous metadata or log events cannot starve keyboard handling.
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	dirty := false
	for {
		select {
		case <-r.ctx.Done():
			return nil
		case <-signals:
			return nil
		case <-resize:
			if terminal.RefreshSize() {
				r.schedule(r.root.HandleEvent(ResizeEvent{terminal.Width, terminal.Height}))
				screen.Invalidate()
				dirty = true
			}
		case e := <-r.events:
			switch e := e.(type) {
			case QuitEvent:
				return nil
			case effectFailed:
				return e.err
			case inputClosed:
				if e.err == io.EOF {
					return nil
				}
				return e.err
			case MouseEvent:
				if frame.OnMouse != nil {
					r.schedule(frame.OnMouse(e))
				}
			default:
				r.schedule(r.root.HandleEvent(e))
			}
			dirty = true
		case <-tick.C:
			if dirty {
				if err = draw(); err != nil {
					return err
				}
				dirty = false
			}
		}
	}
}

type inputClosed struct{ err error }
type effectFailed struct{ err error }

func decodeInput(event term.InputMsg) Event {
	switch e := event.(type) {
	case term.Key:
		codes := map[term.KeyKind]rune{term.KeyEnter: KeyEnter, term.KeyTab: KeyTab, term.KeyBackTab: KeyTab, term.KeyBackspace: KeyBackspace, term.KeyEsc: KeyEscape, term.KeyUp: KeyUp, term.KeyDown: KeyDown, term.KeyLeft: KeyLeft, term.KeyRight: KeyRight, term.KeyHome: KeyHome, term.KeyEnd: KeyEnd, term.KeyPgUp: KeyPgUp, term.KeyPgDn: KeyPgDown, term.KeyDelete: KeyDelete}
		k := Key{Code: e.Rune}
		if code, ok := codes[e.Kind]; ok {
			k.Code = code
		}
		if e.Ctrl {
			k.Mod |= ModCtrl
		}
		if e.Alt {
			k.Mod |= ModAlt
		}
		if e.Shift || e.Kind == term.KeyBackTab {
			k.Mod |= ModShift
		}
		return KeyPress(k)
	case term.PasteMsg:
		return PasteEvent{e.Text}
	case term.MouseMsg:
		if e.Action != term.MouseActionPress {
			return nil
		}
		buttons := map[term.MouseButton]MouseButton{term.MouseButtonLeft: MouseLeft, term.MouseButtonMiddle: MouseMiddle, term.MouseButtonRight: MouseRight, term.MouseButtonWheelUp: MouseWheelUp, term.MouseButtonWheelDown: MouseWheelDown}
		button, ok := buttons[e.Button]
		if !ok {
			return nil
		}
		m := Mouse{e.X - 1, e.Y - 1, button}
		if button == MouseWheelUp || button == MouseWheelDown {
			return MouseWheel(m)
		}
		return MouseClick(m)
	}
	return nil
}

// Screen diffs rendered rows, clears stale cells, and bounds every write to the
// terminal. A frame's final row never emits a newline, avoiding terminal scroll.
type Screen struct {
	out         io.Writer
	prev        []string
	w, h        int
	mouse       MouseMode
	initialized bool
}

func NewScreen(out io.Writer) *Screen { return &Screen{out: out} }
func (s *Screen) Invalidate()         { s.prev = nil }
func (s *Screen) Draw(frame Frame, w, h int) error {
	if w < 1 || h < 1 {
		return nil
	}
	if s.w != w || s.h != h {
		s.Invalidate()
		s.w = w
		s.h = h
	}
	lines := strings.Split(frame.Content, "\n")
	next := make([]string, h)
	var b strings.Builder
	if !s.initialized || frame.MouseMode != s.mouse {
		if frame.MouseMode == MouseModeNone {
			b.WriteString("\x1b[?1002;1006l")
		} else {
			b.WriteString("\x1b[?1002;1006h")
		}
		s.mouse = frame.MouseMode
		s.initialized = true
	}
	// Synchronized output keeps changed rows in one visible commit on terminals
	// supporting mode 2026. Other terminals ignore it.
	b.WriteString("\x1b[?2026h")
	for i := range next {
		if i < len(lines) {
			next[i] = text.Truncate(lines[i], w, "")
		}
		if i < len(s.prev) && next[i] == s.prev[i] {
			continue
		}
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[0m%s\x1b[0m\x1b]8;;\x1b\\\x1b[K", i+1, next[i])
	}
	b.WriteString("\x1b[?2026l")
	if _, err := io.WriteString(s.out, b.String()); err != nil {
		return err
	}
	s.prev = next
	return nil
}
