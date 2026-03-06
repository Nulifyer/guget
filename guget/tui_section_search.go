package main

import (
	"sort"
	"strings"
	"time"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (m *App) openSearch() bubble_tea.Cmd {
	m.search = packageSearch{
		sectionBase: sectionBase{app: m, baseWidth: 90, minWidth: 56, maxMargin: 4},
		input:       m.search.input,
	}
	m.search.input.Reset()
	m.search.active = true
	m.ctx.StatusLine = ""
	return m.search.input.Focus()
}

func (s *packageSearch) FooterKeys() []kv {
	return []kv{{"↑↓", "nav"}, {"enter", "select"}, {"esc", "close"}}
}

func (s *packageSearch) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		return nil
	case "]":
		s.Resize(4)
		return nil
	case "esc":
		s.closeOverlay()
		s.input.Blur()
		return nil

	case "up", "ctrl+p":
		if s.cursor > 0 {
			s.cursor--
		}
		return nil

	case "down", "ctrl+n":
		if s.cursor < len(s.results)-1 {
			s.cursor++
		}
		return nil

	case "enter":
		if s.fetchingVersion || len(s.results) == 0 {
			return nil
		}
		selected := s.results[s.cursor]
		// Check if already installed in this project
		if proj := s.app.selectedProject(); proj != nil {
			for ref := range proj.Packages {
				if strings.EqualFold(ref.Name, selected.ID) {
					s.closeOverlay()
					s.input.Blur()
					return s.app.setStatus("▲ "+selected.ID+" is in project", true)
				}
			}
		}
		// Use cached info if we already fetched this package (e.g. it's in another project).
		if cached, ok := s.app.ctx.Results[selected.ID]; ok && cached.pkg != nil {
			return func() bubble_tea.Msg {
				return packageFetchedMsg{info: cached.pkg, source: cached.source}
			}
		}
		s.fetchingVersion = true
		s.err = nil
		return s.fetchPackageCmd(selected.ID)
	}

	// Forward all other keys to the textinput
	var cmd bubble_tea.Cmd
	s.input, cmd = s.input.Update(msg)
	newQuery := s.input.Value()

	if newQuery == "" {
		s.results = nil
		s.loading = false
		s.debounceID++ // invalidate any in-flight debounce
		s.lastQuery = ""
		return cmd
	}

	if newQuery != s.lastQuery {
		s.lastQuery = newQuery
		s.loading = true
		return bubble_tea.Batch(cmd, s.debounceCmd(newQuery))
	}
	return cmd
}

func (s *packageSearch) debounceCmd(query string) bubble_tea.Cmd {
	s.debounceID++
	id := s.debounceID
	return bubble_tea.Tick(500*time.Millisecond, func(t time.Time) bubble_tea.Msg {
		return searchDebounceMsg{id: id, query: query}
	})
}

func (s *packageSearch) doSearchCmd(query string) bubble_tea.Cmd {
	services := s.app.ctx.NugetServices
	sourceMapping := s.app.ctx.SourceMapping
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

func (s *packageSearch) fetchPackageCmd(id string) bubble_tea.Cmd {
	services := FilterServices(s.app.ctx.NugetServices, s.app.ctx.SourceMapping, id)
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

func (s *packageSearch) Render() string {
	w := s.Width()
	innerW := w - 6 // border (2) + padding (2*2)

	var lines []string

	// Title row
	title := styleAccentBold.Render("Add Package")
	proj := s.app.selectedProject()
	projName := ""
	if proj != nil {
		projName = styleSubtle.
			Render("  " + truncate(proj.FileName, innerW-15))
	}
	lines = append(lines, title+projName)

	// Text input
	lines = append(lines, s.input.View())

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
	maxVisible := s.app.overlayHeight() - 7
	if maxVisible < 5 {
		maxVisible = 5
	}
	if maxVisible > 20 {
		maxVisible = 20
	}
	switch {
	case s.fetchingVersion:
		lines = append(lines,
			s.app.ctx.Spinner.View()+" "+
				styleAccent.Render("Fetching package info…"))

	case s.loading:
		lines = append(lines,
			s.app.ctx.Spinner.View()+" "+
				styleSubtle.Render("Searching…"))

	case s.err != nil:
		lines = append(lines,
			styleRed.Render("✗ "+s.err.Error()))

	case len(s.results) == 0 && s.lastQuery != "":
		lines = append(lines,
			styleMuted.Render("No results found"))

	case len(s.results) == 0:
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
		if s.cursor >= maxVisible {
			start = s.cursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(s.results) {
			end = len(s.results)
		}

		for i := start; i < end; i++ {
			r := s.results[i]
			selected := i == s.cursor

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

	return s.centerOverlay(box)
}
