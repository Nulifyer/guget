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
