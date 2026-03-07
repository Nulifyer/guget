package main

import (
	"fmt"
	"strings"
	"time"

	bubbles_spinner "charm.land/bubbles/v2/spinner"
	bubbles_textinpute "charm.land/bubbles/v2/textinput"
	bubbles_viewport "charm.land/bubbles/v2/viewport"
	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

const (
	logPanelLines       = 6
	logPanelOuterHeight = logPanelLines + 3 // bottom border(1) + title(1) + divider(1)
)

// layoutWidth returns the effective width for the main content area.
func (m *App) layoutWidth() int {
	const minLayoutWidth = 80
	if m.ctx.Width < minLayoutWidth {
		return minLayoutWidth
	}
	return m.ctx.Width
}

type App struct {
	ctx *AppContext

	focus focusPanel

	projects projectPanel
	packages packagePanel
	detail   detailPanel
	log      logPanel

	picker        versionPicker
	search        packageSearch
	confirmRemove confirmRemove
	confirmUpdate confirmUpdate
	locationPick  locationPicker
	projectPick   projectPicker
	depTree       depTreeOverlay
	releaseNotes  releaseNotesOverlay
	sources       sourcesOverlay
	help          helpOverlay

	resizeDebounceID int
}

// overlays returns all overlay sections in priority order (highest first).
// Used for generic key dispatch and rendering.
func (m *App) overlays() []Overlay {
	return []Overlay{
		&m.depTree, &m.releaseNotes, &m.sources, &m.help,
		&m.search, &m.picker, &m.locationPick, &m.projectPick,
		&m.confirmRemove, &m.confirmUpdate,
	}
}

func (m *App) anyOverlayActive() bool {
	for _, o := range m.overlays() {
		if o.IsActive() {
			return true
		}
	}
	return false
}

func NewApp(parsedProjects []*ParsedProject, propsProjects []*ParsedProject, nugetServices []*NugetService, sources []NugetSource, sourceMapping *PackageSourceMapping, initialLogLines []string, loadingTotal int, flags BuiltFlags) *App {
	sp := bubbles_spinner.New()
	sp.Spinner = bubbles_spinner.Dot
	sp.Style = styleAccent

	projItems := []projectItem{
		{name: "All Projects", project: nil},
	}
	for _, p := range parsedProjects {
		projItems = append(projItems, projectItem{name: p.FileName, project: p})
	}
	for _, p := range propsProjects {
		projItems = append(projItems, projectItem{name: p.FileName, project: p})
	}

	dv := bubbles_viewport.New(bubbles_viewport.WithWidth(40), bubbles_viewport.WithHeight(20))
	lv := bubbles_viewport.New(bubbles_viewport.WithWidth(80), bubbles_viewport.WithHeight(logPanelLines))

	ti := bubbles_textinpute.New()
	ti.Placeholder = "Type a package name..."
	ti.CharLimit = 100
	ti.SetWidth(44)

	sortMode, sortDir := parseSortFlag(flags.SortBy)

	ctx := &AppContext{
		ParsedProjects: parsedProjects,
		PropsProjects:  propsProjects,
		NugetServices:  nugetServices,
		Sources:        sources,
		SourceMapping:  sourceMapping,
		Loading:        loadingTotal > 0,
		LoadingTotal:   loadingTotal,
		Spinner:        sp,
		Results:        make(map[string]nugetResult, loadingTotal),
		LogLines:       initialLogLines,
	}

	m := &App{
		ctx: ctx,
		projects: projectPanel{
			sectionBase: sectionBase{baseWidth: 30, minWidth: 10},
			items:       projItems,
		},
		packages: packagePanel{
			sortMode: sortMode,
			sortDir:  sortDir,
		},
		detail: detailPanel{
			sectionBase: sectionBase{baseWidth: 50, minWidth: 10},
			vp:          dv,
		},
		log: logPanel{vp: lv},
		search: packageSearch{
			sectionBase: sectionBase{baseWidth: 90, minWidth: 56, maxMargin: 4},
			input:       ti,
		},
		sources: sourcesOverlay{
			sectionBase: sectionBase{baseWidth: 90, minWidth: 40, maxMargin: 4},
		},
		help: helpOverlay{
			sectionBase: sectionBase{basePct: 60, minWidth: 56, maxMargin: 4},
			vp:          bubbles_viewport.New(bubbles_viewport.WithWidth(60), bubbles_viewport.WithHeight(20)),
		},
	}
	// Set back-pointers so sections can access the App.
	m.projects.app = m
	m.detail.app = m
	m.search.app = m
	m.sources.app = m
	m.help.app = m
	return m
}

func (m *App) Init() bubble_tea.Cmd {
	return m.ctx.Spinner.Tick
}

func (m *App) Update(msg bubble_tea.Msg) (bubble_tea.Model, bubble_tea.Cmd) {
	var cmds []bubble_tea.Cmd

	switch msg := msg.(type) {

	case bubble_tea.WindowSizeMsg:
		m.ctx.Width = msg.Width
		m.ctx.Height = msg.Height
		// relayout() is cheap (viewport dimension setters only) — call immediately
		// so panels always render at the correct size. Expensive content rebuilds
		// (help, release notes) are debounced.
		m.relayout()
		m.resizeDebounceID++
		id := m.resizeDebounceID
		cmds = append(cmds, bubble_tea.Tick(50*time.Millisecond, func(t time.Time) bubble_tea.Msg {
			return resizeDebounceMsg{id: id}
		}))

	case resizeDebounceMsg:
		if msg.id == m.resizeDebounceID {
			if m.help.active {
				m.help.refreshView()
			}
			if m.releaseNotes.active {
				m.releaseNotes.resizeViewport()
			}
		}

	case bubbles_spinner.TickMsg:
		var cmd bubble_tea.Cmd
		m.ctx.Spinner, cmd = m.ctx.Spinner.Update(msg)
		cmds = append(cmds, cmd)

	case packageReadyMsg:
		m.ctx.Results[msg.name] = msg.result
		m.ctx.LoadingDone++
		if m.ctx.LoadingDone >= m.ctx.LoadingTotal {
			m.ctx.Loading = false
			m.refreshDetail()
		}
		m.rebuildPackageRows()

	case writeResultMsg:
		if msg.err != nil {
			cmds = append(cmds, m.setStatus("▲ Save failed: "+msg.err.Error(), true))
		} else {
			status := "✓ Saved"
			if msg.written > 0 && msg.skipped > 0 {
				status = fmt.Sprintf("✓ Saved %d, %d locked", msg.written, msg.skipped)
			} else if msg.skipped > 0 {
				status = fmt.Sprintf("🔒 %d skipped (version locked)", msg.skipped)
			}
			cmds = append(cmds, m.setStatus(status, false))
		}

	case restoreResultMsg:
		m.ctx.Restoring = false
		if msg.err != nil {
			logError("restore failed: %v", msg.err)
			cmds = append(cmds, m.setStatus("✗ Restore failed (see logs)", true))
		} else {
			cmds = append(cmds, m.setStatus("✓ Restore complete", false))
		}

	case searchDebounceMsg:
		if msg.id == m.search.debounceID && msg.query != "" {
			m.search.loading = true
			cmds = append(cmds, m.search.doSearchCmd(msg.query))
		}

	case searchResultsMsg:
		if msg.query == m.search.lastQuery {
			m.search.loading = false
			m.search.err = msg.err
			if msg.err == nil {
				m.search.results = msg.results
			}
			m.search.cursor = 0
		}

	case packageFetchedMsg:
		m.search.fetchingVersion = false
		if msg.err != nil {
			m.search.err = msg.err
			break
		}
		m.search.fetchedInfo = msg.info
		m.search.fetchedSource = msg.source
		m.search.closeOverlay()
		m.search.input.Blur()
		proj := m.selectedProject()
		if proj != nil {
			m.picker = newVersionPicker(m, msg.info.ID, msg.info.Versions, proj.TargetFrameworks, proj, true)
		} else {
			// "All Projects" — collect union of all target frameworks.
			allTFMs := NewSet[TargetFramework]()
			for _, p := range m.ctx.ParsedProjects {
				for fw := range p.TargetFrameworks {
					allTFMs.Add(fw)
				}
			}
			m.picker = newVersionPicker(m, msg.info.ID, msg.info.Versions, allTFMs, nil, true)
		}

	case logLineMsg:
		m.ctx.LogLines = append(m.ctx.LogLines, msg.line)
		m.updateLogView()

	case releaseListReadyMsg:
		m.releaseNotes.ghLoading = false
		if msg.owner != "" {
			m.releaseNotes.ghOwner = msg.owner
			m.releaseNotes.ghRepo = msg.repo
		}
		if msg.err != nil {
			m.releaseNotes.ghErr = msg.err
			// Auto-switch to NuSpec if GitHub failed and NuSpec is available.
			if m.releaseNotes.nsAvailable || len(m.releaseNotes.nsVersions) > 0 {
				m.releaseNotes.activeTab = tabNuSpec
			}
			m.releaseNotes.updateViewportContent()
			break
		}
		m.releaseNotes.ghReleases = msg.releases
		m.releaseNotes.ghAvailable = len(msg.releases) > 0
		if len(msg.releases) == 0 {
			if m.releaseNotes.nsAvailable || len(m.releaseNotes.nsVersions) > 0 {
				m.releaseNotes.activeTab = tabNuSpec
			}
			m.releaseNotes.updateViewportContent()
			break
		}
		m.releaseNotes.ghCursor = 0
		m.releaseNotes.ghLoading = true
		cmds = append(cmds, m.releaseNotes.fetchReleaseNotesCmd(msg.releases[0]))

	case releaseNotesReadyMsg:
		m.releaseNotes.ghLoading = false
		if msg.err != nil {
			m.releaseNotes.ghNotes = ""
		} else {
			m.releaseNotes.ghNotes = msg.body
			m.releaseNotes.ghNotesURL = msg.htmlURL
		}
		m.releaseNotes.updateViewportContent()

	case nuspecVersionNotesReadyMsg:
		m.releaseNotes.nsLoading = false
		if msg.notes != "" {
			m.releaseNotes.nsAvailable = true
		}
		m.releaseNotes.nsNotes = msg.notes
		m.releaseNotes.updateViewportContent()

	case depTreeReadyMsg:
		m.depTree.loading = false
		m.depTree.err = msg.err
		if msg.err == nil {
			m.depTree.content = m.depTree.renderParsedDotnetList(parseDotnetListOutput(msg.content))
		}
		m.depTree.vp.SetContent(m.depTree.buildContent())

	case bubble_tea.KeyMsg:
		handled := false
		for _, o := range m.overlays() {
			if o.IsActive() {
				cmds = append(cmds, o.HandleKey(msg))
				handled = true
				break
			}
		}
		if !handled {
			cmds = append(cmds, m.handleKey(msg))
		}
	}

	if !m.anyOverlayActive() {
		switch m.focus {
		case focusProjects:
			if keyMsg, ok := msg.(bubble_tea.KeyMsg); ok {
				switch keyMsg.String() {
				case "up", "k":
					if m.projects.cursor > 0 {
						m.projects.cursor--
						m.clampProjectOffset()
						m.packages.cursor = 0
						m.packages.scroll = 0
						m.rebuildPackageRows()
						m.refreshDetail()
					}
				case "down", "j":
					if m.projects.cursor < len(m.projects.items)-1 {
						m.projects.cursor++
						m.clampProjectOffset()
						m.packages.cursor = 0
						m.packages.scroll = 0
						m.rebuildPackageRows()
						m.refreshDetail()
					}
				}
			}
		case focusDetail:
			if keyMsg, ok := msg.(bubble_tea.KeyMsg); ok && (keyMsg.String() == "v" || keyMsg.String() == "n") {
				// handled by handleKey above
				if keyMsg.String() == "v" {
					m.openVersionPicker()
				}
			} else {
				var cmd bubble_tea.Cmd
				m.detail.vp, cmd = m.detail.vp.Update(msg)
				cmds = append(cmds, cmd)
			}
		case focusLog:
			if m.ctx.ShowLogs {
				var cmd bubble_tea.Cmd
				m.log.vp, cmd = m.log.vp.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, bubble_tea.Batch(cmds...)
}

func (m *App) setStatus(text string, isErr bool) bubble_tea.Cmd {
	// Strip newlines and truncate to keep the status on a single line.
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	maxW := m.layoutWidth() - 6 // padding + border
	if lipgloss.Width(text) > maxW && maxW > 3 {
		text = text[:maxW-3] + "..."
	}
	m.ctx.StatusLine = text
	m.ctx.StatusIsErr = isErr
	return nil
}

func (m *App) handleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		return bubble_tea.Quit

	case "tab":
		if m.ctx.ShowLogs {
			m.focus = (m.focus + 1) % 4
		} else {
			m.focus = (m.focus + 1) % 3
		}

	case "shift+tab":
		if m.ctx.ShowLogs {
			m.focus = (m.focus + 3) % 4
		} else {
			m.focus = (m.focus + 2) % 3
		}

	case "l":
		m.ctx.ShowLogs = !m.ctx.ShowLogs
		if !m.ctx.ShowLogs && m.focus == focusLog {
			m.focus = focusPackages
		}
		if m.ctx.ShowLogs {
			m.updateLogView()
		}
		m.relayout()

	case "s":
		m.sources.active = !m.sources.active
		if m.sources.active {
			m.ctx.StatusLine = ""
		}

	case "?":
		m.help.active = !m.help.active
		if m.help.active {
			m.ctx.StatusLine = ""
			m.help.refreshView()
		}

	case "up", "k":
		if m.focus == focusPackages && m.packages.cursor > 0 {
			m.packages.cursor--
			m.clampOffset()
			m.refreshDetail()
		}

	case "down", "j":
		if m.focus == focusPackages && m.packages.cursor < len(m.packages.rows)-1 {
			m.packages.cursor++
			m.clampOffset()
			m.refreshDetail()
		}

	case "u":
		if m.focus == focusPackages {
			return m.updatePackage(false, scopeSelected)
		}

	case "U":
		if m.focus == focusPackages {
			return m.updatePackage(false, scopeAll)
		}

	case "a":
		if m.focus == focusPackages {
			return m.updatePackage(true, scopeSelected)
		}

	case "A":
		if m.focus == focusPackages {
			return m.updatePackage(true, scopeAll)
		}

	case "v":
		if m.focus == focusPackages {
			m.openVersionPicker()
		}

	case "r":
		if !m.ctx.Restoring {
			return m.restore(scopeSelected)
		}

	case "R":
		if !m.ctx.Restoring {
			return m.restore(scopeAll)
		}

	case "n":
		if m.focus == focusPackages || m.focus == focusDetail {
			return m.openReleaseNotes()
		}

	case "t":
		if m.focus == focusPackages {
			return m.openDepTree()
		}

	case "T":
		return m.openTransitiveDepTree()

	case "o":
		if m.focus == focusPackages {
			m.packages.sortMode = m.packages.sortMode.next()
			m.packages.sortDir = m.packages.sortMode.defaultDir()
			m.packages.cursor = 0
			m.packages.scroll = 0
			m.rebuildPackageRows()
			m.refreshDetail()
		}

	case "O":
		if m.focus == focusPackages {
			m.packages.sortDir = !m.packages.sortDir
			m.packages.cursor = 0
			m.packages.scroll = 0
			m.rebuildPackageRows()
			m.refreshDetail()
		}

	case "d":
		if m.focus == focusPackages && m.packages.cursor < len(m.packages.rows) {
			m.confirmRemove = newConfirmRemove(m, m.packages.rows[m.packages.cursor].ref.Name)
			m.ctx.StatusLine = ""
		}

	case "/":
		return m.openSearch()

	case "[":
		m.resizeFocused(-2)
		m.relayout()
		return nil
	case "]":
		m.resizeFocused(2)
		m.relayout()
		return nil

	case "enter":
		if m.focus == focusProjects {
			m.focus = focusPackages
		}
	}
	return nil
}

func (m *App) resizeFocused(delta int) {
	const (
		borders = 6
		minW    = 10
	)
	lw := m.layoutWidth()
	switch m.focus {
	case focusProjects:
		maxW := lw - (m.detail.baseWidth + m.detail.widthOffset) - borders - minW
		adjustOffset(&m.projects.widthOffset, delta, m.projects.baseWidth, minW, maxW)
	case focusPackages:
		maxW := lw - (m.projects.baseWidth + m.projects.widthOffset) - borders - minW
		adjustOffset(&m.detail.widthOffset, -delta, m.detail.baseWidth, minW, maxW)
		m.refreshDetail()
	case focusDetail:
		maxW := lw - (m.projects.baseWidth + m.projects.widthOffset) - borders - minW
		adjustOffset(&m.detail.widthOffset, delta, m.detail.baseWidth, minW, maxW)
		m.refreshDetail()
	}
}

func (m *App) selectedProject() *ParsedProject {
	if m.projects.cursor >= 0 && m.projects.cursor < len(m.projects.items) {
		return m.projects.items[m.projects.cursor].project
	}
	return nil
}

func (m *App) rowByName(name string) *packageRow {
	for i := range m.packages.rows {
		if strings.EqualFold(m.packages.rows[i].ref.Name, name) {
			return &m.packages.rows[i]
		}
	}
	return nil
}

func (m *App) View() bubble_tea.View {
	v := bubble_tea.NewView("")
	v.AltScreen = true

	if m.ctx.Width == 0 {
		v.SetContent("Initializing...")
		return v
	}

	if m.ctx.Loading {
		v.SetContent(lipgloss.Place(m.ctx.Width, m.ctx.Height,
			lipgloss.Center, lipgloss.Center,
			styleAccent.Render(
				fmt.Sprintf("%s Loading packages... (%d/%d)", m.ctx.Spinner.View(), m.ctx.LoadingDone, m.ctx.LoadingTotal),
			),
		))
		return v
	}

	footer := m.renderFooter()
	footerH := lipgloss.Height(footer)

	// Overlay views — render in the space above the footer.
	var overlay string
	for _, o := range m.overlays() {
		if o.IsActive() {
			overlay = o.Render()
			break
		}
	}

	if overlay != "" {
		// Overlay renderers use overlayHeight() to fit above the footer.
		// Safety trim in case the overlay output still exceeds the space.
		overlayLines := strings.Split(overlay, "\n")
		maxLines := m.ctx.Height - footerH
		if len(overlayLines) > maxLines {
			overlayLines = overlayLines[:maxLines]
		}
		trimmed := strings.Join(overlayLines, "\n")
		v.SetContent(trimmed + "\n" + footer)
		return v
	}

	leftW, midW, rightW := m.panelWidths()

	left := m.renderProjectPanel(leftW)
	mid := m.renderPackagePanel(midW)
	right := m.renderDetailPanel(rightW)

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, mid, right)

	parts := []string{body}
	if m.ctx.ShowLogs {
		parts = append(parts, m.renderLogPanel())
	}

	// Safety trim: ensure body + log never exceed the space above the footer.
	// alignTextVertical does NOT truncate, so panels can render taller than
	// Height if content overflows. Clamp here using the actual measured
	// footer height — the same pattern that makes overlays work.
	joined := lipgloss.JoinVertical(lipgloss.Left, parts...)
	bodyLines := strings.Split(joined, "\n")
	maxBodyLines := m.ctx.Height - footerH
	if len(bodyLines) > maxBodyLines {
		bodyLines = bodyLines[:maxBodyLines]
	}
	content := strings.Join(bodyLines, "\n") + "\n" + footer

	v.SetContent(content)
	return v
}
