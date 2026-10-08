module github.com/nulifyer/guget

go 1.26.0

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.0.10
	charm.land/lipgloss/v2 v2.0.6
	github.com/charmbracelet/x/ansi v0.11.8
	golang.org/x/sys v0.48.0
	golang.org/x/term v0.46.0
)

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20261001125412-878653296cfd // indirect
	github.com/charmbracelet/x/exp/golden v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v1.2.0 // indirect
	golang.org/x/exp v0.0.0-20261007192929-f45ad48fbe92 // indirect
	golang.org/x/sync v0.23.0 // indirect
)

// Preserve the historical terminfo revision while updating the Charm stack.
// Newer Ultraviolet requires v1, so a require entry alone cannot hold this pin.
replace github.com/xo/terminfo => github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e
