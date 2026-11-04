//go:build windows

package term

import (
	"io"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// StdinReader returns a byte source that reads console input via
// ReadConsoleW. Two layers on Windows translate ^Z -> EOF and close
// the app on Ctrl+Z:
//  1. Go's internal/poll console wrapper (golang/go#3530) -- bypassed
//     by not using os.Stdin.Read.
//  2. The Win32 ReadFile path itself, at the ConDrv driver layer
//     (microsoft/terminal#4958) -- raw mode does not disable this.
//
// ReadConsoleW is the only documented API that never processes ^Z,
// so we use it and decode the UTF-16 result to UTF-8 bytes.
//
// The handle is os.Stdin's existing handle, which term.MakeRaw
// already put into raw mode (console modes are per-handle, so a
// fresh CONIN$ open would come back cooked).
func StdinReader() io.Reader {
	return &conInReader{h: windows.Handle(os.Stdin.Fd())}
}

type conInReader struct {
	h                      windows.Handle
	wbuf                   [256]uint16
	buf                    []byte
	suppressPasteSynthesis bool
	textDecoder            utf16InputDecoder
	readErr                error
}

// pasteEventThreshold is the minimum number of key-down events that
// must be queued simultaneously before we treat the burst as a paste.
// Normal typing rarely queues more than 1-2 events; a paste dumps all
// characters at once. 8 avoids false positives from fast typing or
// held-key repeat while still catching short multi-line pastes.
const pasteEventThreshold = 8

func (c *conInReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(c.buf) == 0 && c.readErr != nil {
		return 0, c.readErr
	}
	if len(c.buf) == 0 && !c.suppressPasteSynthesis {
		// ReadConsoleW often does not relay the terminal's ESC[200~
		// brackets. When the console queue already looks paste-like,
		// drain that queue directly and synthesize bracketed paste
		// markers so KeyReader receives one PasteMsg.
		if c.pendingKeyCount() >= pasteEventThreshold {
			if synth := c.readPasteBatch(); len(synth) > 0 {
				c.buf = synth
			}
		}
	}
	for len(c.buf) == 0 {
		var read uint32
		if err := windows.ReadConsole(c.h, &c.wbuf[0], uint32(len(c.wbuf)), &read, nil); err != nil {
			c.readErr = err
		}
		if read == 0 && c.readErr == nil {
			c.readErr = io.EOF
		}
		c.buf = []byte(c.textDecoder.decode(c.wbuf[:read], c.readErr != nil))
		if len(c.buf) == 0 && c.readErr != nil {
			return 0, c.readErr
		}
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *conInReader) setPasteSynthesisEnabled(enabled bool) {
	c.suppressPasteSynthesis = !enabled
}

// pendingKeyCount returns the number of key-down events with
// printable/control characters currently queued. Native bracketed
// paste starters are treated as paste batches even before the normal
// heuristic threshold is reached, so a queued ESC[200~ opener does not
// fall back to the timing-sensitive ReadConsoleW byte path.
func (c *conInReader) pendingKeyCount() int {
	var total uint32
	if err := windows.GetNumberOfConsoleInputEvents(c.h, &total); err != nil || total == 0 {
		return 0
	}
	peekBuf := make([]inputRecord, min(int(total), 256))
	var read uint32
	r1, _, _ := procPeekConsoleInputW.Call(
		uintptr(c.h),
		uintptr(unsafe.Pointer(&peekBuf[0])),
		uintptr(len(peekBuf)),
		uintptr(unsafe.Pointer(&read)),
	)
	if r1 == 0 {
		return 0
	}
	count := 0
	var prefix strings.Builder
	prefix.Grow(len(bracketedPasteStart))
	firstPrintable := true
	startsWithEsc := false
	for i := uint32(0); i < read; i++ {
		if peekBuf[i].EventType != keyEvent {
			continue
		}
		kr := (*keyEventRecord)(unsafe.Pointer(&peekBuf[i].Event[0]))
		if kr.KeyDown == 0 || kr.UnicodeChar == 0 {
			continue
		}
		if firstPrintable {
			startsWithEsc = kr.UnicodeChar == 0x1B
			firstPrintable = false
		}
		if prefix.Len() < len(bracketedPasteStart) {
			prefix.WriteRune(rune(kr.UnicodeChar))
			if prefix.String() == bracketedPasteStart {
				return pasteEventThreshold
			}
		}
		count++
	}
	if total < uint32(pasteEventThreshold) {
		return 0
	}
	if startsWithEsc {
		return 0
	}
	return count
}

// readPasteBatch drains pending console input and returns a bracketed
// paste sequence. Native ESC[200~ batches are passed through as-is;
// every other burst is wrapped once.
func (c *conInReader) readPasteBatch() []byte {
	recBuf := make([]inputRecord, 256)
	var units []uint16
	for {
		var n uint32
		if err := windows.GetNumberOfConsoleInputEvents(c.h, &n); err != nil || n == 0 {
			break
		}
		toRead := min(int(n), len(recBuf))
		var read uint32
		r1, _, _ := procReadConsoleInputW.Call(
			uintptr(c.h),
			uintptr(unsafe.Pointer(&recBuf[0])),
			uintptr(toRead),
			uintptr(unsafe.Pointer(&read)),
		)
		if r1 == 0 || read == 0 {
			break
		}
		for i := uint32(0); i < read; i++ {
			if recBuf[i].EventType != keyEvent {
				continue
			}
			kr := (*keyEventRecord)(unsafe.Pointer(&recBuf[i].Event[0]))
			if kr.KeyDown == 0 || kr.UnicodeChar == 0 {
				continue
			}
			ch := kr.UnicodeChar
			rep := kr.RepeatCount
			if rep == 0 {
				rep = 1
			}
			for j := uint16(0); j < rep; j++ {
				units = append(units, ch)
			}
		}
	}
	if len(units) == 0 {
		return nil
	}
	content := c.textDecoder.decode(units, false)
	if content == "" {
		return nil
	}
	return wrapPasteBatch(content)
}

func wrapPasteBatch(content string) []byte {
	if strings.Contains(content, bracketedPasteStart) || strings.Contains(content, bracketedPasteEnd) {
		return []byte(content)
	}
	return []byte(bracketedPasteStart + content + bracketedPasteEnd)
}

const keyEvent = 0x0001

type inputRecord struct {
	EventType uint16
	_         uint16
	// Union of event records; KEY_EVENT_RECORD is the largest we care
	// about. 16 bytes is enough to cover it on both 32- and 64-bit.
	Event [16]byte
}

type keyEventRecord struct {
	KeyDown         int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

var (
	modKernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procPeekConsoleInputW = modKernel32.NewProc("PeekConsoleInputW")
	procReadConsoleInputW = modKernel32.NewProc("ReadConsoleInputW")
)
