package ui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	text "github.com/nulifyer/guget/internal/ui/text"
)

type probeElement struct {
	content                  string
	mounted, unmounted, keys int
}

func (p *probeElement) Draw(Rect) string          { return p.content }
func (p *probeElement) HandleKey(KeyEvent) Effect { p.keys++; return nil }
func (p *probeElement) Mounted()                  { p.mounted++ }
func (p *probeElement) Unmounted()                { p.unmounted++ }
func TestHostLifecycleFocusAndClipping(t *testing.T) {
	var h Host
	a := &probeElement{content: "abcdef\nsecond\noverflow"}
	b := &probeElement{content: "右"}
	nodes := []Node{{Key: "a", Element: a, Bounds: Rect{W: 3, H: 2}}, {Key: "b", Element: b, Bounds: Rect{X: 3, W: 2, H: 2}, Focused: true}}
	h.Reconcile(nodes)
	h.Reconcile(nodes)
	h.HandleKey(KeyPress(Key{Code: 'j'}))
	if a.mounted != 1 || b.mounted != 1 || a.keys != 0 || b.keys != 1 {
		t.Fatalf("lifecycle/input: %+v %+v", a, b)
	}
	if got := text.Strip(h.Draw(5, 2)); got != "abc右\nsec  " {
		t.Fatalf("bounded composition=%q", got)
	}
	h.Reconcile(nodes[:1])
	h.Disconnect()
	if a.unmounted != 1 || b.unmounted != 1 {
		t.Fatal("unmount did not run exactly once")
	}
}
func TestScreenClearsShorterRowsAndInvalidatesOnResize(t *testing.T) {
	var out bytes.Buffer
	s := NewScreen(&out)
	if err := s.Draw(NewFrame("long content\nold row"), 12, 3); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := s.Draw(NewFrame("tiny"), 12, 3); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "tiny\x1b[0m") || !strings.Contains(out.String(), "\x1b[2;1H") || !strings.Contains(out.String(), "\x1b[K") {
		t.Fatalf("stale content not erased: %q", out.String())
	}
	out.Reset()
	s.Draw(NewFrame("tiny"), 12, 3)
	if strings.Contains(out.String(), ";1H") {
		t.Fatal("unchanged rows were repainted")
	}
	out.Reset()
	s.Draw(NewFrame("tiny"), 8, 3)
	if !strings.Contains(out.String(), "\x1b[3;1H") {
		t.Fatal("resize did not invalidate all rows")
	}
}
func TestEffectsRunIndependentlyAndCanceledSendReturns(t *testing.T) {
	r := NewRuntime(context.Background(), nil)
	defer r.cancel()
	blocked := make(chan struct{})
	defer close(blocked)
	r.schedule(Batch(func() Event { <-blocked; return "slow" }, func() Event { return "fast" }))
	select {
	case e := <-r.events:
		if e != "fast" {
			t.Fatalf("event=%v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("slow effect blocked independent work")
	}
	r.cancel()
	for i := 0; i < cap(r.events); i++ {
		select {
		case r.events <- "fill":
		default:
		}
	}
	done := make(chan struct{})
	go func() { r.Send("after close"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Send blocked after cancellation")
	}
}
func TestEffectPanicReturnsFailureToLoop(t *testing.T) {
	r := NewRuntime(context.Background(), nil)
	defer r.cancel()
	r.schedule(func() Event { panic("failed") })
	select {
	case e := <-r.events:
		if _, ok := e.(effectFailed); !ok {
			t.Fatalf("event=%T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("panic lost")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }
func TestScreenPropagatesWriteFailure(t *testing.T) {
	if NewScreen(brokenWriter{}).Draw(NewFrame("x"), 1, 1) == nil {
		t.Fatal("write failure ignored")
	}
}
