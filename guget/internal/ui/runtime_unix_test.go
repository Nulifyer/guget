//go:build linux || darwin

package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"
)

// Exercise the real raw-mode event loop in a separate controlling PTY. Python's
// pty.fork works on Linux and macOS without tmux or a third-party Go PTY module.
func TestRuntimeNativePTYLifecycle(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("Python 3 is required for native PTY verification in CI")
		}
		t.Skip("Python 3 is required for the native PTY harness")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"ctrl-c", "sigterm", "cancel-without-input", "output-backpressure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			statusPath := filepath.Join(t.TempDir(), "status")
			if err := os.WriteFile(statusPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, python, "-c", nativePTYLifecycleScript, binary, mode, statusPath)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("native PTY %s: %v\n%s", mode, err, output)
			}
		})
	}
}

type nativePTYProbe struct {
	before                  *xterm.State
	width, height           int
	rawActive, disconnected bool
	resized                 chan struct{}
	resizedOnce             sync.Once
	flood                   bool
	ticks                   int
	status                  *os.File
}

type nativePTYTick struct{}

func (p *nativePTYProbe) Connect() Effect {
	if p.flood {
		return Tick(time.Millisecond, func(time.Time) Event { return nativePTYTick{} })
	}
	return nil
}
func (p *nativePTYProbe) HandleEvent(event Event) Effect {
	switch event := event.(type) {
	case ResizeEvent:
		p.width, p.height = event.Width, event.Height
	case nativePTYTick:
		p.ticks++
		return Tick(time.Millisecond, func(time.Time) Event { return nativePTYTick{} })
	case KeyPress:
		if event.String() == "k" && p.status != nil {
			fmt.Fprintln(p.status, "NATIVE_PTY_KEY_HANDLED")
		}
		if event.String() == "ctrl+c" {
			return Quit
		}
	}
	return nil
}
func (p *nativePTYProbe) Render() Frame {
	state, err := xterm.GetState(int(os.Stdin.Fd()))
	p.rawActive = p.rawActive || err == nil && !reflect.DeepEqual(p.before, state)
	if p.width == 100 && p.height == 30 {
		p.resizedOnce.Do(func() { close(p.resized) })
	}
	header := fmt.Sprintf("PTY_SIZE=%dx%d RAW_ACTIVE=%t", p.width, p.height, p.rawActive)
	if !p.flood {
		return NewFrame(header)
	}
	row := fmt.Sprintf("FLOOD_FRAME=%08d ", p.ticks)
	row += strings.Repeat("X", max(0, p.width-len(row)))
	return NewFrame(header + strings.Repeat("\n"+row, max(0, p.height-1)))
}
func (p *nativePTYProbe) Disconnect() { p.disconnected = true }

