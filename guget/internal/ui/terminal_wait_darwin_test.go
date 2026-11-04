//go:build darwin

package ui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinTerminalReadinessRejectsDescriptorOutsideSelectCapacity(t *testing.T) {
	var descriptors unix.FdSet
	limit := len(descriptors.Bits) * unix.NFDBITS
	terminal := &unixTerminalIO{fd: limit, name: "test terminal", wake: [2]int{0, 1}}
	err := terminal.waitReady(unix.POLLIN)
	if err == nil || !errors.Is(err, unix.EINVAL) || errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), "select capacity") || !strings.Contains(err.Error(), "requested=") {
		t.Fatalf("unsupported descriptor must return diagnostic instead of panic/false close: %v", err)
	}
}
