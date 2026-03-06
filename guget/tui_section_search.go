package main

import (
	"sort"
	"strings"
	"time"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (m *Model) handleSearchKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		adjustOffset(&m.overlayWidthOffset, -4, m.ctx.Width, 30, m.ctx.Width-4)
		return nil
	case "]":
		adjustOffset(&m.overlayWidthOffset, 4, m.ctx.Width, 30, m.ctx.Width-4)
		return nil
	case "esc":
		m.overlayWidthOffset = 0
		m.search.active = false
		m.search.input.Blur()
		m.ctx.StatusLine = ""
		return nil

	case "up", "ctrl+p":
		if m.search.cursor > 0 {
			m.search.cursor--
		}
		return nil

	case "down", "ctrl+n":
		if m.search.cursor < len(m.search.results)-1 {
			m.search.cursor++
		}
		return nil

	case "enter":
		if m.search.fetchingVersion || len(m.search.results) == 0 {
			return nil
		}
		selected := m.search.results[m.search.cursor]
		// Check if already installed in this project
		if proj := m.selectedProject(); proj != nil {
			for ref := range proj.Packages {
				if strings.EqualFold(ref.Name, selected.ID) {
					m.overlayWidthOffset = 0
					m.search.active = false
					m.search.input.Blur()
					return m.setStatus("▲ "+selected.ID+" is in project", true)
				}
			}
		}
		// Use cached info if we already fetched this package (e.g. it's in another project).
		if cached, ok := m.ctx.Results[selected.ID]; ok && cached.pkg != nil {
			return func() bubble_tea.Msg {
				return packageFetchedMsg{info: cached.pkg, source: cached.source}
			}
		}
		m.search.fetchingVersion = true
		m.search.err = nil
		return m.fetchPackageCmd(selected.ID)
	}

	// Forward all other keys to the textinput
	var cmd bubble_tea.Cmd
	m.search.input, cmd = m.search.input.Update(msg)
	newQuery := m.search.input.Value()

	if newQuery == "" {
		m.search.results = nil
		m.search.loading = false
		m.search.debounceID++ // invalidate any in-flight debounce
		m.search.lastQuery = ""
		return cmd
	}

	if newQuery != m.search.lastQuery {
		m.search.lastQuery = newQuery
		m.search.loading = true
		return bubble_tea.Batch(cmd, m.searchDebounceCmd(newQuery))
	}
	return cmd
}

func (m *Model) searchDebounceCmd(query string) bubble_tea.Cmd {
	m.search.debounceID++
	id := m.search.debounceID
	return bubble_tea.Tick(500*time.Millisecond, func(t time.Time) bubble_tea.Msg {
		return searchDebounceMsg{id: id, query: query}
	})
}

func (m *Model) doSearchCmd(query string) bubble_tea.Cmd {
	services := m.ctx.NugetServices
	sourceMapping := m.ctx.SourceMapping
	return func() bubble_tea.Msg {
		type sourceResult struct {
			results []SearchResult
			err     error
			source  string
		}

		ch := make(chan sourceResult, len(services))
		for _, svc := range services {
			go func(svc *NugetService) {
				results, err := svc.Search(query, 50)
				ch <- sourceResult{results: results, err: err, source: svc.SourceName()}
			}(svc)
		}

		seen := NewSet[string]()
		var merged []SearchResult
		var lastErr error
		for range services {
			sr := <-ch
			if sr.err != nil {
				lastErr = sr.err
				logWarn("search source [%s] failed: %v", sr.source, sr.err)
				continue
			}
			for _, r := range sr.results {
				key := strings.ToLower(r.ID)
				if seen.Contains(key) {
					continue
				}
				// If source mapping is configured, only include results
				// whose package ID is allowed on the source that found it.
				if sourceMapping.IsConfigured() {
					allowed := sourceMapping.SourcesForPackage(r.ID)
					if len(allowed) > 0 {
						allowedSet := NewSet[string]()
						for _, k := range allowed {
							allowedSet.Add(strings.ToLower(k))
						}
						if !allowedSet.Contains(strings.ToLower(sr.source)) {
							continue
						}
					}
				}
				seen.Add(key)
				r.Source = sr.source
				merged = append(merged, r)
			}
		}
		if len(merged) == 0 && lastErr != nil {
			return searchResultsMsg{query: query, err: lastErr}
		}
		// Push exact matches to the top.
		lowerQ := strings.ToLower(query)
		sort.SliceStable(merged, func(i, j int) bool {
			iExact := strings.ToLower(merged[i].ID) == lowerQ
			jExact := strings.ToLower(merged[j].ID) == lowerQ
			return iExact && !jExact
		})
		return searchResultsMsg{results: merged, query: query}
	}
}

