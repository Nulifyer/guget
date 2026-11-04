package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
	text "github.com/nulifyer/guget/internal/ui/text"
)

func (m *App) openSearch() ui.Effect {
	if !m.canMutate() {
		return m.setStatus("Wait for the save or workspace reload to finish", false)
	}
	if m.search.cancel != nil {
		m.search.cancel()
	}
	m.search = packageSearch{
		sectionBase: sectionBase{app: m, baseWidth: 90, minWidth: 56, maxMargin: 4, requestID: m.nextRequestID()},
		input:       m.search.input,
	}
	m.search.input.Reset()
	m.search.active = true
	m.ctx.StatusLine = ""
	return m.search.input.Focus()
}

func (s *packageSearch) closeOverlay() {
	if s.cancel != nil {
		s.cancel()
	}
	s.input.Blur()
	s.sectionBase.closeOverlay()
}

func (s *packageSearch) FooterKeys() []kv {
	return []kv{{"↑↓", "nav"}, {"enter", "select"}, {"esc", "close"}}
}

func (s *packageSearch) HandleKey(msg ui.KeyEvent) ui.Effect {
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
		if s.loading || s.fetchingVersion || s.cursor < 0 || s.cursor >= len(s.results) {
			return nil
		}
		selected := s.results[s.cursor]
		s.fetchingVersion = true
		s.err = nil
		requestID := s.requestID
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
		if cached, ok := s.app.ctx.Results[selected.ID]; ok && cached.pkg != nil && strings.EqualFold(cached.source, selected.Source) {
			return func() ui.Event {
				return packageFetchedMsg{requestID: requestID, info: cached.pkg, source: cached.source}
			}
		}
		return s.fetchPackageCmd(selected.ID, selected.Source)
	}
	return s.handleInput(msg)
}

func (s *packageSearch) handleInput(msg ui.Event) ui.Effect {
	if s.fetchingVersion {
		return nil
	}

	// Forward typing and bracketed paste to the same query/debounce path.
	var cmd ui.Effect
	s.input, cmd = s.input.Update(msg)
	newQuery := s.input.Value()

	if newQuery == "" {
		if s.cancel != nil {
			s.cancel()
		}
		s.results = nil
		s.cursor = 0
		s.err = nil
		s.loading = false
		s.debounceID++ // invalidate any in-flight debounce
		s.lastQuery = ""
		return cmd
	}

	if newQuery != s.lastQuery {
		if s.cancel != nil {
			s.cancel()
		}
		s.results = nil
		s.cursor = 0
		s.err = nil
		s.lastQuery = newQuery
		s.loading = true
		return ui.Batch(cmd, s.debounceCmd(newQuery))
	}
	return cmd
}

func (s *packageSearch) debounceCmd(query string) ui.Effect {
	s.debounceID++
	id := s.debounceID
	requestID := s.requestID
	return ui.Tick(500*time.Millisecond, func(t time.Time) ui.Event {
		return searchDebounceMsg{requestID: requestID, id: id, query: query}
	})
}

func (s *packageSearch) doSearchCmd(query string) ui.Effect {
	services := s.app.ctx.NugetServices
	sourceMapping := s.app.ctx.SourceMapping
	requestID, id := s.requestID, s.debounceID
	ctx, cancel := context.WithCancel(s.app.lifecycle)
	s.cancel = cancel
	return func() ui.Event {
		defer cancel()
		if len(services) == 0 {
			return searchResultsMsg{requestID: requestID, id: id, query: query, err: fmt.Errorf("no metadata-capable NuGet sources are available")}
		}
		type sourceResult struct {
			results []SearchResult
			err     error
			source  string
			index   int
		}

		ch := make(chan sourceResult, len(services))
		for index, svc := range services {
			go func(index int, svc *NugetService) {
				results, err := svc.SearchContext(ctx, query, 50)
				ch <- sourceResult{results: results, err: err, source: svc.SourceName(), index: index}
			}(index, svc)
		}
		ordered := make([]sourceResult, len(services))
		for range services {
			sr := <-ch
			ordered[sr.index] = sr
		}

		seen := NewSet[string]()
		var merged []SearchResult
		var lastErr error
		for _, sr := range ordered {
			if sr.err != nil {
				lastErr = sr.err
				logWarn("search source [%s] failed: %v", sr.source, sr.err)
				continue
			}
			for _, r := range sr.results {
				key := strings.ToLower(r.ID) + "\x00" + strings.ToLower(sr.source)
				if seen.Contains(key) {
					continue
				}
				// If source mapping is configured, only include results
				// whose package ID is allowed on the source that found it.
				if sourceMapping.IsConfigured() {
					allowed := sourceMapping.SourcesForPackage(r.ID)
					if len(allowed) == 0 {
						continue
					}
					allowedSet := NewSet[string]()
					for _, k := range allowed {
						allowedSet.Add(strings.ToLower(k))
					}
					if !allowedSet.Contains(strings.ToLower(sr.source)) {
						continue
					}
				}
				seen.Add(key)
				r.Source = sr.source
				merged = append(merged, r)
			}
		}
		if len(merged) == 0 && lastErr != nil {
			return searchResultsMsg{requestID: requestID, id: id, query: query, err: lastErr}
		}
		// Push exact matches to the top.
		lowerQ := strings.ToLower(query)
		sort.SliceStable(merged, func(i, j int) bool {
			iExact := strings.ToLower(merged[i].ID) == lowerQ
			jExact := strings.ToLower(merged[j].ID) == lowerQ
			return iExact && !jExact
		})
		return searchResultsMsg{requestID: requestID, id: id, results: merged, query: query}
	}
}

