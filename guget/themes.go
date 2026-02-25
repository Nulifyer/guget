package main

import (
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"
)

type Theme struct {
	Border lipgloss.TerminalColor
	Muted  lipgloss.TerminalColor
	Text   lipgloss.TerminalColor
	Subtle lipgloss.TerminalColor
	Accent lipgloss.TerminalColor
	Green  lipgloss.TerminalColor
	Yellow lipgloss.TerminalColor
	Red    lipgloss.TerminalColor
	Purple lipgloss.TerminalColor
	Cyan   lipgloss.TerminalColor
}

var validThemeNames = []string{
	"auto", "auto-light", "auto-dark", "dracula",
	"catppuccin-mocha", "catppuccin-macchiato", "catppuccin-frappe", "catppuccin-latte",
	"nord", "tokyo-night", "everforest", "gruvbox",
}

var themes = map[string]Theme{
	"auto": {
		Border: lipgloss.AdaptiveColor{Dark: "#30363d", Light: "#d0d7de"},
		Muted:  lipgloss.AdaptiveColor{Dark: "#484f58", Light: "#8c959f"},
		Text:   lipgloss.AdaptiveColor{Dark: "#e6edf3", Light: "#1f2328"},
		Subtle: lipgloss.AdaptiveColor{Dark: "#8b949e", Light: "#656d76"},
		Accent: lipgloss.AdaptiveColor{Dark: "#58a6ff", Light: "#0969da"},
		Green:  lipgloss.AdaptiveColor{Dark: "#3fb950", Light: "#1a7f37"},
		Yellow: lipgloss.AdaptiveColor{Dark: "#d29922", Light: "#9a6700"},
		Red:    lipgloss.AdaptiveColor{Dark: "#f85149", Light: "#cf222e"},
		Purple: lipgloss.AdaptiveColor{Dark: "#bc8cff", Light: "#8250df"},
		Cyan:   lipgloss.AdaptiveColor{Dark: "#56d7c2", Light: "#0d7680"},
	},
	"auto-light": {
		Border: lipgloss.Color("#d0d7de"),
		Muted:  lipgloss.Color("#8c959f"),
		Text:   lipgloss.Color("#1f2328"),
		Subtle: lipgloss.Color("#656d76"),
		Accent: lipgloss.Color("#0969da"),
		Green:  lipgloss.Color("#1a7f37"),
		Yellow: lipgloss.Color("#9a6700"),
		Red:    lipgloss.Color("#cf222e"),
		Purple: lipgloss.Color("#8250df"),
		Cyan:   lipgloss.Color("#0d7680"),
	},
	"auto-dark": {
		Border: lipgloss.Color("#30363d"),
		Muted:  lipgloss.Color("#484f58"),
		Text:   lipgloss.Color("#e6edf3"),
		Subtle: lipgloss.Color("#8b949e"),
		Accent: lipgloss.Color("#58a6ff"),
		Green:  lipgloss.Color("#3fb950"),
		Yellow: lipgloss.Color("#d29922"),
		Red:    lipgloss.Color("#f85149"),
		Purple: lipgloss.Color("#bc8cff"),
		Cyan:   lipgloss.Color("#56d7c2"),
	},
	"dracula": {
		Border: lipgloss.Color("#44475a"),
		Muted:  lipgloss.Color("#6272a4"),
		Text:   lipgloss.Color("#f8f8f2"),
		Subtle: lipgloss.Color("#6272a4"),
		Accent: lipgloss.Color("#8be9fd"),
		Green:  lipgloss.Color("#50fa7b"),
		Yellow: lipgloss.Color("#f1fa8c"),
		Red:    lipgloss.Color("#ff5555"),
		Purple: lipgloss.Color("#bd93f9"),
		Cyan:   lipgloss.Color("#8be9fd"),
	},
	"catppuccin-mocha": {
		Border: lipgloss.Color("#313244"),
		Muted:  lipgloss.Color("#585b70"),
		Text:   lipgloss.Color("#cdd6f4"),
		Subtle: lipgloss.Color("#a6adc8"),
		Accent: lipgloss.Color("#89b4fa"),
		Green:  lipgloss.Color("#a6e3a1"),
		Yellow: lipgloss.Color("#f9e2af"),
		Red:    lipgloss.Color("#f38ba8"),
		Purple: lipgloss.Color("#cba6f7"),
		Cyan:   lipgloss.Color("#94e2d5"),
	},
	"catppuccin-macchiato": {
		Border: lipgloss.Color("#5b6078"),
		Muted:  lipgloss.Color("#6e738d"),
		Text:   lipgloss.Color("#cad3f5"),
		Subtle: lipgloss.Color("#a5adcb"),
		Accent: lipgloss.Color("#8aadf4"),
		Green:  lipgloss.Color("#a6da95"),
		Yellow: lipgloss.Color("#eed49f"),
		Red:    lipgloss.Color("#ed8796"),
		Purple: lipgloss.Color("#c6a0f6"),
		Cyan:   lipgloss.Color("#8bd5ca"),
	},
	"catppuccin-frappe": {
		Border: lipgloss.Color("#626880"),
		Muted:  lipgloss.Color("#737994"),
		Text:   lipgloss.Color("#c6d0f5"),
		Subtle: lipgloss.Color("#a5adce"),
		Accent: lipgloss.Color("#8caaee"),
		Green:  lipgloss.Color("#a6d189"),
		Yellow: lipgloss.Color("#e5c890"),
		Red:    lipgloss.Color("#e78284"),
		Purple: lipgloss.Color("#ca9ee6"),
		Cyan:   lipgloss.Color("#81c8be"),
	},
	"catppuccin-latte": {
		Border: lipgloss.Color("#ccd0da"),
		Muted:  lipgloss.Color("#9ca0b0"),
		Text:   lipgloss.Color("#4c4f69"),
		Subtle: lipgloss.Color("#6c6f85"),
		Accent: lipgloss.Color("#1e66f5"),
		Green:  lipgloss.Color("#40a02b"),
		Yellow: lipgloss.Color("#df8e1d"),
		Red:    lipgloss.Color("#d20f39"),
		Purple: lipgloss.Color("#8839ef"),
		Cyan:   lipgloss.Color("#179299"),
	},
	"nord": {
		Border: lipgloss.Color("#3b4252"),
		Muted:  lipgloss.Color("#4c566a"),
		Text:   lipgloss.Color("#eceff4"),
		Subtle: lipgloss.Color("#d8dee9"),
		Accent: lipgloss.Color("#88c0d0"),
		Green:  lipgloss.Color("#a3be8c"),
		Yellow: lipgloss.Color("#ebcb8b"),
		Red:    lipgloss.Color("#bf616a"),
		Purple: lipgloss.Color("#b48ead"),
		Cyan:   lipgloss.Color("#8fbcbb"),
	},
	"tokyo-night": {
		Border: lipgloss.Color("#292e42"),
		Muted:  lipgloss.Color("#565f89"),
		Text:   lipgloss.Color("#c0caf5"),
		Subtle: lipgloss.Color("#a9b1d6"),
		Accent: lipgloss.Color("#7aa2f7"),
		Green:  lipgloss.Color("#9ece6a"),
		Yellow: lipgloss.Color("#e0af68"),
		Red:    lipgloss.Color("#f7768e"),
		Purple: lipgloss.Color("#bb9af7"),
		Cyan:   lipgloss.Color("#7dcfff"),
	},
	"everforest": {
		Border: lipgloss.Color("#475258"),
		Muted:  lipgloss.Color("#7a8478"),
		Text:   lipgloss.Color("#d3c6aa"),
		Subtle: lipgloss.Color("#9da9a0"),
		Accent: lipgloss.Color("#7fbbb3"),
		Green:  lipgloss.Color("#a7c080"),
		Yellow: lipgloss.Color("#dbbc7f"),
		Red:    lipgloss.Color("#e67e80"),
		Purple: lipgloss.Color("#d699b6"),
		Cyan:   lipgloss.Color("#83c092"),
	},
	"gruvbox": {
		Border: lipgloss.Color("#665c54"),
		Muted:  lipgloss.Color("#a89984"),
		Text:   lipgloss.Color("#ebdbb2"),
		Subtle: lipgloss.Color("#bdae93"),
		Accent: lipgloss.Color("#83a598"),
		Green:  lipgloss.Color("#b8bb26"),
		Yellow: lipgloss.Color("#fabd2f"),
		Red:    lipgloss.Color("#fb4934"),
		Purple: lipgloss.Color("#d3869b"),
		Cyan:   lipgloss.Color("#8ec07c"),
	},
}

