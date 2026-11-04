package term

import (
	"testing"

	xterm "golang.org/x/term"
)

func TestRestoreReportsFailureAndPreservesSavedState(t *testing.T) {
	saved := &xterm.State{}
	terminal := &Terminal{InFd: -1, oldState: saved, restoreOutput: func() {}}
	if err := terminal.Restore(); err == nil {
		t.Fatal("restoring an invalid descriptor must report an error")
	}
	if terminal.oldState != saved {
		t.Fatal("failed restoration discarded the saved state needed to retry")
	}
}

func TestRestoreWithoutSavedStateIsSafe(t *testing.T) {
	for _, terminal := range []*Terminal{nil, {}} {
		if err := terminal.Restore(); err != nil {
			t.Fatal(err)
		}
	}
}
