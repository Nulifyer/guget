package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nulifyer/guget/internal/ui/term"
	text "github.com/nulifyer/guget/internal/ui/text"
)

// Runtime serializes component mutations, runs effects asynchronously, and
// commits bounded frames. Send applies backpressure without spawning goroutines.
type Runtime struct {
	ctx     context.Context
	cancel  context.CancelFunc
	root    Component
	events  chan Event
	effects sync.WaitGroup
}

func NewRuntime(parent context.Context, root Component) *Runtime {
	ctx, cancel := context.WithCancel(parent)
	return &Runtime{ctx: ctx, cancel: cancel, root: root, events: make(chan Event, 256)}
}

// Context belongs to this runtime, including a user-requested quit. Components
// use it for background work so shutdown can cancel and drain file recovery.
func (r *Runtime) Context() context.Context { return r.ctx }

func (r *Runtime) shutdown() {
	r.cancel()
	r.root.Disconnect()
	r.effects.Wait()
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
	if effect == nil || r.ctx.Err() != nil {
		return
	}
	r.effects.Add(1)
	go func() {
		defer r.effects.Done()
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
	defer func() { err = errors.Join(err, terminal.Restore()) }()
	defer r.shutdown()
	output, err := newTerminalOutput(r.ctx)
	if err != nil {
		return err
	}
	defer output.Close()
	resize, stopResize := term.WatchResize()
	defer stopResize()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	input, closeInput, err := openInput()
	if err != nil {
		return err
	}
	inputDone := make(chan struct{})
	defer func() {
		r.cancel()
		closeInput()
		<-inputDone
	}()
	reader := term.NewKeyReader(input)
	go func() {
		defer close(inputDone)
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
	var frame Frame
	draw := func() { output.Submit(r.root.Render(), terminal.Width, terminal.Height) }
	draw()
	// A frame is committed at most every 16ms while input and result events keep
	// flowing. Continuous metadata or log events cannot starve keyboard handling.
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	dirty := false
	resizePending := false
	for {
		select {
		case <-r.ctx.Done():
			return nil
		case <-signals:
			return nil
		case commit := <-output.Committed():
			if commit.err != nil {
				return commit.err
			}
			frame = commit.frame
		case <-resize:
			if !resizePending {
				resizePending = true
				r.schedule(func() Event {
					width, height, sizeErr := terminal.Size()
					return terminalSizeResult{width, height, sizeErr}
				})
			}
		case e := <-r.events:
			switch e := e.(type) {
			case terminalSizeResult:
				resizePending = false
				if e.err != nil || e.width == terminal.Width && e.height == terminal.Height {
					continue
				}
				terminal.Width, terminal.Height = e.width, e.height
				output.Invalidate()
				r.schedule(r.root.HandleEvent(ResizeEvent{e.width, e.height}))
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
				effect, mouseErr := dispatchCommittedMouse(&frame, output.Committed(), e)
				if mouseErr != nil {
					return mouseErr
				}
				r.schedule(effect)
			default:
				r.schedule(r.root.HandleEvent(e))
			}
			dirty = true
		case <-tick.C:
			if dirty {
				draw()
				dirty = false
			}
		}
	}
}

func dispatchCommittedMouse(frame *Frame, commits <-chan outputCommit, event MouseEvent) (Effect, error) {
	// A select may choose input while a newer visible frame acknowledgement is
	// also ready. Bind the click to the newest committed geometry first.
	for {
		select {
		case commit := <-commits:
			if commit.err != nil {
				return nil, commit.err
			}
			*frame = commit.frame
		default:
			if frame.OnMouse != nil {
				return frame.OnMouse(event), nil
			}
			return nil, nil
		}
	}
}

type inputClosed struct{ err error }
type effectFailed struct{ err error }
type terminalSizeResult struct {
	width, height int
	err           error
}

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
