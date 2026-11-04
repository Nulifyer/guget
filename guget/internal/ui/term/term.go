package term

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// Terminal wraps the controlling terminal in raw mode and exposes its size.
// Restore() must be called before exit (defer it).
//
// On Windows the console has separate input and output handles and each one
// supports a different subset of APIs: MakeRaw wants the input handle (stdin),
// while GetConsoleScreenBufferInfo (used by term.GetSize) wants the output
// handle (stdout). Passing the wrong one yields "The handle is invalid." So
// we store both fds and use the right one per call.
type Terminal struct {
	InFd          int
	OutFd         int
	oldState      *term.State
	restoreOutput func()
	Width         int
	Height        int
}

func OpenTerminal() (*Terminal, error) {
	inFd := int(os.Stdin.Fd())
	outFd := int(os.Stdout.Fd())
	if !term.IsTerminal(inFd) {
		return nil, fmt.Errorf("stdin is not a terminal")
	}
	if !term.IsTerminal(outFd) {
		return nil, fmt.Errorf("stdout is not a terminal")
	}
	st, err := term.MakeRaw(inFd)
	if err != nil {
		return nil, fmt.Errorf("make raw: %w", err)
	}
	restoreOutput, err := enableOutput(outFd)
	if err != nil {
		_ = term.Restore(inFd, st)
		return nil, fmt.Errorf("enable terminal output: %w", err)
	}
	w, h, err := term.GetSize(outFd)
	if err != nil {
		restoreOutput()
		_ = term.Restore(inFd, st)
		return nil, fmt.Errorf("get size: %w", err)
	}
	return &Terminal{InFd: inFd, OutFd: outFd, oldState: st, restoreOutput: restoreOutput, Width: w, Height: h}, nil
}

func (t *Terminal) Restore() error {
	if t == nil || t.oldState == nil {
		return nil
	}
	t.restoreOutput()
	if err := term.Restore(t.InFd, t.oldState); err != nil {
		return fmt.Errorf("restore terminal input: %w", err)
	}
	t.oldState = nil
	return nil
}

// Size reads native dimensions without changing terminal state. Console APIs
// can wait behind output, so callers query from a background worker.
func (t *Terminal) Size() (int, int, error) { return term.GetSize(t.OutFd) }
