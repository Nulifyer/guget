//go:build !windows

package ui

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type unixIOResult struct {
	count int
	err   error
}

func unixPipeAdapter(t *testing.T, writing bool) (*unixTerminalIO, int) {
	t.Helper()
	var descriptors [2]int
	if err := unix.Pipe(descriptors[:]); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		unix.CloseOnExec(descriptor)
		if err := unix.SetNonblock(descriptor, true); err != nil {
			t.Fatal(err)
		}
	}
	owned, peer := descriptors[0], descriptors[1]
	if writing {
		owned, peer = peer, owned
	}
	adapter, err := newUnixTerminalIO(owned, "test pipe")
	if err != nil {
		_ = unix.Close(owned)
		_ = unix.Close(peer)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close(); _ = unix.Close(peer) })
	return adapter, peer
}

func fillUnixOutput(t *testing.T, descriptor int) int {
	t.Helper()
	data := bytes.Repeat([]byte("X"), 4096)
	filled := 0
	for {
		count, err := unix.Write(descriptor, data)
		if count > 0 {
			filled += count
		}
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			if len(data) == 1 {
				return filled
			}
			data = data[:1]
			continue
		}
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatal("pipe fill made no progress")
		}
	}
}

func TestUnixTerminalReadWaitsForReadinessInsteadOfReturningEAGAIN(t *testing.T) {
	adapter, peer := unixPipeAdapter(t, false)
	result := make(chan unixIOResult, 1)
	data := make([]byte, 16)
	go func() { count, err := adapter.Read(data); result <- unixIOResult{count, err} }()
	select {
	case got := <-result:
		t.Fatalf("empty nonblocking input returned early: %+v", got)
	case <-time.After(15 * time.Millisecond):
	}
	if _, err := unix.Write(peer, []byte("input")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.err != nil || string(data[:got.count]) != "input" {
			t.Fatalf("read=%+v data=%q", got, data[:got.count])
		}
	case <-time.After(time.Second):
		t.Fatal("read did not resume when input became ready")
	}
}

func TestUnixTerminalWriteWaitsForBackpressureAndCompletes(t *testing.T) {
	adapter, peer := unixPipeAdapter(t, true)
	filled := fillUnixOutput(t, adapter.fd)
	result := make(chan unixIOResult, 1)
	go func() { count, err := adapter.Write([]byte("payload")); result <- unixIOResult{count, err} }()
	select {
	case got := <-result:
		t.Fatalf("full nonblocking output returned early: %+v", got)
	case <-time.After(15 * time.Millisecond):
	}
	data := make([]byte, 65536)
	for remaining := filled; remaining > 0; {
		count, err := unix.Read(peer, data[:min(len(data), remaining)])
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatal("pipe unexpectedly reached EOF while draining")
		}
		remaining -= count
	}
	select {
	case got := <-result:
		if got.err != nil || got.count != len("payload") {
			t.Fatalf("write=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("write did not resume after output drained")
	}
	count, err := unix.Read(peer, data)
	if err != nil || !bytes.HasSuffix(data[:max(0, count)], []byte("payload")) {
		t.Fatalf("payload lost after partial writes: count=%d err=%v", count, err)
	}
}

func TestUnixTerminalCloseWakesAndJoinsPendingIO(t *testing.T) {
	for _, writing := range []bool{false, true} {
		name := "read"
		if writing {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			adapter, _ := unixPipeAdapter(t, writing)
			if writing {
				fillUnixOutput(t, adapter.fd)
			}
			result := make(chan unixIOResult, 1)
			go func() {
				var count int
				var err error
				if writing {
					count, err = adapter.Write([]byte("blocked"))
				} else {
					count, err = adapter.Read(make([]byte, 1))
				}
				result <- unixIOResult{count, err}
			}()
			select {
			case got := <-result:
				t.Fatalf("I/O was not pending before cancellation: %+v", got)
			case <-time.After(15 * time.Millisecond):
			}
			var closes sync.WaitGroup
			for range 8 {
				closes.Add(1)
				go func() {
					defer closes.Done()
					if err := adapter.Close(); err != nil {
						t.Errorf("close: %v", err)
					}
				}()
			}
			done := make(chan struct{})
			go func() { closes.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("close did not wake and join pending I/O")
			}
			select {
			case got := <-result:
				if !errors.Is(got.err, os.ErrClosed) {
					t.Fatalf("canceled operation=%+v", got)
				}
			case <-time.After(time.Second):
				t.Fatal("I/O operation survived Close")
			}
			if count, err := adapter.Read(make([]byte, 1)); count != 0 || !errors.Is(err, os.ErrClosed) {
				t.Fatalf("read after close=%d,%v", count, err)
			}
			if count, err := adapter.Write([]byte("late")); count != 0 || !errors.Is(err, os.ErrClosed) {
				t.Fatalf("write after close=%d,%v", count, err)
			}
			// Repeated cancellation must not touch a newly opened/reused descriptor.
			descriptor, err := unix.Open(os.DevNull, unix.O_RDONLY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(descriptor)
			adapter.Interrupt()
			_ = adapter.Close()
			if _, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0); err != nil {
				t.Fatalf("repeated close affected another descriptor: %v", err)
			}
		})
	}
}

func TestUnixTerminalOpenPreservesCallerDescriptorFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared-file")
	caller, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Close()
	before, err := unix.FcntlInt(caller.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := openUnixTerminalIO(path, unix.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Write([]byte("output")); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := unix.FcntlInt(caller.Fd(), unix.F_GETFL, 0)
	if err != nil || before != after {
		t.Fatalf("caller flags changed: before=%d after=%d err=%v", before, after, err)
	}
	if _, err := caller.WriteString("still open"); err != nil {
		t.Fatalf("caller descriptor was closed: %v", err)
	}
}

func TestUnixTerminalInterruptStopsReadWithoutClosingItsDescriptor(t *testing.T) {
	adapter, _ := unixPipeAdapter(t, false)
	result := make(chan error, 1)
	go func() { _, err := adapter.Read(make([]byte, 1)); result <- err }()
	select {
	case err := <-result:
		t.Fatalf("read returned before interrupt: %v", err)
	case <-time.After(15 * time.Millisecond):
	}
	adapter.Interrupt()
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("interrupt error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupt did not wake readiness wait")
	}
	if _, err := unix.FcntlInt(uintptr(adapter.fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("interrupt closed descriptor before Close's I/O barrier: %v", err)
	}
}

func TestUnixTerminalReadinessDiagnosticKeepsValidFDAndFailureCause(t *testing.T) {
	adapter, _ := unixPipeAdapter(t, false)
	err := adapter.readinessError("poll", unix.POLLIN, unix.POLLNVAL, 0, unix.EBADF)
	if !errors.Is(err, unix.EBADF) || errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), "valid=true") || !strings.Contains(err.Error(), "requested=0x") || !strings.Contains(err.Error(), "data=0x") {
		t.Fatalf("readiness failure lost cause or fd state: %v", err)
	}
}