func (m *Model) fetchPackageCmd(id string) bubble_tea.Cmd {
	services := FilterServices(m.ctx.NugetServices, m.ctx.SourceMapping, id)
	return func() bubble_tea.Msg {
		var lastErr error
		for _, svc := range services {
			info, err := svc.SearchExact(id)
			if err == nil {
				return packageFetchedMsg{info: info, source: svc.SourceName()}
			}
			lastErr = err
		}
		return packageFetchedMsg{err: lastErr}
	}
}

func (m Model) renderSearchOverlay() string {
	w := clampW(90+m.overlayWidthOffset, 56, m.ctx.Width-4)
	innerW := w - 6 // border (2) + padding (2*2)

	var lines []string

	// Title row
	title := styleAccentBold.Render("Add Package")
	proj := m.selectedProject()
	projName := ""
	if proj != nil {
		projName = styleSubtle.
			Render("  " + truncate(proj.FileName, innerW-15))
	}
	lines = append(lines, title+projName)

	// Text input
	lines = append(lines, m.search.input.View())

	// Divider
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	// Column widths: prefix(2) + id(flex) + source(18) + version(12) + suffix
	const colSource = 18
	const colVer = 12
	colID := innerW - colSource - colVer - 2 // 2 for prefix
	if colID < 20 {
		colID = 20
	}

	// Body — scale with terminal height but cap at 20 rows.
	// 7 = 3 fixed content lines + 4 box chrome (border 2 + padding 2).
	maxVisible := m.overlayHeight() - 7
	if maxVisible < 5 {
		maxVisible = 5
	}
	if maxVisible > 20 {
		maxVisible = 20
	}
	switch {
	case m.search.fetchingVersion:
		lines = append(lines,
			m.ctx.Spinner.View()+" "+
				styleAccent.Render("Fetching package info…"))

	case m.search.loading:
		lines = append(lines,
			m.ctx.Spinner.View()+" "+
				styleSubtle.Render("Searching…"))

	case m.search.err != nil:
		lines = append(lines,
			styleRed.Render("✗ "+m.search.err.Error()))

	case len(m.search.results) == 0 && m.search.lastQuery != "":
		lines = append(lines,
			styleMuted.Render("No results found"))

	case len(m.search.results) == 0:
		lines = append(lines,
			styleMuted.Render("Type to search NuGet…"))

	default:
		installedVer := make(map[string]SemVer)
		if proj != nil {
			for ref := range proj.Packages {
				installedVer[strings.ToLower(ref.Name)] = ref.Version
			}
		}

		start := 0
		if m.search.cursor >= maxVisible {
			start = m.search.cursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(m.search.results) {
			end = len(m.search.results)
		}

		for i := start; i < end; i++ {
			r := m.search.results[i]
			selected := i == m.search.cursor

			prefix := "  "
			idStyle := styleText
			if selected {
				prefix = styleAccent.Render("▶ ")
				idStyle = styleAccentBold
			}

			pkgID := padRight(idStyle.Render(truncate(r.ID, colID-1)), colID)
			source := padRight(styleMuted.Render(truncate(r.Source, colSource-2)), colSource)

			icon := " "
			if iv, ok := installedVer[strings.ToLower(r.ID)]; ok {
				searchVer := ParseSemVer(r.Version)
				if searchVer.IsNewerThan(iv) {
					icon = styleYellow.Render("↑")
				} else if iv.IsNewerThan(searchVer) {
					icon = styleMuted.Render("↓")
				} else {
					icon = styleGreen.Render("✓")
				}
			}

			verText := truncate(r.Version, colVer-2)
			verPad := colVer - 2 - lipgloss.Width(verText)
			if verPad < 0 {
				verPad = 0
			}
			ver := icon + strings.Repeat(" ", verPad) + styleSubtle.Render(verText)

			line := prefix + pkgID + source + ver
			lines = append(lines, line)
		}
	}

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.ctx.Width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}
