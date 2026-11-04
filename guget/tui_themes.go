package main

import (
	"image/color"
	"os"
	"strings"

	style "github.com/nulifyer/guget/internal/ui/style"
)

var (
	// colors
	colorBorder color.Color
	colorMuted  color.Color
	colorText   color.Color
	colorSubtle color.Color
	colorAccent color.Color
	colorGreen  color.Color
	colorYellow color.Color
	colorRed    color.Color
	colorPurple color.Color
	colorCyan   color.Color
)

var (
	// text styles
	styleMuted      style.Style
	styleSubtle     style.Style
	styleSubtleBold style.Style
	styleText       style.Style
	styleTextBold   style.Style
	styleAccent     style.Style
	styleAccentBold style.Style
	styleGreen      style.Style
	styleYellow     style.Style
	styleYellowBold style.Style
	styleRed        style.Style
	styleRedBold    style.Style
	stylePurple     style.Style
	styleCyan       style.Style
	styleBorder     style.Style

	// Layout styles
	styleHeaderTitle   style.Style
	styleHeaderBar     style.Style
	styleFooterBar     style.Style
	styleOverlay       style.Style
	styleOverlayDanger style.Style
	stylePanel         style.Style
	stylePanelNoPad    style.Style
)

type Theme struct {
	Border color.Color
	Muted  color.Color
	Text   color.Color
	Subtle color.Color
	Accent color.Color
	Green  color.Color
	Yellow color.Color
	Red    color.Color
	Purple color.Color
	Cyan   color.Color
}

var validThemeNames = []string{
	"auto", "auto-light", "auto-dark", "dracula",
	"catppuccin-mocha", "catppuccin-macchiato", "catppuccin-frappe", "catppuccin-latte",
	"nord", "tokyo-night", "everforest", "gruvbox",
}

var themes = map[string]Theme{
	// "auto" is resolved at runtime in initTheme via HasDarkBackground
	"auto-light": {
		Border: style.Color("#d0d7de"),
		Muted:  style.Color("#656d76"),
		Text:   style.Color("#1f2328"),
		Subtle: style.Color("#656d76"),
		Accent: style.Color("#0969da"),
		Green:  style.Color("#1a7f37"),
		Yellow: style.Color("#9a6700"),
		Red:    style.Color("#cf222e"),
		Purple: style.Color("#8250df"),
		Cyan:   style.Color("#0d7680"),
	},
	"auto-dark": {
		Border: style.Color("#30363d"),
		Muted:  style.Color("#8b949e"),
		Text:   style.Color("#e6edf3"),
		Subtle: style.Color("#8b949e"),
		Accent: style.Color("#58a6ff"),
		Green:  style.Color("#3fb950"),
		Yellow: style.Color("#d29922"),
		Red:    style.Color("#f85149"),
		Purple: style.Color("#bc8cff"),
		Cyan:   style.Color("#56d7c2"),
	},
	"dracula": {
		Border: style.Color("#44475a"),
		Muted:  style.Color("#6272a4"),
		Text:   style.Color("#f8f8f2"),
		Subtle: style.Color("#6272a4"),
		Accent: style.Color("#8be9fd"),
		Green:  style.Color("#50fa7b"),
		Yellow: style.Color("#f1fa8c"),
		Red:    style.Color("#ff5555"),
		Purple: style.Color("#bd93f9"),
		Cyan:   style.Color("#8be9fd"),
	},
	"catppuccin-mocha": {
		Border: style.Color("#313244"),
		Muted:  style.Color("#585b70"),
		Text:   style.Color("#cdd6f4"),
		Subtle: style.Color("#a6adc8"),
		Accent: style.Color("#89b4fa"),
		Green:  style.Color("#a6e3a1"),
		Yellow: style.Color("#f9e2af"),
		Red:    style.Color("#f38ba8"),
		Purple: style.Color("#cba6f7"),
		Cyan:   style.Color("#94e2d5"),
	},
	"catppuccin-macchiato": {
		Border: style.Color("#5b6078"),
		Muted:  style.Color("#6e738d"),
		Text:   style.Color("#cad3f5"),
		Subtle: style.Color("#a5adcb"),
		Accent: style.Color("#8aadf4"),
		Green:  style.Color("#a6da95"),
		Yellow: style.Color("#eed49f"),
		Red:    style.Color("#ed8796"),
		Purple: style.Color("#c6a0f6"),
		Cyan:   style.Color("#8bd5ca"),
	},
	"catppuccin-frappe": {
		Border: style.Color("#626880"),
		Muted:  style.Color("#737994"),
		Text:   style.Color("#c6d0f5"),
		Subtle: style.Color("#a5adce"),
		Accent: style.Color("#8caaee"),
		Green:  style.Color("#a6d189"),
		Yellow: style.Color("#e5c890"),
		Red:    style.Color("#e78284"),
		Purple: style.Color("#ca9ee6"),
		Cyan:   style.Color("#81c8be"),
	},
	"catppuccin-latte": {
		Border: style.Color("#ccd0da"),
		Muted:  style.Color("#9ca0b0"),
		Text:   style.Color("#4c4f69"),
		Subtle: style.Color("#6c6f85"),
		Accent: style.Color("#1e66f5"),
		Green:  style.Color("#40a02b"),
		Yellow: style.Color("#df8e1d"),
		Red:    style.Color("#d20f39"),
		Purple: style.Color("#8839ef"),
		Cyan:   style.Color("#179299"),
	},
	"nord": {
		Border: style.Color("#3b4252"),
		Muted:  style.Color("#4c566a"),
		Text:   style.Color("#eceff4"),
		Subtle: style.Color("#d8dee9"),
		Accent: style.Color("#88c0d0"),
		Green:  style.Color("#a3be8c"),
		Yellow: style.Color("#ebcb8b"),
		Red:    style.Color("#bf616a"),
		Purple: style.Color("#b48ead"),
		Cyan:   style.Color("#8fbcbb"),
	},
	"tokyo-night": {
		Border: style.Color("#292e42"),
		Muted:  style.Color("#565f89"),
		Text:   style.Color("#c0caf5"),
		Subtle: style.Color("#a9b1d6"),
		Accent: style.Color("#7aa2f7"),
		Green:  style.Color("#9ece6a"),
		Yellow: style.Color("#e0af68"),
		Red:    style.Color("#f7768e"),
		Purple: style.Color("#bb9af7"),
		Cyan:   style.Color("#7dcfff"),
	},
	"everforest": {
		Border: style.Color("#475258"),
		Muted:  style.Color("#7a8478"),
		Text:   style.Color("#d3c6aa"),
		Subtle: style.Color("#9da9a0"),
		Accent: style.Color("#7fbbb3"),
		Green:  style.Color("#a7c080"),
		Yellow: style.Color("#dbbc7f"),
		Red:    style.Color("#e67e80"),
		Purple: style.Color("#d699b6"),
		Cyan:   style.Color("#83c092"),
	},
	"gruvbox": {
		Border: style.Color("#665c54"),
		Muted:  style.Color("#a89984"),
		Text:   style.Color("#ebdbb2"),
		Subtle: style.Color("#bdae93"),
		Accent: style.Color("#83a598"),
		Green:  style.Color("#b8bb26"),
		Yellow: style.Color("#fabd2f"),
		Red:    style.Color("#fb4934"),
		Purple: style.Color("#d3869b"),
		Cyan:   style.Color("#8ec07c"),
	},
}

