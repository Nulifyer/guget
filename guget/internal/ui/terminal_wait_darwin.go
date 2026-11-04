//go:build darwin

package ui

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Darwin's controlling-TTY driver implements cttyselect; poll/kqueue do not
// support every /dev/tty descriptor. Use select for both the owned terminal and
// its private cancellation pipe, keeping stdin/stdout descriptions untouched.
func (t *unixTerminalIO) waitReady(event int16) error {
	var capacity unix.FdSet
	limit := len(capacity.Bits) * unix.NFDBITS
	if t.fd < 0 || t.wake[0] < 0 || t.fd >= limit || t.wake[0] >= limit {
		return t.readinessError("select", event, 0, 0, fmt.Errorf("descriptor exceeds select capacity %d: %w", limit, unix.EINVAL))
	}
	for {
		if t.isStopped() {
			return os.ErrClosed
		}
		// select replaces sets with the ready subset, so rebuild them for every
		// attempt, including after a signal interrupted the system call.
		var reads, writes unix.FdSet
		reads.Set(t.wake[0])
		if event&unix.POLLIN != 0 {
			reads.Set(t.fd)
		}
		if event&unix.POLLOUT != 0 {
			writes.Set(t.fd)
		}
		_, err := unix.Select(max(t.fd, t.wake[0])+1, &reads, &writes, nil, nil)
		if err == unix.EINTR {
			continue
		}
		if t.isStopped() {
			return os.ErrClosed
		}
		if err != nil {
			return t.readinessError("select", event, 0, 0, err)
		}
		if reads.IsSet(t.wake[0]) {
			return t.readinessError("select wakeup", event, 0, unix.POLLIN, fmt.Errorf("unexpected cancellation-pipe readiness"))
		}
		if event&unix.POLLIN != 0 && reads.IsSet(t.fd) || event&unix.POLLOUT != 0 && writes.IsSet(t.fd) {
			return nil
		}
	}
}
