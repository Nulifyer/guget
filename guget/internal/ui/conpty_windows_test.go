//go:build windows

package ui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Run the actual test executable in a native pseudoconsole: ordinary Actions
// stdin/stdout pipes do not exercise ReadConsoleW or console mode restoration.
type nativeConsole struct {
	console       windows.Handle
	process       windows.Handle
	input         *os.File
	output        *os.File
	mu            sync.Mutex
	transcript    bytes.Buffer
	changed       chan struct{}
	pause         chan chan struct{}
	resume        chan struct{}
	resumeOnce    sync.Once
	drainDelay    atomic.Int64
	statusDir     string
	consoleClosed bool
}

func startNativeConsole(t *testing.T, scenario string) *nativeConsole {
	t.Helper()
	if err := windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole").Find(); err != nil {
		if errors.Is(err, windows.ERROR_PROC_NOT_FOUND) {
			if os.Getenv("CI") != "" {
				t.Fatalf("native ConPTY is required on CI: %v", err)
			}
			t.Skipf("native ConPTY unavailable: CreatePseudoConsole is not exported: %v", err)
		}
		t.Fatalf("detect native ConPTY capability: %v", err)
	}
	inRead, inWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inRead.Close(); _ = inWrite.Close() })
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = outRead.Close(); _ = outWrite.Close() })
	var console windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: 120, Y: 30}, windows.Handle(inRead.Fd()), windows.Handle(outWrite.Fd()), 0, &console); err != nil {
		t.Fatalf("create native ConPTY: %v", err)
	}
	p := &nativeConsole{console: console, input: inWrite, output: outRead, changed: make(chan struct{}, 1), pause: make(chan chan struct{}, 1), resume: make(chan struct{}), statusDir: t.TempDir()}
	go func() {
		var buffer [4096]byte
		for {
			select {
			case paused := <-p.pause:
				close(paused)
				<-p.resume
			default:
			}
			n, err := outRead.Read(buffer[:])
			if n > 0 {
				p.mu.Lock()
				_, _ = p.transcript.Write(buffer[:n])
				p.mu.Unlock()
				select {
				case p.changed <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
			if delay := time.Duration(p.drainDelay.Load()); delay > 0 {
				time.Sleep(delay)
			}
		}
	}()
	t.Cleanup(func() {
		p.drainDelay.Store(0)
		p.resumeDraining()
		if p.process != 0 {
			_ = windows.TerminateProcess(p.process, 1)
		}
		// Keep draining output until ClosePseudoConsole returns; otherwise its
		// final output can fill a pipe and deadlock teardown.
		if !p.consoleClosed {
			windows.ClosePseudoConsole(p.console)
		}
		if p.process != 0 {
			_ = windows.CloseHandle(p.process)
		}
	})
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer attributes.Delete()
	// This attribute takes the opaque HPCON value itself as lpValue, rather
	// than a pointer to a Go variable containing the handle.
	update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
	ok, _, callErr := update.Call(uintptr(unsafe.Pointer(attributes.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(console), unsafe.Sizeof(console), 0, 0)
	if ok == 0 {
		t.Fatalf("attach native ConPTY: %v", callErr)
	}
	info := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	info.Cb = uint32(unsafe.Sizeof(info))
	command, err := windows.UTF16PtrFromString(syscall.EscapeArg(os.Args[0]) + " -test.run=^TestNativeConPTYTerminal$ -test.v")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GUGET_CONPTY_CHILD", scenario)
	t.Setenv("GUGET_CONPTY_STATUS_DIR", p.statusDir)
	var process windows.ProcessInformation
	if err := windows.CreateProcess(nil, command, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &info.StartupInfo, &process); err != nil {
		t.Fatalf("start native ConPTY helper: %v", err)
	}
	p.process = process.Process
	_ = windows.CloseHandle(process.Thread)
	_ = inRead.Close()
	_ = outWrite.Close()
	return p
}

func (p *nativeConsole) resumeDraining() { p.resumeOnce.Do(func() { close(p.resume) }) }

func (p *nativeConsole) request(t *testing.T, command string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(p.statusDir, "request-"+command), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (p *nativeConsole) expectStatus(t *testing.T, name string) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if data, err := os.ReadFile(filepath.Join(p.statusDir, name)); err == nil && string(data) == name {
			return
		}
		if failure, err := os.ReadFile(filepath.Join(p.statusDir, "failure")); err == nil {
			t.Fatalf("native backpressure helper failed: %s", failure)
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			stacks, _ := os.ReadFile(filepath.Join(p.statusDir, "stacks"))
			t.Fatalf("runtime did not report %s with terminal output paused:\n%s\nNative helper stacks:\n%s", name, p.text(), stacks)
		}
	}
}

func (p *nativeConsole) text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.transcript.String()
}

func (p *nativeConsole) expect(t *testing.T, marker string) {
	t.Helper()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		if strings.Contains(p.text(), marker) {
			return
		}
		select {
		case <-p.changed:
		case <-timer.C:
			t.Fatalf("ConPTY did not report %s:\n%s", marker, p.text())
		}
	}
}

func (p *nativeConsole) send(t *testing.T, input string) {
	t.Helper()
	if _, err := io.WriteString(p.input, input); err != nil {
		t.Fatal(err)
	}
}

func (p *nativeConsole) wait(t *testing.T, requireSuccess bool) {
	t.Helper()
	state, err := windows.WaitForSingleObject(p.process, 8000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatalf("ConPTY helper did not exit: state=%d error=%v\n%s", state, err, p.text())
	}
	var status uint32
	if err := windows.GetExitCodeProcess(p.process, &status); err != nil {
		t.Fatal(err)
	}
	if requireSuccess && status != 0 {
		t.Fatalf("ConPTY helper exited %d:\n%s", status, p.text())
	}
	if !requireSuccess {
		t.Logf("console-disconnected helper exited %d; mode restoration is asserted separately while console handles remain valid", status)
	}
}

func (p *nativeConsole) resize(t *testing.T, width, height int16) {
	t.Helper()
	if err := windows.ResizePseudoConsole(p.console, windows.Coord{X: width, Y: height}); err != nil {
		t.Fatal(fmt.Errorf("resize ConPTY: %w", err))
	}
}