// initTheme applies the named theme to the package-level color and style vars.
// Call this before NewApp. If noColor is true, all color output is disabled.
func initTheme(name string, noColor bool) {
	hyperlinkEnabled = true
	if noColor {
		// NoColor omits foreground and background sequences.
		// Setting all colors to NoColor effectively disables color output.
		nc := style.NoColor{}
		colorBorder = nc
		colorMuted = nc
		colorText = nc
		colorSubtle = nc
		colorAccent = nc
		colorGreen = nc
		colorYellow = nc
		colorRed = nc
		colorPurple = nc
		colorCyan = nc
		rebuildStyles()
		return
	}

	lower := strings.ToLower(name)

	// "auto" resolves to auto-dark or auto-light based on terminal background.
	if lower == "auto" {
		if style.HasDarkBackground(os.Stdin, os.Stdout) {
			lower = "auto-dark"
		} else {
			lower = "auto-light"
		}
	}

	t, ok := themes[lower]
	if !ok {
		logWarn("Unknown theme %q, falling back to \"auto-dark\"", name)
		t = themes["auto-dark"]
	}

	colorBorder = t.Border
	colorMuted = t.Muted
	colorText = t.Text
	colorSubtle = t.Subtle
	colorAccent = t.Accent
	colorGreen = t.Green
	colorYellow = t.Yellow
	colorRed = t.Red
	colorPurple = t.Purple
	colorCyan = t.Cyan

	rebuildStyles()
}

// rebuildStyles reassigns every style var from the current color vars.
func rebuildStyles() {
	// text styles
	styleMuted = style.NewStyle().Foreground(colorMuted)
	styleSubtle = style.NewStyle().Foreground(colorSubtle)
	styleSubtleBold = style.NewStyle().Foreground(colorSubtle).Bold(true)
	styleText = style.NewStyle().Foreground(colorText)
	styleTextBold = style.NewStyle().Foreground(colorText).Bold(true)
	styleAccent = style.NewStyle().Foreground(colorAccent)
	styleAccentBold = style.NewStyle().Foreground(colorAccent).Bold(true)
	styleGreen = style.NewStyle().Foreground(colorGreen)
	styleYellow = style.NewStyle().Foreground(colorYellow)
	styleYellowBold = style.NewStyle().Foreground(colorYellow).Bold(true)
	styleRed = style.NewStyle().Foreground(colorRed)
	styleRedBold = style.NewStyle().Foreground(colorRed).Bold(true)
	stylePurple = style.NewStyle().Foreground(colorPurple)
	styleCyan = style.NewStyle().Foreground(colorCyan)
	styleBorder = style.NewStyle().Foreground(colorBorder)

	// layout styles
	styleHeaderTitle = styleAccentBold.Padding(0, 2)
	styleHeaderBar = style.NewStyle().BorderBottom(true).BorderStyle(style.NormalBorder()).BorderBottomForeground(colorBorder)
	styleFooterBar = style.NewStyle().BorderTop(true).BorderStyle(style.NormalBorder()).BorderTopForeground(colorBorder).Padding(0, 2)
	styleOverlay = style.NewStyle().Border(style.RoundedBorder()).BorderForeground(colorAccent).Padding(1, 2)
	styleOverlayDanger = style.NewStyle().Border(style.RoundedBorder()).BorderForeground(colorRed).Padding(1, 2)
	stylePanel = style.NewStyle().Border(style.RoundedBorder()).BorderForeground(colorBorder).Padding(0, 1)
	stylePanelNoPad = style.NewStyle().Border(style.RoundedBorder()).BorderForeground(colorBorder)

	// log styles
	rebuildLogStyles()
}
