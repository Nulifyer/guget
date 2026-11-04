package term

// InputMsg is any terminal input event. Key events remain the primary
// shape; mouse/paste/focus are additive for layers that opt in.
//
// We use an interface with an unexported marker so only types in this
// package can satisfy it -- external code can't accidentally spawn
// new event variants.
type InputMsg interface {
	inputMsg()
}

func (Key) inputMsg()       {}
func (PasteMsg) inputMsg()  {}
func (MouseMsg) inputMsg()  {}
func (FocusMsg) inputMsg()  {}
func (BlurMsg) inputMsg()   {}
func (ResizeMsg) inputMsg() {}

// PasteMsg is a batched paste delivered by bracketed-paste mode. Layers
// that want paste to behave differently from typed input (e.g. suppress
// autocomplete, single-undo-snapshot the whole block) should consume
// this. Layers that don't handle it get the usual per-byte key events
// only if the terminal isn't in bracketed-paste mode -- with
// PasteOn active, the bytes between CSI 200~ and CSI 201~ are captured
// and delivered as one PasteMsg with no Key events in between.
type PasteMsg struct {
	Text string
}

// MouseButton identifies which pointer gesture produced a MouseMsg.
// Wheel "buttons" are modeled the same way xterm encodes them (as
// button codes 64/65) so the decoder doesn't need a separate enum.
type MouseButton int

const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
	MouseButtonMiddle
	MouseButtonRight
	MouseButtonWheelUp
	MouseButtonWheelDown
	MouseButtonWheelLeft
	MouseButtonWheelRight
)

// MouseAction separates press/release/motion. SGR mouse encoding
// distinguishes press from release via the final byte (M vs m); legacy
// X10 does not and so always reports Press.
type MouseAction int

const (
	MouseActionPress MouseAction = iota
	MouseActionRelease
	MouseActionMotion
)

// MouseMsg is a pointer event. Coordinates are 1-based to match the
// rest of the TUI (MoveTo, Cellbuf positions). Modifier bits mirror Key
// for uniformity.
type MouseMsg struct {
	X, Y   int
	Button MouseButton
	Action MouseAction
	Shift  bool
	Alt    bool
	Ctrl   bool
}

// FocusMsg fires when the terminal window gains focus (requires
// PasteOn's sibling mode ?1004h to be enabled). BlurMsg fires on loss.
// Neither carries a payload.
type FocusMsg struct{}
type BlurMsg struct{}

// ResizeMsg is reserved for the future; the current loop polls the
// terminal size each frame so it isn't emitted today. Included here so
// the InputMsg taxonomy is complete and new layers can switch on it
// without touching this file later.
type ResizeMsg struct {
	Width, Height int
}
