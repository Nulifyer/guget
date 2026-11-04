//go:build !windows

package ui

import (
	"io"

	"golang.org/x/sys/unix"
)

// Own a separate terminal description; cancellation never closes stdin or
// changes the caller's descriptor flags.
func openInput() (io.Reader, func(), error) {
	input, err := openUnixTerminalIO("/dev/tty", unix.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	return input, func() { _ = input.Close() }, nil
}
