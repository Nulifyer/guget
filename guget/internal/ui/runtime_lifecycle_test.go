package ui

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMouseUsesVisibleFrameWhenInputAndCommitAreBothReady(t *testing.T) {
	selected := ""
	frame := Frame{OnMouse: func(MouseEvent) Effect { selected = "old row"; return nil }}
	commits := make(chan outputCommit, 1)
	commits <- outputCommit{frame: Frame{OnMouse: func(MouseEvent) Effect { selected = "visible row"; return nil }}}
	if _, err := dispatchCommittedMouse(&frame, commits, MouseClick(Mouse{X: 1, Y: 1, Button: MouseLeft})); err != nil {
		t.Fatal(err)
	}
	if selected != "visible row" {
		t.Fatalf("click selected %q instead of the visible row", selected)
	}
	failed := errors.New("terminal failed")
	commits <- outputCommit{err: failed}
	selected = ""
	if _, err := dispatchCommittedMouse(&frame, commits, MouseClick{}); !errors.Is(err, failed) || selected != "" {
		t.Fatalf("click dispatched after output failure: selected=%q error=%v", selected, err)
	}
}

type shutdownProbe struct{ disconnected bool }

func (*shutdownProbe) Connect() Effect          { return nil }
func (*shutdownProbe) HandleEvent(Event) Effect { return nil }
func (*shutdownProbe) Render() Frame            { return NewFrame("") }
func (p *shutdownProbe) Disconnect()            { p.disconnected = true }

func TestShutdownCancelsAndWaitsForEffectRecovery(t *testing.T) {
	root := &shutdownProbe{}
	r := NewRuntime(context.Background(), root)
	started := make(chan struct{})
	recovering := make(chan struct{})
	recovered := make(chan struct{})
	r.schedule(func() Event {
		close(started)
		<-r.Context().Done()
		close(recovering)
		<-recovered
		return "finished"
	})
	<-started
	done := make(chan struct{})
	go func() { r.shutdown(); close(done) }()
	select {
	case <-recovering:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel component work")
	}
	select {
	case <-done:
		t.Fatal("shutdown returned before recovery completed")
	default:
	}
	close(recovered)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not drain recovered effect")
	}
	if !root.disconnected {
		t.Fatal("component was not disconnected")
	}
}

func TestShutdownDiscardsUnstartedBatchChildren(t *testing.T) {
	r := NewRuntime(context.Background(), &shutdownProbe{})
	started, release := make(chan struct{}), make(chan struct{})
	child := make(chan struct{}, 1)
	r.schedule(func() Event {
		close(started)
		<-release
		return Effects{func() Event { child <- struct{}{}; return nil }}
	})
	<-started
	r.cancel()
	close(release)
	r.shutdown()
	select {
	case <-child:
		t.Fatal("canceled batch started new background work")
	default:
	}
}

func TestFramePacerDoesNotDelayTheResultOfAnUnchangedFrame(t *testing.T) {
	start := time.Unix(0, 0)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	p := framePacer{interval: 16 * time.Millisecond, last: start}
	// A key press draws at once, but the frame shows nothing new.
	p.dirty = true
	if draw, _ := p.next(at(30)); !draw {
		t.Fatal("a change after an idle interval did not draw at once")
	}
	p.unchanged()
	// Its result arrives 4 ms later and is drawn without waiting.
	p.dirty = true
	if draw, wait := p.next(at(34)); !draw {
		t.Fatalf("the result waited %v behind an unchanged frame", wait)
	}
	// Only one draw is free: further events that change nothing are paced.
	p.unchanged()
	p.dirty = true
	if draw, wait := p.next(at(35)); draw || wait != 15*time.Millisecond {
		t.Fatalf("second unchanged frame was not paced: draw=%v wait=%v", draw, wait)
	}
	p.armed = false // the timer fired
	if draw, _ := p.next(at(50)); !draw {
		t.Fatal("paced change was not drawn at the end of the interval")
	}
	// A frame that changed leaves the next one paced as before.
	p.dirty = true
	if draw, wait := p.next(at(52)); draw || wait != 14*time.Millisecond {
		t.Fatalf("change after a changed frame: draw=%v wait=%v", draw, wait)
	}
}

func TestFramePacerDrawsIdleChangesAtOnceAndCoalescesBursts(t *testing.T) {
	start := time.Unix(0, 0)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	p := framePacer{interval: 16 * time.Millisecond, last: start}
	if draw, wait := p.next(at(30)); draw || wait != 0 {
		t.Fatalf("nothing changed: draw=%v wait=%v", draw, wait)
	}
	p.dirty = true
	if draw, _ := p.next(at(30)); !draw {
		t.Fatal("a change after an idle interval did not draw at once")
	}
	p.dirty = true
	if draw, wait := p.next(at(35)); draw || wait != 11*time.Millisecond {
		t.Fatalf("change within the interval: draw=%v wait=%v, want a wait of 11ms", draw, wait)
	}
	p.dirty = true
	if draw, wait := p.next(at(40)); draw || wait != 0 {
		t.Fatalf("pending wait was not shared: draw=%v wait=%v", draw, wait)
	}
	p.armed = false // the timer fired
	if draw, _ := p.next(at(46)); !draw {
		t.Fatal("the burst was not drawn at the end of the interval")
	}
	if draw, wait := p.next(at(47)); draw || wait != 0 {
		t.Fatalf("drawn change drew again: draw=%v wait=%v", draw, wait)
	}
}