// initTheme applies the named theme to the package-level color and style vars.
// Call this before NewModel. If noColor is true, all color output is disabled.
func initTheme(name string, noColor bool) {
	if noColor {
		hyperlinkEnabled = false
		lipgloss.SetColorProfile(0)
		return
	}

	t, ok := themes[strings.ToLower(name)]
	if !ok {
		logWarn("Unknown theme %q, falling back to \"auto\"", name)
		t = themes["auto"]
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
	styleMuted = lipgloss.NewStyle().Foreground(colorMuted)
	styleSubtle = lipgloss.NewStyle().Foreground(colorSubtle)
	styleSubtleBold = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true)
	styleText = lipgloss.NewStyle().Foreground(colorText)
	styleTextBold = lipgloss.NewStyle().Foreground(colorText).Bold(true)
	styleAccent = lipgloss.NewStyle().Foreground(colorAccent)
	styleAccentBold = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleGreen = lipgloss.NewStyle().Foreground(colorGreen)
	styleYellow = lipgloss.NewStyle().Foreground(colorYellow)
	styleYellowBold = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	styleRed = lipgloss.NewStyle().Foreground(colorRed)
	styleRedBold = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	stylePurple = lipgloss.NewStyle().Foreground(colorPurple)
	styleCyan = lipgloss.NewStyle().Foreground(colorCyan)
	styleBorder = lipgloss.NewStyle().Foreground(colorBorder)

	// layout styles
	styleHeaderTitle = styleAccentBold.Padding(0, 2)
	styleHeaderBar = lipgloss.NewStyle().BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderBottomForeground(colorBorder)
	styleFooterBar = lipgloss.NewStyle().BorderTop(true).BorderStyle(lipgloss.NormalBorder()).BorderTopForeground(colorBorder).Padding(0, 2)
	styleOverlay = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(1, 2)
	styleOverlayDanger = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorRed).Padding(1, 2)
	stylePanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder).Padding(0, 1)
	stylePanelNoPad = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder)

	// log styles
	rebuildLogStyles()
}
