package main

import (
	"fmt"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
)

func (m *App) openProjectPicker(pkgName, version string) {
	items := make([]projectPickItem, 0, len(m.ctx.ParsedProjects))
	for _, p := range m.ctx.ParsedProjects {
		installed := false
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, pkgName) {
				installed = true
				break
			}
		}
		items = append(items, projectPickItem{
			project:   p,
			selected:  false,
			installed: installed,
		})
	}
	// Also include props projects that have packages.
	for _, p := range m.ctx.PropsProjects {
		installed := false
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, pkgName) {
				installed = true
				break
			}
		}
		items = append(items, projectPickItem{
			project:   p,
			selected:  false,
			installed: installed,
		})
	}
	// baseWidth=80, minWidth=60, maxMargin=4
	m.projectPick = projectPicker{
		sectionBase: sectionBase{app: m, baseWidth: 80, minWidth: 60, maxMargin: 4, active: true},
		pkgName:     pkgName,
		version:     version,
		items:       items,
	}
}

func (s *projectPicker) FooterKeys() []kv {
	return []kv{{"↑↓", "nav"}, {"space", "toggle"}, {"a", "all"}, {"enter", "confirm"}, {"esc", "cancel"}}
}

func (s *projectPicker) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		return nil
	case "]":
		s.Resize(4)
		return nil
	case "esc", "q":
		s.closeOverlay()
	case "up", "k":
		s.moveCursor(-1)
	case "down", "j":
		s.moveCursor(1)
	case "space":
		if s.cursor < len(s.items) && !s.items[s.cursor].installed {
			s.items[s.cursor].selected = !s.items[s.cursor].selected
		}
	case "a":
		// Toggle all: if any non-installed are unselected, select all; otherwise deselect all.
		anyUnselected := false
		for _, it := range s.items {
			if !it.installed && !it.selected {
				anyUnselected = true
				break
			}
		}
		for i := range s.items {
			if !s.items[i].installed {
				s.items[i].selected = anyUnselected
			}
		}
	case "enter":
		return s.confirmSelection()
	}
	return nil
}

func (s *projectPicker) moveCursor(delta int) {
	next := s.cursor + delta
	if next >= 0 && next < len(s.items) {
		s.cursor = next
	}
}

func (s *projectPicker) selectedCount() int {
	n := 0
	for _, it := range s.items {
		if it.selected && !it.installed {
			n++
		}
	}
	return n
}

func (s *projectPicker) confirmSelection() bubble_tea.Cmd {
	selected := make([]*ParsedProject, 0)
	for _, it := range s.items {
		if it.selected && !it.installed {
			selected = append(selected, it.project)
		}
	}
	s.closeOverlay()
	if len(selected) == 0 {
		return s.app.setStatus("No projects selected", true)
	}
	// Single project: use the full flow (may open location picker if ambiguous).
	if len(selected) == 1 {
		return s.app.openLocationPickerOrAdd(s.pkgName, s.version, selected[0])
	}
	// Multiple projects: pre-cache the fetched info so subsequent adds find it.
	if s.app.search.fetchedInfo != nil {
		if s.app.ctx.Results == nil {
			s.app.ctx.Results = make(map[string]nugetResult)
		}
		s.app.ctx.Results[s.pkgName] = nugetResult{
			pkg:    s.app.search.fetchedInfo,
			source: s.app.search.fetchedSource,
		}
	}
	var cmds []bubble_tea.Cmd
	for _, proj := range selected {
		target := defaultAddTarget(proj)
		cmds = append(cmds, s.app.addPackageToLocation(s.pkgName, s.version, proj, target))
	}
	return bubble_tea.Batch(cmds...)
}

// defaultAddTarget picks the best AddTarget for a project when adding a
// package in bulk (no interactive location picker). Prefers CPM, then
// the project file itself.
func defaultAddTarget(p *ParsedProject) AddTarget {
	for _, t := range p.AddTargets {
		if t.Kind == AddTargetCPM {
			return t
		}
	}
	if len(p.AddTargets) > 0 {
		return p.AddTargets[0]
	}
	return AddTarget{FilePath: p.FilePath, Kind: AddTargetProject}
}

func (s *projectPicker) Render() string {
	w := s.Width()
	innerW := w - 6 // border (2) + padding (2*2)

	lines := []string{
		styleAccentBold.Render("Add to which projects?"),
		styleSubtle.Render(s.pkgName + " " + s.version),
		"",
	}

	maxVisible := s.app.overlayHeight() - 8
	if maxVisible < 5 {
		maxVisible = 5
	}

	start := 0
	if s.cursor >= maxVisible {
		start = s.cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(s.items) {
		end = len(s.items)
	}

	for i := start; i < end; i++ {
		it := s.items[i]
		selected := i == s.cursor

		var check string
		nameStyle := styleText
		if it.installed {
			check = styleGreen.Render("✓ ")
			nameStyle = styleMuted
		} else if it.selected {
			check = styleAccent.Render("◉ ")
		} else {
			check = styleMuted.Render("○ ")
		}

		cursor := "  "
		if selected {
			cursor = styleAccent.Render("▶ ")
			if !it.installed {
				nameStyle = styleAccentBold
			}
		}

		// innerW-10 accounts for: cursor (2) + check (2) + suffix padding (6)
		name := truncate(it.project.FileName, innerW-10)
		suffix := ""
		if it.installed {
			// Show installed version.
			for ref := range it.project.Packages {
				if strings.EqualFold(ref.Name, s.pkgName) {
					suffix = styleMuted.Render(" " + ref.Version.String())
					break
				}
			}
		}

		lines = append(lines, cursor+check+nameStyle.Render(name)+suffix)
	}

	// Summary line
	count := s.selectedCount()
	lines = append(lines, "")
	lines = append(lines, styleMuted.Render(
		padRight("", 2)+styleSubtle.Render(
			strings.Repeat("─", innerW-2),
		),
	))
	if count > 0 {
		lines = append(lines, styleAccent.Render(
			padRight("", 2)+formatCount(count, "project", "projects")+" selected",
		))
	} else {
		lines = append(lines, styleMuted.Render(padRight("", 2)+"No projects selected"))
	}

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return s.centerOverlay(box)
}

func formatCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}
