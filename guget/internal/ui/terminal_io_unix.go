//go:build !windows

package ui

import (
	"errors"
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// unixTerminalIO owns a nonblocking descriptor and a private cancellation pipe.
// Darwin does not make every nonblocking TTY an os.File poll descriptor, so
// readiness and cancellation are handled explicitly instead of relying on it.
// The active-operation barrier prevents Close from reusing a descriptor while
// a read, write, or poll still references its numeric value.
type unixTerminalIO struct {
	fd        int
	name      string
	wake      [2]int
	mu        sync.Mutex
	stopped   bool
	active    sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

func openUnixTerminalIO(path string, flags int) (*unixTerminalIO, error) {
	fd, err := unix.Open(path, flags|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	terminal, err := newUnixTerminalIO(fd, path)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return terminal, nil
}

// fd must be owned by the caller and already nonblocking. On success ownership
// transfers to the adapter; on failure the caller remains responsible for it.
func newUnixTerminalIO(fd int, name string) (*unixTerminalIO, error) {
	terminal := &unixTerminalIO{fd: fd, name: name}
	if err := unix.Pipe(terminal.wake[:]); err != nil {
		return nil, os.NewSyscallError("pipe", err)
	}
	for _, pipeFD := range terminal.wake {
		unix.CloseOnExec(pipeFD)
		if err := unix.SetNonblock(pipeFD, true); err != nil {
			_ = unix.Close(terminal.wake[0])
			_ = unix.Close(terminal.wake[1])
			return nil, os.NewSyscallError("nonblocking cancellation pipe", err)
		}
	}
	return terminal, nil
}

func (t *unixTerminalIO) begin() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return false
	}
	t.active.Add(1)
	return true
}
func (t *unixTerminalIO) isStopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}
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
		if err != nil {
			return os.NewSyscallError("poll", err)
		}
		if descriptors[1].Revents != 0 || t.isStopped() {
			return os.ErrClosed
		}
		if descriptors[0].Revents&unix.POLLNVAL != 0 {
			return os.ErrClosed
		}
		if descriptors[0].Revents&(event|unix.POLLHUP|unix.POLLERR) != 0 {
			return nil
		}
	}
}

func (t *unixTerminalIO) Read(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if !t.begin() {
		return 0, os.ErrClosed
	}
	defer t.active.Done()
	for {
		if t.isStopped() {
			return 0, os.ErrClosed
		}
		count, err := unix.Read(t.fd, data)
		if err == nil {
			if count == 0 {
				return 0, io.EOF
			}
			return count, nil
		}
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			if err := t.waitReady(unix.POLLIN); err != nil {
				return 0, err
			}
			continue
		}
		return 0, &os.PathError{Op: "read", Path: t.name, Err: err}
	}
}
func (t *unixTerminalIO) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if !t.begin() {
		return 0, os.ErrClosed
	}
	defer t.active.Done()
	written := 0
	for written < len(data) {
		if t.isStopped() {
			return written, os.ErrClosed
		}
		count, err := unix.Write(t.fd, data[written:])
		if count > 0 {
			written += count
		}
		if err == nil {
			if count == 0 {
				return written, io.ErrShortWrite
			}
			continue
		}
		if err == unix.EINTR {
			continue
		}
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			if err := t.waitReady(unix.POLLOUT); err != nil {
				return written, err
			}
			continue
		}
		return written, &os.PathError{Op: "write", Path: t.name, Err: err}
	}
	return written, nil
}
func (t *unixTerminalIO) Interrupt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.stopped = true
	for {
		_, err := unix.Write(t.wake[1], []byte{1})
		if err != unix.EINTR {
			break
		}
	}
}
func (t *unixTerminalIO) Close() error {
	t.closeOnce.Do(func() {
		t.Interrupt()
		t.active.Wait()
		t.closeErr = errors.Join(unix.Close(t.fd), unix.Close(t.wake[0]), unix.Close(t.wake[1]))
	})
	return t.closeErr
}
