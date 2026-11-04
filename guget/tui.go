package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
	input "github.com/nulifyer/guget/internal/ui/input"
	spinner "github.com/nulifyer/guget/internal/ui/spinner"
	style "github.com/nulifyer/guget/internal/ui/style"
	viewport "github.com/nulifyer/guget/internal/ui/viewport"
)

const (
	logPanelLines       = 6
	logPanelOuterHeight = logPanelLines + 4 // borders(2) + title(1) + divider(1)
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
	host      ui.Host
	ctx       *AppContext
	lifecycle context.Context

	projectDir string
	send       func(ui.Event)
	mouse      bool

	updateVersion        string
	updateChecksDisabled bool
	updateCachePath      string

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

	workspaceGeneration  int
	sourceSignature      string
	activeReload         reloadRequestedMsg
	pendingReload        reloadRequestedMsg
	hasPendingReload     bool
	watchInputs          *workspaceWatchInputs
	workspaceStale       bool
	sourceInputRevisions map[string][32]byte

	resizeDebounceID      int
	requestID             int
	logRefreshPending     bool
	metadataCancel        context.CancelFunc
	packageRefreshPending bool
}

func (m *App) nextRequestID() int {
	m.requestID++
	return m.requestID
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

func NewApp(projectDir string, snapshot *workspaceSnapshot, initialLogLines []string, flags BuiltFlags) *App {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleAccent

	projItems := buildProjectItems(snapshot.ParsedProjects, snapshot.PropsProjects)

	dv := viewport.New(viewport.WithWidth(40), viewport.WithHeight(20))
	lv := viewport.New(viewport.WithWidth(80), viewport.WithHeight(logPanelLines))

	ti := input.New()
	ti.Placeholder = "Type a package name..."
	ti.CharLimit = 100
	ti.SetWidth(44)

	sortMode, sortDir := parseSortFlag(flags.SortBy)

	ctx := &AppContext{
		ParsedProjects:  snapshot.ParsedProjects,
		PropsProjects:   snapshot.PropsProjects,
		NugetServices:   snapshot.NugetServices,
		Sources:         snapshot.Sources,
		SourceMapping:   snapshot.SourceMapping,
		PendingPackages: NewSet[string](),
		Spinner:         sp,
		Results:         make(map[string]nugetResult),
		LogLines:        initialLogLines,
	}

	m := &App{
		ctx:                  ctx,
		lifecycle:            context.Background(),
		projectDir:           projectDir,
		mouse:                !flags.NoMouse,
		updateChecksDisabled: flags.NoUpdateCheck,
		sourceSignature:      workspaceSourceSignature(snapshot.Sources, snapshot.SourceMapping),
		sourceInputRevisions: snapshot.InputRevisions,
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
			vp:          viewport.New(),
		},
		help: helpOverlay{
			sectionBase: sectionBase{basePct: 60, minWidth: 56, maxMargin: 4},
			vp:          viewport.New(viewport.WithWidth(60), viewport.WithHeight(20)),
		},
	}
	// Set back-pointers so sections can access the App.
	m.projects.app = m
	m.packages.app = m
	m.log.app = m
	m.detail.app = m
	m.search.app = m
	m.sources.app = m
	m.help.app = m
	return m
}

func (m *App) Connect() ui.Effect {
	return ui.Batch(m.ctx.Spinner.Tick, m.checkForUpdate())
}

