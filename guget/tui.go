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
func (m *Model) layoutWidth() int {
	const minLayoutWidth = 80
	if m.ctx.Width < minLayoutWidth {
		return minLayoutWidth
	}
	return m.ctx.Width
}

type Model struct {
	ctx *AppContext

	focus focusPanel

	projectItems  []projectItem
	projectCursor int
	projectOffset int

	packageRows     []packageRow
	packageCursor   int
	packageOffset   int
	packageSortMode packageSortMode
	packageSortDir  bool

	detailView bubbles_viewport.Model

	picker        versionPicker
	search        packageSearch
	confirmRemove confirmRemove
	confirmUpdate confirmUpdate
	locationPick  locationPicker
	depTree       depTreeOverlay
	releaseNotes  releaseNotesOverlay

	showSources bool
	showHelp    bool
	helpView    bubbles_viewport.Model

	logView bubbles_viewport.Model

	resizeDebounceID int

	leftWidthOffset    int // projects panel width offset ([ / ])
	rightWidthOffset   int // detail panel width offset ([ / ])
	overlayWidthOffset int // active overlay width offset, reset on close
}

func NewModel(parsedProjects []*ParsedProject, propsProjects []*ParsedProject, nugetServices []*NugetService, sources []NugetSource, sourceMapping *PackageSourceMapping, initialLogLines []string, loadingTotal int, flags BuiltFlags) Model {
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
	ti.Placeholder = "Type a package name…"
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

	m := Model{
		ctx:             ctx,
		projectItems:    projItems,
		detailView:      dv,
		search:          packageSearch{input: ti},
		logView:         lv,
		packageSortMode: sortMode,
		packageSortDir:  sortDir,
	}
	return m
}

func (m Model) Init() bubble_tea.Cmd {
	return m.ctx.Spinner.Tick
}

