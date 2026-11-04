//go:build !windows

package ui

import (
	"io"
	"os"
	"syscall"
)

// Own a pollable terminal descriptor so Close interrupts pending decoder reads.
// Closing os.Stdin would affect the caller after Runtime.Run returns.
func openInput() (io.Reader, func(), error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}
