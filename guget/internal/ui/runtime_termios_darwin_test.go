package ui

import (
	"testing"

	"golang.org/x/sys/unix"
)

func readNativeTerminalMode(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}

func nativeTerminalModeRestored(before, after *unix.Termios) bool {
	expected, actual := *before, *after
	// Darwin's TIOCSETA sets PENDIN when restoring ICANON, then preserves it
	// alongside the requested local flags. This tracks pending input processing,
	// not a failure to restore the caller's input policy. Compare every other bit
	// and every other termios field exactly.
	// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/tty.c
	expected.Lflag &^= unix.PENDIN
	actual.Lflag &^= unix.PENDIN
	return expected == actual
}

func TestNativeTerminalModeComparisonAllowsDarwinKernelPENDIN(t *testing.T) {
	before := unix.Termios{Lflag: unix.ECHO | unix.ICANON | unix.ISIG}
	after := before
	after.Lflag |= unix.PENDIN
	if !nativeTerminalModeRestored(&before, &after) {
		t.Fatal("Darwin's kernel-managed PENDIN is permitted after canonical restoration")
	}
	if before.Lflag&unix.PENDIN != 0 || after.Lflag&unix.PENDIN == 0 {
		t.Fatal("comparison changed the original terminal snapshots")
	}
}
