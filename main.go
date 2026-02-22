package main

// A simple Bubble Tea TUI with a left column list and right detail pane.

import (
	"fmt"
	"log"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Item struct {
	Name   string
	Active bool
}

type model struct {
	items  []Item
	cursor int
}

func main() {
	items := []Item{
		{Name: "Full Solution", Active: false},
		{Name: "Project A", Active: false},
		{Name: "Project B", Active: false},
		{Name: "Package C", Active: false},
	}

	m := &model{items: items, cursor: 0}
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
			return m, nil
		case "enter":
			// If Full Solution (index 0) is selected, toggle all other items.
			if m.cursor == 0 {
				allActive := true
				for i := 1; i < len(m.items); i++ {
					if !m.items[i].Active {
						allActive = false
						break
					}
				}
				newState := !allActive
				for i := 1; i < len(m.items); i++ {
					m.items[i].Active = newState
				}
			} else {
				m.items[m.cursor].Active = !m.items[m.cursor].Active
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *model) View() string {
	leftWidth := 30

	// Build left column (list)
	var leftLines []string
	leftLines = append(leftLines, "Left")
	leftLines = append(leftLines, strings.Repeat("-", leftWidth-1))
	for i, it := range m.items {
		marker := "  "
		if i == m.cursor {
			marker = "> "
		}
		status := "[ ]"
		if it.Active {
			status = "[x]"
		}
		leftLines = append(leftLines, fmt.Sprintf("%s %s %s", marker, status, it.Name))
	}

	// Build right column (detail)
	var rightLines []string
	if m.cursor == 0 {
		rightLines = append(rightLines, "Full Solution Manager")
		rightLines = append(rightLines, "----------------------")
		for i := 1; i < len(m.items); i++ {
			it := m.items[i]
			status := "Inactive"
			if it.Active {
				status = "Active"
			}
			rightLines = append(rightLines, fmt.Sprintf("%s: %s", it.Name, status))
		}
		rightLines = append(rightLines, "")
		rightLines = append(rightLines, "Press Enter to toggle all items on/off.")
		rightLines = append(rightLines, "Use ↑/↓ or j/k to move. Press q to quit.")
	} else {
		it := m.items[m.cursor]
		rightLines = append(rightLines, fmt.Sprintf("Details for %s", it.Name))
		rightLines = append(rightLines, strings.Repeat("-", 30))
		status := "Inactive"
		if it.Active {
			status = "Active"
		}
		rightLines = append(rightLines, fmt.Sprintf("Status: %s", status))
		rightLines = append(rightLines, "")
		rightLines = append(rightLines, "Press Enter to toggle this item.")
		rightLines = append(rightLines, "Use ↑/↓ or j/k to move. Press q to quit.")
	}

	// Combine columns line-by-line
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}

	var b strings.Builder
	for i := 0; i < maxLines; i++ {
		var l, r string
		if i < len(leftLines) {
			l = padRight(leftLines[i], leftWidth)
		} else {
			l = padRight("", leftWidth)
		}
		if i < len(rightLines) {
			r = rightLines[i]
		} else {
			r = ""
		}
		b.WriteString(l)
		b.WriteString(" | ")
		b.WriteString(r)
		if i < maxLines-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}
