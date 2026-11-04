//go:build !windows

package term

import (
	"io"
	"os"
)

// StdinReader returns the byte source for key input. On non-Windows
// platforms os.Stdin works directly; Windows has a separate
// implementation that bypasses Go's internal/poll console wrapper
// (which translates ^Z to EOF and would otherwise close the app on
// Ctrl+Z).
func StdinReader() io.Reader { return os.Stdin }