func (m *App) HandleEvent(msg ui.Event) ui.Effect {
	previousFooterLines := m.footerLines()
	defer func() {
		if m.footerLines() != previousFooterLines {
			m.relayout()
		}
	}()
	var cmds []ui.Effect

	switch msg := msg.(type) {
	case ui.PasteEvent:
		if m.search.active && m.ctx.Width >= 80 && m.ctx.Height >= 12 {
			cmds = append(cmds, m.search.handleInput(msg))
		}

	case selfUpdateNoticeMsg:
		m.updateVersion = msg.version

	case ui.ResizeEvent:
		m.ctx.Width = msg.Width
		m.ctx.Height = msg.Height
		// Resize allocations and reflow detail text immediately. Help content
		// is rebuilt after the terminal size settles.
		m.relayout()
		m.resizeDebounceID++
		id := m.resizeDebounceID
		cmds = append(cmds, ui.Tick(50*time.Millisecond, func(t time.Time) ui.Event {
			return resizeDebounceMsg{id: id}
		}))

	case resizeDebounceMsg:
		if msg.id == m.resizeDebounceID {
			offset := m.detail.vp.YOffset()
			m.refreshDetail()
			m.detail.vp.SetYOffset(offset)
			if m.help.active {
				m.help.refreshView()
			}
			if m.releaseNotes.active {
				m.releaseNotes.resizeViewport()
			}
			if m.depTree.active {
				m.depTree.refreshContent()
			}
		}

	case spinner.TickMsg:
		var cmd ui.Effect
		m.ctx.Spinner, cmd = m.ctx.Spinner.Update(msg)
		cmds = append(cmds, cmd)

	case packageReadyMsg:
		if msg.generation != m.workspaceGeneration {
			break
		}
		m.ctx.Results[msg.name] = msg.result
		if m.ctx.PendingPackages != nil {
			m.ctx.PendingPackages.Remove(msg.name)
		}
		if m.ctx.LoadingTotal > 0 {
			m.ctx.LoadingDone++
			if m.ctx.LoadingDone >= m.ctx.LoadingTotal {
				m.ctx.Loading = false
			}
		}
		if !m.packageRefreshPending {
			m.packageRefreshPending = true
			cmds = append(cmds, ui.Tick(25*time.Millisecond, func(time.Time) ui.Event { return packageRowsRefreshMsg{} }))
		}

	case packageRowsRefreshMsg:
		m.packageRefreshPending = false
		selectedPackage := m.packageSelection().name
		offset := m.detail.vp.YOffset()
		m.rebuildPackageRows()
		m.selectPackageByName(selectedPackage)
		m.refreshDetail()
		if strings.EqualFold(selectedPackage, m.packageSelection().name) {
			m.detail.vp.SetYOffset(offset)
		}

	case watchedWorkspaceChangeMsg:
		m.handleWatchedWorkspaceChange(msg)

	case mutationReconciledMsg:
		cmds = append(cmds, m.handleMutationReconciled(msg))

	case reloadRequestedMsg:
		m.requestReload(msg)

	case workspaceReloadedMsg:
		m.handleWorkspaceReloaded(msg)

	case writeResultMsg:
		m.ctx.Saving = false
		if msg.err != nil {
			msg.selection = nil
			cmds = append(cmds, m.setStatus("▲ Save failed: "+msg.err.Error(), true))
		} else {
			status := "✓ Saved"
			if msg.written > 0 && msg.skipped > 0 {
				status = fmt.Sprintf("✓ Saved %d, %d locked", msg.written, msg.skipped)
			} else if msg.skipped > 0 {
				status = fmt.Sprintf("%d skipped (version locked)", msg.skipped)
			}
			cmds = append(cmds, m.setStatus(status, false))
		}
		if msg.reload {
			m.requestReload(reloadRequestedMsg{reason: "package files changed", selection: msg.selection, saveError: msg.err})
		} else {
			m.maybeStartQueuedReload()
		}

	case restoreResultMsg:
		m.ctx.Restoring = false
		if msg.err != nil {
			cmds = append(cmds, m.setStatus("✗ Restore failed (see logs)", true))
		} else {
			cmds = append(cmds, m.setStatus("✓ Restore complete", false))
		}

	case searchDebounceMsg:
		if m.search.active && msg.requestID == m.search.requestID && msg.id == m.search.debounceID && msg.query == m.search.lastQuery && msg.query != "" {
			m.search.loading = true
			cmds = append(cmds, m.search.doSearchCmd(msg.query))
		}

	case searchResultsMsg:
		if m.search.active && msg.requestID == m.search.requestID && msg.id == m.search.debounceID && msg.query == m.search.lastQuery {
			m.search.loading = false
			m.search.err = msg.err
			if msg.err == nil {
				m.search.results = msg.results
			}
			m.search.cursor = 0
		}

	case packageFetchedMsg:
		if !m.search.active || msg.requestID != m.search.requestID || !m.search.fetchingVersion {
			break
		}
		m.search.fetchingVersion = false
		if msg.err != nil || msg.info == nil {
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

	case logBatchMsg:
		for _, line := range msg.lines {
			m.ctx.LogLines = appendLogLine(m.ctx.LogLines, line)
		}
		if m.ctx.ShowLogs && !m.logRefreshPending {
			m.logRefreshPending = true
			cmds = append(cmds, ui.Tick(50*time.Millisecond, func(time.Time) ui.Event { return logRefreshMsg{} }))
		}

	case logRefreshMsg:
		m.logRefreshPending = false
		if m.ctx.ShowLogs {
			m.updateLogView()
		}

	case openURLRequestedMsg:
		cmds = append(cmds, openURLCmd(m.lifecycle, msg.url))

	case openURLResultMsg:
		if msg.err != nil {
			cmds = append(cmds, m.setStatus("▲ "+msg.err.Error(), true))
		} else {
			cmds = append(cmds, m.setStatus("Opened "+shortURLHost(msg.url), false))
		}

	case releaseListReadyMsg:
		if !m.releaseNotes.active || msg.requestID != m.releaseNotes.requestID {
			break
		}
		m.releaseNotes.ghLoading = false
		if msg.owner != "" {
			m.releaseNotes.ghOwner = msg.owner
			m.releaseNotes.ghRepo = msg.repo
		}
		// Apply piggy-backed nuspec release notes if present.
		if msg.nuspecVer != "" {
			if msg.nuspecNotes != "" {
				m.releaseNotes.nsAvailable = true
			}
			if m.releaseNotes.nsNotesCache == nil {
				m.releaseNotes.nsNotesCache = make(map[string]string)
			}
			m.releaseNotes.nsNotesCache[msg.nuspecVer] = msg.nuspecNotes
			if msg.nuspecVer == m.releaseNotes.selectedNuSpecVersion() {
				m.releaseNotes.nsLoading = false
				m.releaseNotes.nsNotes = msg.nuspecNotes
			}
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
		if !m.releaseNotes.active || msg.requestID != m.releaseNotes.requestID || msg.tag != m.releaseNotes.selectedGitHubTag() {
			break
		}
		m.releaseNotes.ghLoading = false
		if msg.err != nil {
			m.releaseNotes.ghNotes = ""
		} else {
			m.releaseNotes.ghNotes = msg.body
			m.releaseNotes.ghNotesURL = msg.htmlURL
		}
		m.releaseNotes.updateViewportContent()

	case nuspecVersionNotesReadyMsg:
		if !m.releaseNotes.active || msg.requestID != m.releaseNotes.requestID {
			break
		}
		if msg.notes != "" {
			m.releaseNotes.nsAvailable = true
		}
		if m.releaseNotes.nsNotesCache == nil {
			m.releaseNotes.nsNotesCache = make(map[string]string)
		}
		m.releaseNotes.nsNotesCache[msg.version] = msg.notes
		if msg.version == m.releaseNotes.selectedNuSpecVersion() {
			m.releaseNotes.nsLoading = false
			m.releaseNotes.nsNotes = msg.notes
			m.releaseNotes.updateViewportContent()
		}

	case depTreeReadyMsg:
		if !m.depTree.active || msg.requestID != m.depTree.requestID {
			break
		}
		m.depTree.loading = false
		m.depTree.err = msg.err
		if msg.err == nil {
			m.depTree.dotnetProjects = parseDotnetListOutput(msg.content)
			m.depTree.content = m.depTree.renderParsedDotnetList(m.depTree.dotnetProjects)
		}
		m.depTree.vp.SetContent(m.depTree.buildContent())

	case mouseInputMsg:
		cmds = append(cmds, m.handleMouse(msg))

	case ui.KeyRelease:
		return nil

	case ui.KeyPress:
		if msg.String() == "ctrl+c" {
			return ui.Quit
		}
		if m.ctx.Width > 0 && (m.ctx.Width < 80 || m.ctx.Height < 12) {
			switch mainKey(msg.String()) {
			case "ctrl+c", "esc", "q":
				return ui.Quit
			}
			return nil
		}
		if o := m.activeOverlay(); o != nil {
			// Reconcile before dispatch as a prior event may have opened a dialog.
			footer := m.renderFooter()
			m.host.Reconcile(m.componentNodes(footer, style.Height(footer)))
			cmds = append(cmds, m.host.HandleKey(msg))
		} else {
			cmds = append(cmds, m.handleKey(msg))
		}
	}

	return ui.Batch(cmds...)
}

func (m *App) setStatus(text string, isErr bool) ui.Effect {
	// Strip newlines and truncate to keep the status on a single line.
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	maxW := m.layoutWidth() - 6 // padding + border
	if style.Width(text) > maxW && maxW > 3 {
		text = truncate(text, maxW)
	}
	m.ctx.StatusLine = text
	m.ctx.StatusIsErr = isErr
	return nil
}

func (m *App) handleKey(msg ui.KeyEvent) ui.Effect {
	if _, pressed := msg.(ui.KeyPress); !pressed {
		return nil
	}
	switch mainKey(msg.String()) {
	case "ctrl+x":
		return m.dismissUpdateNotice()
	case "ctrl+c":
		return ui.Quit

	case "tab":
		if m.compactLogs() {
			return nil
		}
		if m.ctx.ShowLogs {
			m.focus = (m.focus + 1) % 4
		} else {
			m.focus = (m.focus + 1) % 3
		}

	case "shift+tab":
		if m.compactLogs() {
			return nil
		}
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
			m.sources.requestID = m.nextRequestID()
			m.ctx.StatusLine = ""
		}

	case "?":
		m.help.active = !m.help.active
		if m.help.active {
			m.help.requestID = m.nextRequestID()
			m.ctx.StatusLine = ""
			m.help.refreshView()
		}

	case "r":
		if !m.ctx.Restoring {
			return m.restore(scopeSelected)
		}

	case "R":
		if !m.ctx.Restoring {
			return m.restore(scopeAll)
		}

	case "ctrl+r":
		m.requestReload(reloadRequestedMsg{reason: "manual reload"})

	case "T":
		return m.openTransitiveDepTree()

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

	default:
		return m.focusedComponent().HandleKey(msg)
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

func (m *App) Render() ui.Frame {
	v := ui.NewFrame("")

	if m.ctx.Width == 0 {
		v.SetContent("Initializing...")
		return v
	}
	if m.ctx.Width < 80 || m.ctx.Height < 12 {
		lines := []string{"Resize terminal to at least 80 x 12.", "esc / q / ctrl+c quit"}
		for i := range lines {
			lines[i] = truncate(lines[i], max(0, m.ctx.Width))
		}
		v.SetContent(strings.Join(lines[:min(len(lines), max(0, m.ctx.Height))], "\n"))
		return v
	}

	footer := m.renderFooter()
	footerH := style.Height(footer)

	m.host.Reconcile(m.componentNodes(footer, footerH))
	v.SetContent(m.host.Draw(m.ctx.Width, m.ctx.Height))
	if o := m.activeOverlay(); o != nil {
		var regions []mouseRegion
		if provider, ok := o.(interface{ MouseRegions() []mouseRegion }); ok {
			regions = provider.MouseRegions()
		}
		m.bindMouse(&v, mouseLayout{modal: true, regions: regions})
	} else {
		left, mid, right := m.panelWidths()
		m.bindMouse(&v, m.mainMouseLayout(left, mid, right))
	}
	return v
}