func (m Model) Update(msg bubble_tea.Msg) (bubble_tea.Model, bubble_tea.Cmd) {
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
			if m.showHelp {
				m.refreshHelpView()
			}
			if m.releaseNotes.active {
				m.resizeReleaseNotesViewport()
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
			cmds = append(cmds, m.doSearchCmd(msg.query))
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
		proj := m.selectedProject()
		if proj == nil {
			break
		}
		m.search.fetchedInfo = msg.info
		m.search.fetchedSource = msg.source
		m.overlayWidthOffset = 0
		m.search.active = false
		m.search.input.Blur()
		m.picker = versionPicker{
			active:        true,
			pkgName:       msg.info.ID,
			versions:      msg.info.Versions,
			cursor:        defaultVersionCursor(msg.info.Versions, proj.TargetFrameworks),
			targets:       proj.TargetFrameworks,
			addMode:       true,
			targetProject: proj,
		}

	case logLineMsg:
		m.ctx.LogLines = append(m.ctx.LogLines, msg.line)
		m.updateLogView()

	case releaseListReadyMsg:
		m.releaseNotes.loading = false
		if msg.owner != "" {
			m.releaseNotes.owner = msg.owner
			m.releaseNotes.repo = msg.repo
		}
		if msg.err != nil {
			m.releaseNotes.err = msg.err
			m.releaseNotes.vp.SetContent(styleRed.Render("Error: " + msg.err.Error()))
			break
		}
		m.releaseNotes.releases = msg.releases
		if len(msg.releases) == 0 {
			m.releaseNotes.vp.SetContent(styleMuted.Render("(no releases found)"))
			break
		}
		m.releaseNotes.cursor = 0
		// Auto-fetch notes for the first release
		m.releaseNotes.loading = true
		cmds = append(cmds, m.fetchReleaseNotesCmd(msg.releases[0]))

	case releaseNotesReadyMsg:
		m.releaseNotes.loading = false
		if msg.err != nil {
			m.releaseNotes.notes = ""
			m.releaseNotes.vp.SetContent(styleRed.Render("Error: " + msg.err.Error()))
			break
		}
		m.releaseNotes.notes = msg.body
		m.releaseNotes.notesURL = msg.htmlURL
		m.releaseNotes.vp.SetContent(m.buildReleaseNotesContent())

	case depTreeReadyMsg:
		m.depTree.loading = false
		m.depTree.err = msg.err
		if msg.err == nil {
			m.depTree.content = m.renderParsedDotnetList(parseDotnetListOutput(msg.content))
		}
		m.depTree.vp.SetContent(m.buildDepTreeContent())

	case bubble_tea.KeyMsg:
		if m.depTree.active {
			cmds = append(cmds, m.handleDepTreeKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		if m.releaseNotes.active {
			cmds = append(cmds, m.handleReleaseNotesKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		if m.showSources {
			cmds = append(cmds, m.handleSourcesKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		if m.showHelp {
			cmds = append(cmds, m.handleHelpKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		if m.search.active {
			cmds = append(cmds, m.handleSearchKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		if m.picker.active {
			cmds = append(cmds, m.handlePickerKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		if m.confirmRemove.active {
			cmds = append(cmds, m.handleConfirmRemoveKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		if m.confirmUpdate.active {
			cmds = append(cmds, m.handleConfirmUpdateKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		if m.locationPick.active {
			cmds = append(cmds, m.handleLocationPickKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		cmds = append(cmds, m.handleKey(msg))
	}

	if !m.picker.active && !m.search.active && !m.confirmRemove.active && !m.confirmUpdate.active && !m.locationPick.active {
		switch m.focus {
		case focusProjects:
			if keyMsg, ok := msg.(bubble_tea.KeyMsg); ok {
				switch keyMsg.String() {
				case "up", "k":
					if m.projectCursor > 0 {
						m.projectCursor--
						m.clampProjectOffset()
						m.packageCursor = 0
						m.packageOffset = 0
						m.rebuildPackageRows()
						m.refreshDetail()
					}
				case "down", "j":
					if m.projectCursor < len(m.projectItems)-1 {
						m.projectCursor++
						m.clampProjectOffset()
						m.packageCursor = 0
						m.packageOffset = 0
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
				m.detailView, cmd = m.detailView.Update(msg)
				cmds = append(cmds, cmd)
			}
		case focusLog:
			if m.ctx.ShowLogs {
				var cmd bubble_tea.Cmd
				m.logView, cmd = m.logView.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, bubble_tea.Batch(cmds...)
}

func (m *Model) setStatus(text string, isErr bool) bubble_tea.Cmd {
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

func (m *Model) handleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
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
		m.showSources = !m.showSources
		if m.showSources {
			m.ctx.StatusLine = ""
		}

	case "?":
		m.showHelp = !m.showHelp
		if m.showHelp {
			m.ctx.StatusLine = ""
			m.refreshHelpView()
		}

	case "up", "k":
		if m.focus == focusPackages && m.packageCursor > 0 {
			m.packageCursor--
			m.clampOffset()
			m.refreshDetail()
		}

	case "down", "j":
		if m.focus == focusPackages && m.packageCursor < len(m.packageRows)-1 {
			m.packageCursor++
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
			m.packageSortMode = m.packageSortMode.next()
			m.packageSortDir = m.packageSortMode.defaultDir()
			m.packageCursor = 0
			m.packageOffset = 0
			m.rebuildPackageRows()
			m.refreshDetail()
		}

	case "O":
		if m.focus == focusPackages {
			m.packageSortDir = !m.packageSortDir
			m.packageCursor = 0
			m.packageOffset = 0
			m.rebuildPackageRows()
			m.refreshDetail()
		}

	case "d":
		if m.focus == focusPackages && m.packageCursor < len(m.packageRows) {
			m.confirmRemove = confirmRemove{
				active:  true,
				pkgName: m.packageRows[m.packageCursor].ref.Name,
			}
			m.ctx.StatusLine = ""
		}

	case "/":
		if m.selectedProject() == nil {
			return m.setStatus("▲ Select project", true)
		}
		m.search = packageSearch{input: m.search.input}
		m.search.input.Reset()
		m.search.active = true
		m.ctx.StatusLine = ""
		return m.search.input.Focus()

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

func (m *Model) resizeFocused(delta int) {
	const (
		borders = 6
		minW    = 10
	)
	lw := m.layoutWidth()
	// maxW for one side = total minus the other side's base+offset minus borders minus minW for mid panel.
	switch m.focus {
	case focusProjects:
		maxW := lw - (50 + m.rightWidthOffset) - borders - minW
		adjustOffset(&m.leftWidthOffset, delta, 30, minW, maxW)
	case focusPackages:
		// Growing packages shrinks detail (right), so we shrink rightWidthOffset.
		maxW := lw - (30 + m.leftWidthOffset) - borders - minW
		adjustOffset(&m.rightWidthOffset, -delta, 50, minW, maxW)
		m.refreshDetail()
	case focusDetail:
		maxW := lw - (30 + m.leftWidthOffset) - borders - minW
		adjustOffset(&m.rightWidthOffset, delta, 50, minW, maxW)
		m.refreshDetail()
	}
}

func (m Model) View() bubble_tea.View {
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
	switch {
	case m.depTree.active:
		overlay = m.renderDepTreeOverlay()
	case m.releaseNotes.active:
		overlay = m.renderReleaseNotesOverlay()
	case m.search.active:
		overlay = m.renderSearchOverlay()
	case m.locationPick.active:
		overlay = m.renderLocationPickOverlay()
	case m.picker.active:
		overlay = m.renderPickerOverlay()
	case m.confirmRemove.active:
		overlay = m.renderConfirmRemoveOverlay()
	case m.confirmUpdate.active:
		overlay = m.renderConfirmUpdateOverlay()
	case m.showSources:
		overlay = m.renderSourcesOverlay()
	case m.showHelp:
		overlay = m.renderHelpOverlay()
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
