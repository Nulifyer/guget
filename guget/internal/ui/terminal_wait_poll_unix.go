//go:build !windows && !darwin

package ui

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func (t *unixTerminalIO) waitReady(event int16) error {
	descriptors := []unix.PollFd{{Fd: int32(t.fd), Events: event}, {Fd: int32(t.wake[0]), Events: unix.POLLIN}}
	for {
		if t.isStopped() {
			return os.ErrClosed
		}
		_, err := unix.Poll(descriptors, -1)
		if err == unix.EINTR {
			continue
		}
		if t.isStopped() {
			return os.ErrClosed
		}
		if err != nil {
			return t.readinessError("poll", event, descriptors[0].Revents, descriptors[1].Revents, err)
		}
		if descriptors[1].Revents != 0 {
			return t.readinessError("poll wakeup", event, descriptors[0].Revents, descriptors[1].Revents, fmt.Errorf("unexpected cancellation-pipe readiness"))
		}
		if descriptors[0].Revents&unix.POLLNVAL != 0 {
			return t.readinessError("poll", event, descriptors[0].Revents, descriptors[1].Revents, unix.EBADF)
		}
		if descriptors[0].Revents&(event|unix.POLLHUP|unix.POLLERR) != 0 {
			return nil
		}
	}
}
