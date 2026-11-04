//go:build windows

package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nulifyer/guget/internal/ui/term"
	"golang.org/x/sys/windows"
)

func TestNativeConPTYTerminal(t *testing.T) {
	if scenario := os.Getenv("GUGET_CONPTY_CHILD"); scenario != "" {
		bindNativeConsoleHandles(t)
		runNativeConsoleChild(t, scenario)
		return
	}
	for _, scenario := range []string{"reader-cancel", "runtime-cancel", "runtime-escape-cancel", "runtime-ctrl-c", "runtime-error", "runtime-input", "output-backpressure", "output-backpressure-cancel", "disconnect"} {
		t.Run(scenario, func(t *testing.T) {
			console := startNativeConsole(t, scenario)
			switch scenario {
			case "output-backpressure", "output-backpressure-cancel":
				console.expect(t, "INPUT-READY")
				console.resize(t, 300, 120)
				console.expect(t, "HIGH-OUTPUT-READY")
				paused := make(chan struct{})
				console.pause <- paused
				console.send(t, "f") // Changing large frames wake the output drainer.
				select {
				case <-paused:
				case <-time.After(8 * time.Second):
					t.Fatal("could not pause ConPTY output")
				}
				console.expectStatus(t, "OUTPUT-BLOCKED")
				// ConPTY hosts must keep draining both channels. Windows console
				// input, size and mode calls can share the host's output lock;
				// an undrained host can therefore delay native key delivery too.
				// Prove the application loop itself still advances using IPC.
				// https://learn.microsoft.com/windows/console/creating-a-pseudoconsole-session
				// https://github.com/microsoft/terminal/blob/main/src/host/_stream.cpp
				console.request(t, "ping")
				console.expectStatus(t, "APP-EVENT-HANDLED")
				if scenario == "output-backpressure-cancel" {
					console.request(t, "cancel")
					console.expectStatus(t, "CANCEL-REQUESTED")
				} else {
					console.send(t, "k") // Queued while output is still paused.
					// Resume a deliberately slow consumer, so native keyboard
					// delivery is checked while large changing frames continue.
					console.drainDelay.Store(int64(5 * time.Millisecond))
				}
				console.resumeDraining()
				if scenario == "output-backpressure" {
					console.expectStatus(t, "KEY-HANDLED")
					console.drainDelay.Store(0)
					console.send(t, "q")
				}
				console.expectStatus(t, "RUN-RETURNED")
			case "runtime-ctrl-c":
				console.expect(t, "INPUT-READY")
				console.send(t, "\x03")
				console.expect(t, "CTRL-C-KEY")
			case "runtime-error":
				console.expect(t, "INPUT-READY")
				console.send(t, "e")
			case "runtime-escape-cancel":
				console.expect(t, "INPUT-READY")
				console.send(t, "\x1b")
			case "runtime-input":
				console.expect(t, "INPUT-READY")
				for _, input := range []struct{ data, marker string }{
					{"\x1a", "CTRL-Z-OK"},
					{"\x1b[A", "ARROW-OK"},
					{"😀", "UNICODE-OK"},
					{"\x1b[200~日本語😀\npaste\x1b[201~", "PASTE-OK"},
					{"\x1b[<0;10;5M", "MOUSE-OK"},
				} {
					console.send(t, input.data)
					console.expect(t, input.marker)
				}
				console.resize(t, 80, 20)
				console.expect(t, "RESIZE-OK")
				console.send(t, "q")
			case "disconnect":
				console.expect(t, "INPUT-READY")
				console.consoleClosed = true
				closed := make(chan struct{})
				go func() { windows.ClosePseudoConsole(console.console); close(closed) }()
				console.wait(t, false)
				select {
				case <-closed:
				case <-time.After(8 * time.Second):
					t.Fatal("ClosePseudoConsole blocked after child exit")
				}
				return
			}
			console.expect(t, "NATIVE-SCENARIO-PASSED")
			console.wait(t, true)
		})
	}
}

