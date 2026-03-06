package main

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

var (
	colorBorder color.Color = lipgloss.Color("#30363d")
	colorMuted  color.Color = lipgloss.Color("#484f58")
	colorText   color.Color = lipgloss.Color("#e6edf3")
	colorSubtle color.Color = lipgloss.Color("#8b949e")
	colorAccent color.Color = lipgloss.Color("#58a6ff")
	colorGreen  color.Color = lipgloss.Color("#3fb950")
	colorYellow color.Color = lipgloss.Color("#d29922")
	colorRed    color.Color = lipgloss.Color("#f85149")
	colorPurple color.Color = lipgloss.Color("#bc8cff")
	colorCyan   color.Color = lipgloss.Color("#56d7c2")
)

var (
	// text styles
	styleMuted      = lipgloss.NewStyle().Foreground(colorMuted)
	styleSubtle     = lipgloss.NewStyle().Foreground(colorSubtle)
	styleSubtleBold = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true)
	styleText       = lipgloss.NewStyle().Foreground(colorText)
	styleTextBold   = lipgloss.NewStyle().Foreground(colorText).Bold(true)
	styleAccent     = lipgloss.NewStyle().Foreground(colorAccent)
	styleAccentBold = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleGreen      = lipgloss.NewStyle().Foreground(colorGreen)
	styleYellow     = lipgloss.NewStyle().Foreground(colorYellow)
	styleYellowBold = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	styleRed        = lipgloss.NewStyle().Foreground(colorRed)
	styleRedBold    = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	stylePurple     = lipgloss.NewStyle().Foreground(colorPurple)
	styleCyan       = lipgloss.NewStyle().Foreground(colorCyan)
	styleBorder     = lipgloss.NewStyle().Foreground(colorBorder)

	// Layout styles
	styleHeaderTitle   = styleAccentBold.Padding(0, 2)
	styleHeaderBar     = lipgloss.NewStyle().BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderBottomForeground(colorBorder)
	styleFooterBar     = lipgloss.NewStyle().BorderTop(true).BorderStyle(lipgloss.NormalBorder()).BorderTopForeground(colorBorder).Padding(0, 2)
	styleOverlay       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(1, 2)
	styleOverlayDanger = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorRed).Padding(1, 2)
	stylePanel         = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder).Padding(0, 1)
	stylePanelNoPad    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder)
)
