//go:build !windows

package ui

import "golang.org/x/sys/unix"

func openTerminalOutput() (outputSink, error) {
	// Open the controlling terminal directly. Darwin /dev/stdout can duplicate
	// the caller's open-file description instead of creating an independent
	// nonblocking description. Input already uses this same controlling TTY.
	return openUnixTerminalIO("/dev/tty", unix.O_WRONLY)
}
