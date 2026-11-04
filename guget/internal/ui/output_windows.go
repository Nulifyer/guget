//go:build windows

package ui

import (
	"io"
	"os"
	"runtime"
	"sync"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

var cancelSynchronousOutput = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

type terminalOutput struct {
	mu              sync.Mutex
	handle, thread  windows.Handle
	stopped, closed bool
}

func openTerminalOutput() (outputSink, error) {
	name, err := windows.UTF16PtrFromString("CONOUT$")
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	return &terminalOutput{handle: handle}, nil
}
func (t *terminalOutput) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(thread)
	t.mu.Lock()
	if t.stopped || t.closed {
		t.mu.Unlock()
		return 0, os.ErrClosed
	}
	t.thread = thread
	handle := t.handle
	t.mu.Unlock()
	defer func() { t.mu.Lock(); t.thread = 0; t.mu.Unlock() }()
	units := utf16.Encode([]rune(string(data)))
	for len(units) > 0 {
		t.mu.Lock()
		stopped := t.stopped || t.closed
		t.mu.Unlock()
		if stopped {
			return 0, os.ErrClosed
		}
		var written uint32
		count := min(len(units), 16384)
		if count < len(units) && units[count-1] >= 0xd800 && units[count-1] <= 0xdbff {
			count-- // Keep a surrogate pair in the same native write.
		}
		if err := windows.WriteConsole(handle, &units[0], uint32(count), &written, nil); err != nil {
			return 0, err
		}
		if written == 0 {
			return 0, io.ErrShortWrite
		}
		units = units[written:]
	}
	return len(data), nil
}
func (t *terminalOutput) Interrupt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
	if t.thread != 0 {
		_, _, _ = cancelSynchronousOutput.Call(uintptr(t.thread))
	}
	if !t.closed {
		_ = windows.CancelIoEx(t.handle, nil)
	}
}
func (t *terminalOutput) Close() error {
	t.Interrupt()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return windows.CloseHandle(t.handle)
}