func TestRuntimeNativePTYChild(t *testing.T) {
	mode := os.Getenv("GUGET_RUNTIME_PTY_MODE")
	if mode == "" {
		t.Skip("subprocess helper for native PTY lifecycle test")
	}
	var status *os.File
	if path := os.Getenv("GUGET_RUNTIME_PTY_STATUS_PATH"); path != "" {
		var err error
		status, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer status.Close()
	}
	fail := func(format string, args ...any) {
		t.Helper()
		message := fmt.Sprintf(format, args...)
		if status != nil {
			// Report before Fatal: terminal output can deliberately be full.
			fmt.Fprintln(status, "NATIVE_PTY_FAILURE: "+message)
		}
		t.Fatal(message)
	}
	before, err := xterm.GetState(int(os.Stdin.Fd()))
	if err != nil {
		fail("%v", err)
	}
	inputFlags, err := unix.FcntlInt(os.Stdin.Fd(), unix.F_GETFL, 0)
	if err != nil {
		fail("%v", err)
	}
	outputFlags, err := unix.FcntlInt(os.Stdout.Fd(), unix.F_GETFL, 0)
	if err != nil {
		fail("%v", err)
	}
	sink, err := openTerminalOutput()
	if err != nil {
		fail("%v", err)
	}
	owned := sink.(*unixTerminalIO)
	ownedFlags, flagErr := unix.FcntlInt(uintptr(owned.fd), unix.F_GETFL, 0)
	closeErr := sink.Close()
	if flagErr != nil || ownedFlags&unix.O_NONBLOCK == 0 || closeErr != nil {
		fail("owned output is not independently nonblocking: flags=%d error=%v close=%v", ownedFlags, flagErr, closeErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &nativePTYProbe{before: before, resized: make(chan struct{}), flood: mode == "output-backpressure"}
	probe.status = status
	if mode == "cancel-without-input" {
		go func() { <-probe.resized; time.Sleep(250 * time.Millisecond); cancel() }()
	}
	runtime := NewRuntime(ctx, probe)
	if err := runtime.Run(); err != nil {
		fail("%v", err)
	}
	after, err := xterm.GetState(int(os.Stdin.Fd()))
	if err != nil {
		fail("%v", err)
	}
	if !reflect.DeepEqual(before, after) {
		fail("terminal mode was not restored: before=%#v after=%#v", before, after)
	}
	finalInputFlags, inputErr := unix.FcntlInt(os.Stdin.Fd(), unix.F_GETFL, 0)
	finalOutputFlags, outputErr := unix.FcntlInt(os.Stdout.Fd(), unix.F_GETFL, 0)
	if inputErr != nil || outputErr != nil || inputFlags != finalInputFlags || outputFlags != finalOutputFlags {
		fail("runtime changed caller descriptor flags: stdin %d→%d stdout %d→%d errors=%v,%v", inputFlags, finalInputFlags, outputFlags, finalOutputFlags, inputErr, outputErr)
	}
	if !probe.rawActive || !probe.disconnected {
		fail("raw mode or disconnect missing: raw=%t disconnected=%t", probe.rawActive, probe.disconnected)
	}
	if probe.status != nil {
		fmt.Fprintln(probe.status, "NATIVE_PTY_RAW_RESTORED")
	}
	fmt.Println("NATIVE_PTY_RAW_RESTORED")
}

const nativePTYLifecycleScript = `
import errno, fcntl, os, pty, select, signal, struct, sys, termios, time
binary, mode, status_path = sys.argv[1:]
pid, master = pty.fork()
if pid == 0:
    fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
    env = dict(os.environ, GUGET_RUNTIME_PTY_MODE=mode,
               GUGET_RUNTIME_PTY_STATUS_PATH=status_path)
    os.execve(binary, [binary, "-test.run=^TestRuntimeNativePTYChild$", "-test.timeout=20s"], env)
content = bytearray()
status_content = bytearray()
reaped = False
def read_status():
    global status_content
    with open(status_path, "rb") as status_file:
        status_content = status_file.read()
    if b"NATIVE_PTY_FAILURE: " in status_content:
        raise RuntimeError("native helper failed: %r" % (bytes(status_content),))
def receive_until(marker):
    deadline = time.monotonic() + 15
    while marker not in content:
        if time.monotonic() >= deadline:
            raise RuntimeError("timed out waiting for %r; output=%r" % (marker, bytes(content)))
        read_status()
        ready, _, _ = select.select([master], [], [], 0.05)
        if master not in ready:
            continue
        try:
            chunk = os.read(master, 65536)
        except OSError as error:
            if error.errno == errno.EIO:
                raise RuntimeError("PTY closed before %r; output=%r" % (marker, bytes(content))) from error
            raise
        if not chunk:
            raise RuntimeError("PTY EOF before %r; output=%r" % (marker, bytes(content)))
        content.extend(chunk)
def receive_status(marker):
    deadline = time.monotonic() + 10
    while marker not in status_content:
        if time.monotonic() >= deadline:
            raise RuntimeError("runtime stalled behind unread output while waiting for %r; status=%r" % (marker, bytes(status_content)))
        read_status()
        if marker not in status_content:
            time.sleep(0.02)
try:
    receive_until(b"PTY_SIZE=80x24 RAW_ACTIVE=true")
    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
    os.kill(pid, signal.SIGWINCH)
    receive_until(b"PTY_SIZE=100x30 RAW_ACTIVE=true")
    if mode == "ctrl-c":
        os.write(master, b"\x03")
    elif mode == "sigterm":
        os.kill(pid, signal.SIGTERM)
    elif mode == "output-backpressure":
        # Stop draining terminal output long enough for changing full-screen
        # frames to fill the PTY. Keyboard dispatch and cancellation must still
        # finish, observed through a file without relieving backpressure.
        time.sleep(0.7)
        os.write(master, b"k")
        receive_status(b"NATIVE_PTY_KEY_HANDLED")
        os.kill(pid, signal.SIGTERM)
        receive_status(b"NATIVE_PTY_RAW_RESTORED")
    receive_until(b"NATIVE_PTY_RAW_RESTORED")
    _, status = os.waitpid(pid, 0)
    reaped = True
    if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
        raise RuntimeError("child exit=%r; output=%r" % (status, bytes(content)))
    required = (b"\x1b[?1049h", b"\x1b[?2004h")
    if mode != "output-backpressure":
        required += (b"\x1b[?1049l", b"\x1b[?2004l", b"\x1b[?25h")
    for sequence in required:
        if sequence not in content:
            raise RuntimeError("missing terminal lifecycle sequence %r" % (sequence,))
except Exception as error:
    # Surface diagnostics before cleanup; a failed helper can be blocked writing
    # its testing failure to the deliberately full terminal.
    print("native PTY failure: %s" % error, file=sys.stderr, flush=True)
    raise
finally:
    if not reaped:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    os.close(master)
    if not reaped:
        os.waitpid(pid, 0)
`
