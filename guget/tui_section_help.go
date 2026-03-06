package main

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

func (m *Model) refreshHelpView() {
	type section struct {
		title string
		rows  [][2]string // [key, description]
	}
	sections := []section{
		{
			title: "Navigation",
			rows: [][2]string{
				{"tab / shift+tab", "cycle focus between panels"},
				{"↑ / ↓  or  j / k", "move up / down in list"},
				{"enter", "switch focus to packages panel"},
			},
		},
		{
			title: "Package actions  (packages panel)",
			rows: [][2]string{
				{"u", "update to latest compatible (this project)"},
				{"U", "update to latest compatible (all projects)"},
				{"a", "update to latest stable (this project)"},
				{"A", "update to latest stable (all projects)"},
				{"v", "pick a specific version from the list"},
				{"d", "delete selected package from project"},
				{"t", "show declared dependency tree for package"},
				{"n", "view release notes (GitHub or NuGet)"},
				{"o", "cycle sort order"},
				{"O", "change sort direction"},
			},
		},
		{
			title: "Project actions",
			rows: [][2]string{
				{"r", "run dotnet restore (selected project)"},
				{"R", "run dotnet restore (all projects)"},
				{"T", "show full transitive dependency tree"},
				{"/", "search NuGet and add a package"},
			},
		},
		{
			title: "Version picker  (v)",
			rows: [][2]string{
				{"↑ / ↓  or  j / k", "move cursor"},
				{"u", "apply version (this project)"},
				{"U", "apply version (all projects)"},
				{"enter", "apply version"},
				{"esc / q", "close picker"},
			},
		},
		{
			title: "Dependency tree  (t / T)",
			rows: [][2]string{
				{"↑ / ↓  or  j / k", "scroll content"},
				{"esc", "close panel"},
			},
		},
		{
			title: "Release notes  (n)",
			rows: [][2]string{
				{"tab", "switch focus between releases and notes"},
				{"↑ / ↓  or  j / k", "navigate releases (left) or scroll notes (right)"},
				{"esc", "close panel"},
			},
		},
		{
			title: "View toggles",
			rows: [][2]string{
				{"[ / ]", "resize focused panel"},
				{"l", "toggle log panel"},
				{"s", "toggle sources panel"},
				{"?", "toggle this help"},
				{"esc / q / ctrl+c", "quit"},
			},
		},
	}

	keyStyle := styleAccentBold
	titleStyle := styleAccentBold
	descStyle := styleSubtle
	dimStyle := styleBorder

	// Compute key column width across all sections.
	maxKeyW := 0
	for _, sec := range sections {
		for _, row := range sec.rows {
			if w := lipgloss.Width(row[0]); w > maxKeyW {
				maxKeyW = w
			}
		}
	}
	maxKeyW += 2

	var lines []string
	lines = append(lines, styleAccentBold.Render("Keybindings"))

	for _, sec := range sections {
		lines = append(lines, "")
		lines = append(lines, titleStyle.Render(sec.title))
		lines = append(lines, dimStyle.Render(strings.Repeat("─", maxKeyW+32)))
		for _, row := range sec.rows {
			k := keyStyle.Render(padRight(row[0], maxKeyW))
			d := descStyle.Render(row[1])
			lines = append(lines, k+"  "+d)
		}
	}
	w := clampW(m.ctx.Width*60/100+m.overlayWidthOffset, 56, m.ctx.Width-4)

	content := strings.Join(lines, "\n")
	// Available height for content inside the overlay box:
	// overlay area - border (2) - padding (2) - margin (2)
	maxH := m.overlayHeight() - 6
	if maxH < 8 {
		maxH = 8
	}

	m.helpView.SetWidth(w - 4)
	m.helpView.SetHeight(maxH)
	m.helpView.SetContent(content)
}

func (m Model) renderHelpOverlay() string {
	w := clampW(m.ctx.Width*60/100+m.overlayWidthOffset, 56, m.ctx.Width-4)

	content := m.helpView.View()

	box := styleOverlay.
		Width(w).
		Render(content)

	return lipgloss.Place(m.ctx.Width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}