func (s *packageSearch) fetchPackageCmd(id, source string) ui.Effect {
	eligible := FilterServices(s.app.ctx.NugetServices, s.app.ctx.SourceMapping, id)
	var services []*NugetService
	for _, service := range eligible {
		if strings.EqualFold(service.SourceName(), source) {
			services = append(services, service)
		}
	}
	requestID := s.requestID
	ctx, cancel := context.WithCancel(s.app.lifecycle)
	s.cancel = cancel
	return func() ui.Event {
		defer cancel()
		if len(services) == 0 {
			return packageFetchedMsg{requestID: requestID, err: fmt.Errorf("no metadata-capable NuGet source is available for %q", id)}
		}
		var lastErr error
		for _, svc := range services {
			info, err := svc.SearchExactContext(ctx, id)
			if err == nil {
				return packageFetchedMsg{requestID: requestID, info: info, source: svc.SourceName()}
			}
			lastErr = err
		}
		return packageFetchedMsg{requestID: requestID, err: lastErr}
	}
}

func (s *packageSearch) Render() string {
	w := s.Width()
	innerW := w - 6 // border (2) + padding (2*2)
	s.input.SetWidth(innerW)
	regions := []mouseRegion{overlayCloseRegion(w, s.HandleKey)}

	var lines []string

	// Title row
	proj := s.app.selectedProject()
	title := "Add Package"
	if proj != nil {
		title += "  " + proj.FileName
	}
	lines = append(lines, overlayTitleLine(title, innerW))

	// Text input
	lines = append(lines, s.input.View())
	regions = append(regions, mouseRegion{
		rect: mouseRect{x: overlayContentX, y: overlayContentY + 1, w: innerW, h: 1},
		click: func(ui.MouseEvent) ui.Effect {
			return s.input.Focus()
		},
	})

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
	if maxVisible < 1 {
		maxVisible = 1
	}
	if maxVisible > 20 {
		maxVisible = 20
	}
	switch {
	case s.fetchingVersion:
		lines = append(lines,
			s.app.ctx.Spinner.View()+" "+
				styleAccent.Render("Fetching package info..."))

	case s.loading:
		lines = append(lines,
			s.app.ctx.Spinner.View()+" "+
				styleSubtle.Render("Searching..."))

	case s.err != nil:
		errorLines := text.Wrap("✗ "+s.err.Error(), innerW)
		if len(errorLines) > maxVisible {
			errorLines = errorLines[:maxVisible]
			errorLines[maxVisible-1] = truncateStyled(errorLines[maxVisible-1], max(0, innerW-3)) + "..."
		}
		for _, line := range errorLines {
			lines = append(lines, styleRed.Render(line))
		}

	case len(s.results) == 0 && s.lastQuery != "":
		lines = append(lines,
			styleMuted.Render("No results found"))

	case len(s.results) == 0:
		lines = append(lines,
			styleMuted.Render("Type to search NuGet..."))

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
			verPad := colVer - 2 - style.Width(verText)
			if verPad < 0 {
				verPad = 0
			}
			ver := icon + strings.Repeat(" ", verPad) + styleSubtle.Render(verText)

			line := prefix + pkgID + source + ver
			lines = append(lines, line)
			queryID := s.debounceID
			resultID, sourceID, version := r.ID, r.Source, r.Version
			regions = append(regions, mouseRegion{
				rect: mouseRect{x: overlayContentX, y: overlayContentY + len(lines) - 1, w: innerW, h: 1},
				click: func(ui.MouseEvent) ui.Effect {
					if s.debounceID != queryID {
						return nil
					}
					for index, current := range s.results {
						if strings.EqualFold(current.ID, resultID) && strings.EqualFold(current.Source, sourceID) && current.Version == version {
							s.cursor = index
							return s.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEnter}))
						}
					}
					return nil
				},
			})
		}
	}

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))
	regions = append(regions, mouseRegion{
		rect:  mouseRect{x: 1, y: 1, w: w - 2, h: style.Height(box) - 2},
		wheel: verticalWheelHandler(s.HandleKey),
	})

	return s.centerOverlayInteractive(box, regions)
}
