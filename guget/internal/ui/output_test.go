package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockedOutputSink struct {
	mu                       sync.Mutex
	data                     bytes.Buffer
	entered                  chan struct{}
	release                  chan struct{}
	interrupted              chan struct{}
	enterOnce, interruptOnce sync.Once
	block                    bool
}

func newBlockedOutputSink() *blockedOutputSink {
	return &blockedOutputSink{entered: make(chan struct{}), release: make(chan struct{}), interrupted: make(chan struct{})}
}
func (s *blockedOutputSink) Write(data []byte) (int, error) {
	if s.block && strings.Contains(string(data), "\x1b[?2026h") {
		s.enterOnce.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-s.interrupted:
			return 0, io.ErrClosedPipe
		}
	}
	select {
	case <-s.interrupted:
		return 0, io.ErrClosedPipe
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Write(data)
}
func (s *blockedOutputSink) Interrupt()     { s.interruptOnce.Do(func() { close(s.interrupted) }) }
func (s *blockedOutputSink) Close() error   { s.Interrupt(); return nil }
func (s *blockedOutputSink) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.data.String() }

func TestOutputBackpressureKeepsSubmissionsBoundedAndLatest(t *testing.T) {
	sink := newBlockedOutputSink()
	sink.block = true
	output := newFrameOutput(context.Background(), sink, nil)
	defer output.Close()
	output.Submit(NewFrame("first"), 20, 2)
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("first frame did not enter blocked write")
	}
	select {
	case commit := <-output.Committed():
		t.Fatalf("unwritten frame acknowledged: %+v", commit)
	default:
	}
	submitted := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			output.Submit(NewFrame("intermediate"), 20, 2)
		}
		output.Submit(NewFrame("latest"), 20, 2)
		close(submitted)
	}()
	select {
	case <-submitted:
	case <-time.After(time.Second):
		t.Fatal("frame submission waited for terminal output")
	}
	output.mu.Lock()
	pending := output.pending
	output.mu.Unlock()
	if pending == nil || pending.frame.Content != "latest" || len(output.wake) > 1 {
		t.Fatal("pending output is not bounded to the latest frame")
	}
	close(sink.release)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case commit := <-output.Committed():
			if commit.err != nil {
				t.Fatal(commit.err)
			}
			if commit.frame.Content == "latest" {
				contents := sink.String()
				if strings.Contains(contents, "intermediate") || !strings.Contains(contents, "latest") {
					t.Fatalf("output did not coalesce obsolete frames: %q", contents)
				}
				return
			}
		case <-deadline.C:
			t.Fatal("latest frame was not acknowledged")
		}
	}
}

func TestOutputCancellationInterruptsWriteAndRunsCleanup(t *testing.T) {
	sink := newBlockedOutputSink()
	sink.block = true
	cleaned := make(chan struct{})
	output := newFrameOutput(context.Background(), sink, func() { close(cleaned) })
	output.Submit(NewFrame("blocked"), 20, 2)
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("write did not block")
	}
	stopped := make(chan struct{})
	go func() { output.Close(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("output shutdown waited for terminal to drain")
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("cleanup did not run after canceling write")
	}
	select {
	case commit := <-output.Committed():
		t.Fatalf("canceled write was acknowledged: %+v", commit)
	default:
	}
	output.Submit(NewFrame("after shutdown"), 20, 2)
}

type failedOutputSink struct{}

func (failedOutputSink) Write([]byte) (int, error) { return 0, errors.New("output failed") }
func (failedOutputSink) Interrupt()                {}
func (failedOutputSink) Close() error              { return nil }
func TestOutputReportsInitializationFailure(t *testing.T) {
	output := newFrameOutput(context.Background(), failedOutputSink{}, nil)
	defer output.Close()
	select {
	case commit := <-output.Committed():
		if commit.err == nil {
			t.Fatal("output error was lost")
		}
	case <-time.After(time.Second):
		t.Fatal("initialization failure was not reported")
	}
}

func TestOutputResizeInvalidationSurvivesCoalescedRoundTrip(t *testing.T) {
	sink := newBlockedOutputSink()
	sink.block = true
	output := newFrameOutput(context.Background(), sink, nil)
	defer output.Close()
	initial := NewFrame("same first row\nsame bottom row")
	initial.OnMouse = func(MouseEvent) Effect { return func() Event { return "initial" } }
	output.Submit(initial, 20, 2)
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("initial frame did not block")
	}
	// A physical resize can reflow terminal cells even when the intermediate
	// frame is coalesced away and the final dimensions/content equal the first.
	output.Invalidate()
	output.Submit(NewFrame("coalesced narrow frame"), 10, 1)
	output.Invalidate()
	final := NewFrame(initial.Content)
	final.OnMouse = func(MouseEvent) Effect { return func() Event { return "final" } }
	output.Submit(final, 20, 2)
	close(sink.release)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case commit := <-output.Committed():
			if commit.err != nil {
				t.Fatal(commit.err)
			}
			if commit.frame.OnMouse == nil || commit.frame.OnMouse(nil)() != "final" {
				continue
			}
			contents := sink.String()
			if strings.Contains(contents, "coalesced narrow frame") {
				t.Fatal("intermediate resize frame was not coalesced")
			}
			if strings.Count(contents, "\x1b[1;1H") != 2 || strings.Count(contents, "\x1b[2;1H") != 2 || strings.Count(contents, "same bottom row") != 2 {
				t.Fatalf("returning to the same geometry did not repaint every row: %q", contents)
			}
			return
		case <-deadline.C:
			t.Fatal("round-trip resize frame was not committed")
		}
	}
}
