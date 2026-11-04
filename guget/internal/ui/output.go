package ui

import (
	"context"
	"io"
	"sync"
	"time"
)

const enterTerminalScreen = "\x1b[?1049h\x1b[?25l\x1b[?2004h"
const leaveTerminalScreen = "\x1b[?2026l\x1b[0m\x1b]8;;\x1b\\\x1b[?1002;1006l\x1b[?2004l\x1b[?25h\x1b[?1049l"

type outputSink interface {
	io.WriteCloser
	Interrupt()
}
type outputRequest struct {
	frame         Frame
	width, height int
	generation    uint64
}
type outputCommit struct {
	frame Frame
	err   error
}

// frameOutput serializes terminal writes and retains one pending latest frame.
// Rendering and input dispatch never wait for a slow terminal to drain.
type frameOutput struct {
	ctx        context.Context
	cancel     context.CancelFunc
	sink       outputSink
	mu         sync.Mutex
	pending    *outputRequest
	generation uint64
	wake       chan struct{}
	commits    chan outputCommit
	done       chan struct{}
}

func newTerminalOutput(parent context.Context) (*frameOutput, error) {
	sink, err := openTerminalOutput()
	if err != nil {
		return nil, err
	}
	return newFrameOutput(parent, sink, restoreTerminalOutput), nil
}
func newFrameOutput(parent context.Context, sink outputSink, cleanup func()) *frameOutput {
	ctx, cancel := context.WithCancel(parent)
	output := &frameOutput{ctx: ctx, cancel: cancel, sink: sink, wake: make(chan struct{}, 1), commits: make(chan outputCommit, 1), done: make(chan struct{})}
	go output.run(cleanup)
	go func() {
		select {
		case <-ctx.Done():
		case <-output.done:
			return
		}
		// A synchronous Windows write can begin just after cancellation. Repeat
		// cancellation until the worker acknowledges it, covering that interval.
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			sink.Interrupt()
			select {
			case <-output.done:
				return
			case <-ticker.C:
			}
		}
	}()
	return output
}
func (o *frameOutput) Submit(frame Frame, width, height int) {
	if o.ctx.Err() != nil {
		return
	}
	o.mu.Lock()
	o.pending = &outputRequest{frame, width, height, o.generation}
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// Invalidate records terminal reflow even when intermediate resize frames are
// coalesced and the next committed frame has the original dimensions.
func (o *frameOutput) Invalidate() {
	o.mu.Lock()
	o.generation++
	o.mu.Unlock()
}
func (o *frameOutput) Committed() <-chan outputCommit { return o.commits }
func (o *frameOutput) Close() {
	o.cancel()
	o.sink.Interrupt()
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-o.done:
	case <-timer.C:
		_ = o.sink.Close()
	}
}
func (o *frameOutput) notify(commit outputCommit) {
	select {
	case o.commits <- commit:
		return
	default:
	}
	select {
	case <-o.commits:
	default:
	}
	select {
	case o.commits <- commit:
	default:
	}
}
func (o *frameOutput) run(cleanup func()) {
	defer close(o.done)
	defer o.sink.Close()
	if cleanup != nil {
		defer cleanup()
	}
	if _, err := io.WriteString(o.sink, enterTerminalScreen); err != nil {
		if o.ctx.Err() == nil {
			o.notify(outputCommit{err: err})
		}
		return
	}
	screen := NewScreen(o.sink)
	var generation uint64
	for {
		select {
		case <-o.ctx.Done():
			return
		case <-o.wake:
			if o.ctx.Err() != nil {
				return
			}
			o.mu.Lock()
			request := o.pending
			o.pending = nil
			o.mu.Unlock()
			if request == nil {
				continue
			}
			if request.generation != generation {
				screen.Invalidate()
				generation = request.generation
			}
			if err := screen.Draw(request.frame, request.width, request.height); err != nil {
				if o.ctx.Err() == nil {
					o.notify(outputCommit{err: err})
				}
				return
			}
			o.notify(outputCommit{frame: request.frame})
		}
	}
}

// Restore on a fresh owned descriptor after the frame write stopped. An
// unresponsive terminal cannot hold shutdown indefinitely for ANSI cleanup.
func restoreTerminalOutput() {
	sink, err := openTerminalOutput()
	if err != nil {
		return
	}
	done := make(chan struct{})
	go func() { defer close(done); defer sink.Close(); _, _ = io.WriteString(sink, leaveTerminalScreen) }()
	timer := time.NewTimer(40 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}
	sink.Interrupt()
	_ = sink.Close()
}
