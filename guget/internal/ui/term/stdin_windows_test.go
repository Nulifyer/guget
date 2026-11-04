//go:build windows

package term

import "testing"

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
