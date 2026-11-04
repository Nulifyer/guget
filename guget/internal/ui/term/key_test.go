package term

import (
	"bytes"
	"strings"
	"testing"
)

func TestFindNavigationFunctionKeyEncodings(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want Key
	}{
		{"\x1bOR", Key{Kind: KeyF3}},
		{"\x1b[13~", Key{Kind: KeyF3}},
		{"\x1b[1;2R", Key{Kind: KeyF3, Shift: true}},
		{"\x1b[13;2~", Key{Kind: KeyF3, Shift: true}},
		{"\x1bé", Key{Kind: KeyRune, Rune: 'é', Alt: true}},
		{"\x15", Key{Kind: KeyRune, Rune: 'u', Ctrl: true}},
		{"\x1bu", Key{Kind: KeyRune, Rune: 'u', Alt: true}},
		{"\x1b/", Key{Kind: KeyRune, Rune: '/', Alt: true}},
		{"\x1f", Key{Kind: KeyRune, Rune: '_', Ctrl: true}},
	} {
		got := readAll(t, tc.raw, 1)[0]
		if got != tc.want {
			t.Fatalf("%q decoded as %+v, want %+v", tc.raw, got, tc.want)
		}
	}
}

// readAll drains the KeyReader until n messages arrive. Tests supply a
// byte stream and expect a specific sequence of InputMsg values.
func readAll(t *testing.T, raw string, n int) []InputMsg {
	t.Helper()
	kr := NewKeyReader(bytes.NewReader([]byte(raw)))
	out := make([]InputMsg, 0, n)
	for i := 0; i < n; i++ {
		m, err := kr.Read()
		if err != nil {
			t.Fatalf("Read %d: %v", i, err)
		}
		out = append(out, m)
	}
	return out
}

func TestBracketedPasteSingleMessage(t *testing.T) {
	// CSI 200~ <payload> CSI 201~ -> one PasteMsg with the payload.
	payload := "SELECT 1;\nSELECT 2;"
	raw := "\x1b[200~" + payload + "\x1b[201~"
	got := readAll(t, raw, 1)
	p, ok := got[0].(PasteMsg)
	if !ok {
		t.Fatalf("want PasteMsg, got %T", got[0])
	}
	if p.Text != payload {
		t.Errorf("paste text = %q, want %q", p.Text, payload)
	}
}

func TestBracketedPasteNestedStartMarkerStripped(t *testing.T) {
	// Windows/ConPTY can expose a native bracketed paste and then batch
	// the remaining queued paste bytes. If that batch gets wrapped again,
	// the editor must not receive the inner control marker as text.
	payload := "SELECT 1;"
	raw := bracketedPasteStart + bracketedPasteStart + payload + bracketedPasteEnd + bracketedPasteEnd
	got := readAll(t, raw, 1)
	p, ok := got[0].(PasteMsg)
	if !ok {
		t.Fatalf("want PasteMsg, got %T", got[0])
	}
	if p.Text != payload {
		t.Errorf("paste text = %q, want %q", p.Text, payload)
	}
}

func TestOversizedBracketedPasteDrainsBeforeNextKey(t *testing.T) {
	payload := strings.Repeat("x", 16*1024*1024+1)
	raw := "\x1b[200~" + payload + "\x1b[201~z"
	got := readAll(t, raw, 2)

	p, ok := got[0].(PasteMsg)
	if !ok {
		t.Fatalf("want PasteMsg, got %T", got[0])
	}
	if p.Text != "" {
		t.Fatalf("oversized paste text = %d bytes, want dropped", len(p.Text))
	}
	k, ok := got[1].(Key)
	if !ok {
		t.Fatalf("want Key after drained paste, got %T", got[1])
	}
	if k.Kind != KeyRune || k.Rune != 'z' {
		t.Fatalf("next key = %+v, want rune 'z'", k)
	}
}

func TestSGRMousePressRelease(t *testing.T) {
	// CSI <0;10;5M = left press at (10,5); CSI <0;10;5m = release.
	got := readAll(t, "\x1b[<0;10;5M\x1b[<0;10;5m", 2)
	press, ok := got[0].(MouseMsg)
	if !ok {
		t.Fatalf("want MouseMsg, got %T", got[0])
	}
	if press.Button != MouseButtonLeft || press.Action != MouseActionPress || press.X != 10 || press.Y != 5 {
		t.Errorf("press = %+v", press)
	}
	rel, ok := got[1].(MouseMsg)
	if !ok {
		t.Fatalf("want MouseMsg, got %T", got[1])
	}
	if rel.Action != MouseActionRelease {
		t.Errorf("release action = %v", rel.Action)
	}
}

func TestSGRMouseWheel(t *testing.T) {
	// Wheel up = code 64 (bit 6 set), wheel down = 65.
	got := readAll(t, "\x1b[<64;1;1M\x1b[<65;1;1M", 2)
	up := got[0].(MouseMsg)
	down := got[1].(MouseMsg)
	if up.Button != MouseButtonWheelUp {
		t.Errorf("wheel up button = %v", up.Button)
	}
	if down.Button != MouseButtonWheelDown {
		t.Errorf("wheel down button = %v", down.Button)
	}
}

func TestFocusBlur(t *testing.T) {
	got := readAll(t, "\x1b[I\x1b[O", 2)
	if _, ok := got[0].(FocusMsg); !ok {
		t.Errorf("want FocusMsg, got %T", got[0])
	}
	if _, ok := got[1].(BlurMsg); !ok {
		t.Errorf("want BlurMsg, got %T", got[1])
	}
}

func TestKeyStillDecodesAfterInputMsgSwitch(t *testing.T) {
	// Regression: changing Read's return type to InputMsg should not
	// break ordinary key decoding.
	got := readAll(t, "a\x1b[A\r", 3)
	k0 := got[0].(Key)
	if k0.Kind != KeyRune || k0.Rune != 'a' {
		t.Errorf("rune key = %+v", k0)
	}
	k1 := got[1].(Key)
	if k1.Kind != KeyUp {
		t.Errorf("up key kind = %v", k1.Kind)
	}
	k2 := got[2].(Key)
	if k2.Kind != KeyEnter {
		t.Errorf("enter key kind = %v", k2.Kind)
	}
}