func bindNativeConsoleHandles(t *testing.T) {
	t.Helper()
	// A runner launches this helper from redirected pipes. Attaching ConPTY
	// does not replace those inherited standard handles; open the child's
	// attached console explicitly before testing the production runtime.
	for _, stream := range []struct {
		name string
		file **os.File
		id   uint32
	}{
		{"CONIN$", &os.Stdin, windows.STD_INPUT_HANDLE},
		{"CONOUT$", &os.Stdout, windows.STD_OUTPUT_HANDLE},
		{"CONOUT$", &os.Stderr, windows.STD_ERROR_HANDLE},
	} {
		name, err := windows.UTF16PtrFromString(stream.name)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatalf("open attached %s: %v", stream.name, err)
		}
		file := os.NewFile(uintptr(handle), stream.name)
		*stream.file = file
		if err := windows.SetStdHandle(stream.id, handle); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
	}
}

type nativeModes struct{ input, output uint32 }

func readNativeModes(t *testing.T) nativeModes {
	t.Helper()
	var modes nativeModes
	if err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &modes.input); err != nil {
		t.Fatal(err)
	}
	if err := windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &modes.output); err != nil {
		t.Fatal(err)
	}
	return modes
}

func expectNativeModes(t *testing.T, want nativeModes) {
	t.Helper()
	if actual := readNativeModes(t); actual != want {
		t.Fatalf("console modes were not preserved/restored: got %#v, want %#v", actual, want)
	}
}

// Establish that ReadConsoleW is actually pending, rather than letting Close
// race before a reader goroutine has ever entered the native console API.
func waitForNativeRead() error {
	return waitForNativeConsoleCall("windows.ReadConsole(", 0)
}

func waitForNativeConsoleCall(call string, sustained time.Duration) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	buffer := make([]byte, 128*1024)
	var pendingSince time.Time
	for {
		n := runtime.Stack(buffer, true)
		if strings.Contains(string(buffer[:n]), call) {
			if pendingSince.IsZero() {
				pendingSince = time.Now()
			}
			if time.Since(pendingSince) >= sustained {
				return nil
			}
		} else {
			pendingSince = time.Time{}
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return fmt.Errorf("native call %s did not remain pending:\n%s", call, buffer[:n])
		}
	}
}

