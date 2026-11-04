package ui

import (
	"testing"

	"golang.org/x/sys/unix"
)

func readNativeTerminalMode(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}

func nativeTerminalModeRestored(before, after *unix.Termios) bool {
	return *before == *after
}

func TestNativeTerminalModeComparisonKeepsLinuxPENDINStrict(t *testing.T) {
	before := unix.Termios{}
	after := before
	after.Lflag = unix.PENDIN
	if nativeTerminalModeRestored(&before, &after) {
		t.Fatal("Linux restoration must compare every terminal flag")
	}
}
