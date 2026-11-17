//go:build windows

package term

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWrapPasteBatchPassesThroughNativePasteMarkers(t *testing.T) {
	t.Parallel()

	native := bracketedPasteStart + "SELECT 1" + bracketedPasteEnd
	if got := string(wrapPasteBatch(native)); got != native {
		t.Fatalf("native paste batch = %q, want unchanged %q", got, native)
	}

	inFlightNative := "SELECT 1" + bracketedPasteEnd
	if got := string(wrapPasteBatch(inFlightNative)); got != inFlightNative {
		t.Fatalf("in-flight native paste batch = %q, want unchanged %q", got, inFlightNative)
	}

	const csiPayload = "\x1b[A"
	want := bracketedPasteStart + csiPayload + bracketedPasteEnd
	if got := string(wrapPasteBatch(csiPayload)); got != want {
		t.Fatalf("non-paste CSI batch = %q, want wrapped %q", got, want)
	}
}

func TestRecordYieldsTextMatchesReadConsoleW(t *testing.T) {
	key := func(down bool, vk, char uint16) inputRecord {
		record := inputRecord{EventType: keyEvent}
		k := (*keyEventRecord)(unsafe.Pointer(&record.Event[0]))
		if down {
			k.KeyDown = 1
		}
		k.VirtualKeyCode, k.UnicodeChar = vk, char
		return record
	}
	cases := []struct {
		name   string
		record inputRecord
		want   bool
	}{
		{"character press", key(true, 'A', 'a'), true},
		{"control character press", key(true, 'Z', 0x1A), true},
		{"character release", key(false, 'A', 'a'), false},
		{"modifier press", key(true, windows.VK_SHIFT, 0), false},
		{"alt numpad completion", key(false, windows.VK_MENU, 0xE9), true},
		{"alt release without character", key(false, windows.VK_MENU, 0), false},
		{"focus event", inputRecord{EventType: 0x0010}, false},
		{"buffer size event", inputRecord{EventType: 0x0004}, false},
	}
	for _, tc := range cases {
		if got := recordYieldsText(&tc.record); got != tc.want {
			t.Errorf("%s: recordYieldsText = %v, want %v", tc.name, got, tc.want)
		}
	}
}