func runNativeConsoleChild(t *testing.T, scenario string) {
	before := readNativeModes(t)
	if scenario == "reader-cancel" {
		terminal, err := term.OpenTerminal()
		if err != nil {
			t.Fatal(err)
		}
		defer terminal.Restore()
		raw := readNativeModes(t)
		if raw.input&(windows.ENABLE_ECHO_INPUT|windows.ENABLE_LINE_INPUT|windows.ENABLE_PROCESSED_INPUT) != 0 || raw.input&windows.ENABLE_VIRTUAL_TERMINAL_INPUT == 0 || raw.output&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 {
			t.Fatalf("raw/VT modes not enabled: %#v", raw)
		}
		reader, closeInput, err := openInput()
		if err != nil {
			t.Fatal(err)
		}
		defer closeInput()
		readDone := make(chan error, 1)
		go func() { var b [8]byte; _, err := reader.Read(b[:]); readDone <- err }()
		if err := waitForNativeRead(); err != nil {
			t.Fatal(err)
		}
		closed := make(chan struct{})
		go func() { closeInput(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(3 * time.Second):
			t.Fatal("owned reader Close did not cancel pending native input")
		}
		select {
		case err := <-readDone:
			if err != io.EOF {
				t.Fatalf("closed reader returned %v, want EOF", err)
			}
		case <-time.After(time.Second):
			t.Fatal("native Read remained alive after Close returned")
		}
		expectNativeModes(t, raw) // Closing the duplicate preserves original handles.
		closeInput()              // Closing an already closed reader is harmless.
		var b [1]byte
		if _, err := reader.Read(b[:]); err != io.EOF {
			t.Fatalf("read after close = %v", err)
		}
		terminal.Restore()
		expectNativeModes(t, before)
		fmt.Println("NATIVE-SCENARIO-PASSED")
		return
	}
	if scenario == "runtime-cancel" {
		// Reuse the same console after Run returns so process exit cannot hide a
		// reader that survived shutdown or a stale console mode.
		for range 3 {
			ctx, cancel := context.WithCancel(context.Background())
			readCheck := make(chan error, 1)
			component := &nativeProbe{connect: func() Event {
				defer cancel()
				readCheck <- waitForNativeRead()
				return nil
			}}
			if err := NewRuntime(ctx, component).Run(); err != nil {
				t.Fatal(err)
			}
			cancel()
			if err := <-readCheck; err != nil {
				t.Fatal(err)
			}
			if component.disconnected.Load() != 1 {
				t.Fatal("runtime did not disconnect exactly once")
			}
			expectNativeModes(t, before)
		}
		fmt.Println("NATIVE-SCENARIO-PASSED")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	component := &nativeProbe{interactive: true}
	if strings.HasPrefix(scenario, "output-backpressure") {
		component.statusDir = os.Getenv("GUGET_CONPTY_STATUS_DIR")
	}
	r := NewRuntime(ctx, component)
	if scenario == "output-backpressure-cancel" {
		// Acknowledge the runtime's cancellation channel independently of the
		// thread requesting it and any ensuing native console cleanup.
		go func() {
			<-r.Context().Done()
			if err := writeNativeStatus(component.statusDir, "CANCEL-REQUESTED"); err != nil {
				_ = os.WriteFile(filepath.Join(component.statusDir, "failure"), []byte(err.Error()), 0o600)
			}
		}()
	}
	if component.statusDir != "" {
		controlDone := make(chan struct{})
		stackDone := make(chan struct{})
		defer func() { cancel(); <-controlDone; <-stackDone }()
		// Stack collection is independent of command delivery and only runs
		// after a failure. A diagnostic must never delay the cancellation it
		// is meant to diagnose, or perturb successful race-instrumented runs.
		go func() {
			defer close(stackDone)
			tick := time.NewTicker(25 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if _, err := os.Stat(filepath.Join(component.statusDir, "request-stacks")); err == nil {
						buffer := make([]byte, 256*1024)
						n := runtime.Stack(buffer, true)
						_ = os.WriteFile(filepath.Join(component.statusDir, "stacks"), buffer[:n], 0o600)
						return
					}
				}
			}
		}()
		go func() {
			defer close(controlDone)
			tick := time.NewTicker(25 * time.Millisecond)
			defer tick.Stop()
			var pingSeen, cancelSeen bool
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					// Persistent one-shot requests avoid requiring Windows file
					// deletion for IPC delivery. Record each stage before acting.
					for _, request := range []struct {
						name string
						seen *bool
						run  func()
					}{
						{"ping", &pingSeen, func() { r.Send(nativeLoopPing{}) }},
						{"cancel", &cancelSeen, cancel},
					} {
						if *request.seen {
							continue
						}
						record := func(stage string) {
							_ = os.WriteFile(filepath.Join(component.statusDir, "control-state"), []byte(fmt.Sprintf("%s ping=%t cancel=%t %s %s", time.Now().Format(time.RFC3339Nano), pingSeen, cancelSeen, request.name, stage)), 0o600)
						}
						record("reading")
						data, err := os.ReadFile(filepath.Join(component.statusDir, "request-"+request.name))
						record(fmt.Sprintf("read data=%q err=%v", data, err))
						if os.IsNotExist(err) {
							continue
						}
						if err != nil {
							_ = os.WriteFile(filepath.Join(component.statusDir, "failure"), []byte(fmt.Sprintf("read %s request: %v", request.name, err)), 0o600)
							cancel()
							return
						}
						if string(data) != request.name {
							continue // The parent may still be completing the write.
						}
						*request.seen = true
						record("triggering")
						request.run()
						record("returned")
					}
				}
			}
		}()
	}

	if scenario == "runtime-escape-cancel" {
		// Bare Escape leaves KeyReader.pendingPeek waiting for the next console
		// read. Cancel from its delivered key event without sending another key.
		component.escapeCancel = cancel
	}
	err := r.Run()
	if scenario == "runtime-error" {
		if err == nil || !strings.Contains(err.Error(), "native error exit") {
			t.Fatalf("expected runtime effect failure, got %v", err)
		}
	} else if err != nil && scenario != "disconnect" {
		t.Fatal(err)
	}
	if scenario != "disconnect" {
		expectNativeModes(t, before)
		if component.disconnected.Load() != 1 {
			t.Fatal("runtime did not disconnect exactly once")
		}
	}
	if component.statusDir != "" {
		if err := writeNativeStatus(component.statusDir, "RUN-RETURNED"); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("NATIVE-SCENARIO-PASSED")
}

type nativeMouseObserved Mouse
type nativeFloodTick struct{}
type nativeLoopPing struct{}

type nativeProbe struct {
	connect       Effect
	interactive   bool
	escapeCancel  context.CancelFunc
	statusDir     string
	flood         bool
	ticks         int
	width, height int
	disconnected  atomic.Int32
}

func (p *nativeProbe) Connect() Effect { return p.connect }
func (p *nativeProbe) Disconnect()     { p.disconnected.Add(1) }
func (p *nativeProbe) HandleEvent(event Event) Effect {
	switch event := event.(type) {
	case KeyPress:
		switch event.String() {
		case "ctrl+z":
			fmt.Println("CTRL-Z-OK")
		case "up":
			fmt.Println("ARROW-OK")
		case "😀":
			fmt.Println("UNICODE-OK")
		case "q":
			return Quit
		case "ctrl+c":
			fmt.Println("CTRL-C-KEY")
			return Quit
		case "e":
			return func() Event { panic("native error exit") }
		case "f":
			if p.statusDir != "" {
				p.flood = true
				return Batch(Tick(time.Millisecond, func(time.Time) Event { return nativeFloodTick{} }), func() Event {
					if err := waitForNativeConsoleCall("windows.WriteConsole(", 50*time.Millisecond); err != nil {
						_ = os.WriteFile(filepath.Join(p.statusDir, "failure"), []byte(err.Error()), 0o600)
						return effectFailed{err}
					}
					if err := writeNativeStatus(p.statusDir, "OUTPUT-BLOCKED"); err != nil {
						return effectFailed{err}
					}
					return nil
				})
			}
		case "k":
			if p.statusDir != "" {
				if err := writeNativeStatus(p.statusDir, "KEY-HANDLED"); err != nil {
					return func() Event { return effectFailed{err} }
				}
			}
		case "esc":
			if p.escapeCancel != nil {
				p.escapeCancel()
			}
		}
	case PasteEvent:
		if event.Text == "日本語😀\npaste" {
			fmt.Println("PASTE-OK")
		}
	case nativeMouseObserved:
		if event.X == 9 && event.Y == 4 && event.Button == MouseLeft {
			fmt.Println("MOUSE-OK")
		}
	case ResizeEvent:
		p.width, p.height = event.Width, event.Height
		if event.Width == 300 && event.Height == 120 {
			fmt.Println("HIGH-OUTPUT-READY")
		}
		if event.Width == 80 && event.Height == 20 {
			fmt.Println("RESIZE-OK")
		}
	case nativeFloodTick:
		if p.flood {
			p.ticks++
			return Tick(time.Millisecond, func(time.Time) Event { return nativeFloodTick{} })
		}
	case nativeLoopPing:
		if err := writeNativeStatus(p.statusDir, "APP-EVENT-HANDLED"); err != nil {
			return func() Event { return effectFailed{err} }
		}
	}
	return nil
}
func (p *nativeProbe) Render() Frame {
	frame := NewFrame("INPUT-READY")
	if p.flood {
		row := fmt.Sprintf("FRAME=%08d ", p.ticks)
		row += strings.Repeat("X", max(0, p.width-len(row)))
		frame.Content = strings.TrimSuffix(strings.Repeat(row+"\n", max(1, p.height)), "\n")
	}
	if p.interactive {
		frame.MouseMode = MouseModeCellMotion
		frame.OnMouse = func(event MouseEvent) Effect {
			mouse := nativeMouseObserved(event.Mouse())
			return func() Event { return mouse }
		}
	}
	return frame
}

func writeNativeStatus(directory, name string) error {
	return os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600)
}
