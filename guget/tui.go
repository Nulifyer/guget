package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bubbles_spinner "github.com/charmbracelet/bubbles/spinner"
	bubbles_textinpute "github.com/charmbracelet/bubbles/textinput"
	bubbles_viewport "github.com/charmbracelet/bubbles/viewport"
	bubble_tea "github.com/charmbracelet/bubbletea"
	lipgloss "github.com/charmbracelet/lipgloss"
)

const (
	logPanelLines       = 6
	logPanelOuterHeight = logPanelLines + 4 // border(2) + title(1) + divider(1)
	maxLayoutWidth      = 210
)

// layoutWidth returns the effective width for the main content area,
// capped at maxLayoutWidth so the UI stays readable on ultra-wide terminals.
func (m *Model) layoutWidth() int {
	const minLayoutWidth = 80
	if m.width < minLayoutWidth {
		return minLayoutWidth
	}
	if m.width > maxLayoutWidth {
		return maxLayoutWidth
	}
	return m.width
}

type focusPanel int

const (
	focusProjects focusPanel = iota
	focusPackages
	focusDetail
	focusLog
)

type actionScope int

const (
	scopeSelected actionScope = iota // this project (nil when "All Projects" → becomes all)
	scopeAll                         // always all projects
)

type packageSortMode int

const (
	sortByStatus    packageSortMode = iota // status then name (default)
	sortByName                             // name only
	sortBySource                           // source then name
	sortByPublished                        // last published (newest first)
)

func (s packageSortMode) label() string {
	switch s {
	case sortByName:
		return "name"
	case sortBySource:
		return "source"
	case sortByPublished:
		return "published"
	default:
		return "status"
	}
}

func (s packageSortMode) defaultDir() bool {
	switch s {
	case sortByPublished:
		return false
	default:
		return true
	}
}

func (s packageSortMode) next() packageSortMode {
	return (s + 1) % 4
}

// packageReadyMsg is sent by the background loader for each package as its
// NuGet metadata resolves, enabling progressive UI updates.
type packageReadyMsg struct {
	name   string
	result nugetResult
}

// resultsReadyMsg is kept for compatibility but is no longer sent by main.go.
type resultsReadyMsg struct {
	results map[string]nugetResult
}

type writeResultMsg struct {
	err error
}

type restoreResultMsg struct {
	err error
}

type resizeDebounceMsg struct {
	id int
}

type searchDebounceMsg struct {
	id    int
	query string
}

type searchResultsMsg struct {
	results []SearchResult
	query   string
	err     error
}

type packageFetchedMsg struct {
	info   *PackageInfo
	source string
	err    error
}

type logLineMsg struct {
	line string
}

type depTreeReadyMsg struct {
	content string
	err     error
}

type depTreeOverlay struct {
	active  bool
	loading bool // true while dotnet list is running (T key)
	content string
	err     error
	vp      bubbles_viewport.Model
	title   string
}

type projectItem struct {
	name    string
	project *ParsedProject // nil = "All Projects"
}

func (p projectItem) Title() string {
	if p.project == nil {
		return "◈ All Projects"
	}
	return "◦ " + p.name
}

func (p projectItem) Description() string {
	if p.project == nil {
		return "Combined view"
	}
	var fws []string
	for fw := range p.project.TargetFrameworks {
		fws = append(fws, fw.String())
	}
	if len(fws) > 0 {
		return strings.Join(fws, ", ")
	}
	return fmt.Sprintf("%d packages", p.project.Packages.Len())
}

type packageRow struct {
	ref              PackageReference
	project          *ParsedProject
	info             *PackageInfo
	source           string
	err              error
	latestCompatible *PackageVersion
	latestStable     *PackageVersion
	diverged         bool
	oldest           SemVer
	vulnerable       bool // installed version has ≥1 known vulnerability
	deprecated       bool // package is deprecated in the registry
}

// effectiveVersion returns the version used for status comparisons.
// When diverged (All Projects view), use the oldest version so the icon
// reflects the least-up-to-date project.
func (r packageRow) effectiveVersion() SemVer {
	if r.diverged {
		return r.oldest
	}
	return r.ref.Version
}

func (r packageRow) statusIcon() string {
	if r.vulnerable {
		return "▲"
	}
	if r.err != nil {
		return "✗"
	}
	ver := r.effectiveVersion()
	check := r.latestCompatible
	if check == nil {
		check = r.latestStable
	}
	if check != nil && check.SemVer.IsNewerThan(ver) {
		if r.latestStable != nil && r.latestCompatible != nil &&
			r.latestStable.SemVer.IsNewerThan(r.latestCompatible.SemVer) {
			return "⬆"
		}
		return "↑"
	}
	if r.deprecated {
		return "~"
	}
	return "✓"
}

func (r packageRow) statusStyle() lipgloss.Style {
	if r.vulnerable {
		return styleRed
	}
	if r.err != nil {
		return styleRed
	}
	ver := r.effectiveVersion()
	check := r.latestCompatible
	if check == nil {
		check = r.latestStable
	}
	if check != nil && check.SemVer.IsNewerThan(ver) {
		if r.latestStable != nil && r.latestCompatible != nil &&
			r.latestStable.SemVer.IsNewerThan(r.latestCompatible.SemVer) {
			return stylePurple
		}
		return styleYellow
	}
	if r.deprecated {
		return styleYellow
	}
	return styleGreen
}

type versionPicker struct {
	active        bool
	pkgName       string
	versions      []PackageVersion
	cursor        int
	targets       Set[TargetFramework]
	addMode       bool
	targetProject *ParsedProject
}

func (vp *versionPicker) selectedVersion() *PackageVersion {
	if vp.cursor < len(vp.versions) {
		return &vp.versions[vp.cursor]
	}
	return nil
}

type packageSearch struct {
	active          bool
	input           bubbles_textinpute.Model
	debounceID      int
	lastQuery       string
	results         []SearchResult
	cursor          int
	loading         bool
	err             error
	fetchingVersion bool
	fetchedInfo     *PackageInfo
	fetchedSource   string
}

type confirmRemove struct {
	active  bool
	pkgName string
}

type Model struct {
	width  int
	height int
	focus  focusPanel

	parsedProjects []*ParsedProject
	propsProjects  []*ParsedProject // standalone .props files shown after proj files
	nugetServices  []*NugetService
	results        map[string]nugetResult
	loading        bool
	loadingDone    int
	loadingTotal   int
	spinner        bubbles_spinner.Model

	projectItems  []projectItem
	projectCursor int
	projectOffset int

	packageRows     []packageRow
	packageCursor   int
	packageOffset   int
	packageSortMode packageSortMode
	packageSortDir  bool

	detailView bubbles_viewport.Model

	picker  versionPicker
	search  packageSearch
	confirm confirmRemove
	depTree depTreeOverlay

	sources       []NugetSource
	sourceMapping *PackageSourceMapping
	showSources   bool
	showHelp      bool
	helpView      bubbles_viewport.Model

	statusLine  string
	statusIsErr bool
	restoring   bool

	logLines []string
	logView  bubbles_viewport.Model
	showLogs bool

	resizeDebounceID int

	leftWidthOffset    int // projects panel width offset ([ / ])
	rightWidthOffset   int // detail panel width offset ([ / ])
	overlayWidthOffset int // active overlay width offset, reset on close
}

func NewModel(parsedProjects []*ParsedProject, propsProjects []*ParsedProject, nugetServices []*NugetService, sources []NugetSource, sourceMapping *PackageSourceMapping, initialLogLines []string, loadingTotal int) Model {
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

	dv := bubbles_viewport.New(40, 20)
	lv := bubbles_viewport.New(80, logPanelLines)

	ti := bubbles_textinpute.New()
	ti.Placeholder = "Type a package name…"
	ti.CharLimit = 100
	ti.Width = 44

	m := Model{
		parsedProjects:  parsedProjects,
		propsProjects:   propsProjects,
		nugetServices:   nugetServices,
		sources:         sources,
		sourceMapping:   sourceMapping,
		loading:         loadingTotal > 0,
		loadingTotal:    loadingTotal,
		spinner:         sp,
		projectItems:    projItems,
		detailView:      dv,
		search:          packageSearch{input: ti},
		logLines:        initialLogLines,
		logView:         lv,
		results:         make(map[string]nugetResult, loadingTotal),
		packageSortMode: sortByStatus,
		packageSortDir:  sortByStatus.defaultDir(),
	}
	return m
}

func (m Model) Init() bubble_tea.Cmd {
	return m.spinner.Tick
}

func (m Model) Update(msg bubble_tea.Msg) (bubble_tea.Model, bubble_tea.Cmd) {
	var cmds []bubble_tea.Cmd

	switch msg := msg.(type) {

	case bubble_tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeDebounceID++
		id := m.resizeDebounceID
		cmds = append(cmds, bubble_tea.Tick(50*time.Millisecond, func(t time.Time) bubble_tea.Msg {
			return resizeDebounceMsg{id: id}
		}))

	case resizeDebounceMsg:
		if msg.id == m.resizeDebounceID {
			m.relayout()
			if m.showHelp {
				m.refreshHelpView()
			}
		}

	case bubbles_spinner.TickMsg:
		var cmd bubble_tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case packageReadyMsg:
		m.results[msg.name] = msg.result
		m.loadingDone++
		if m.loadingDone >= m.loadingTotal {
			m.loading = false
			m.refreshDetail()
		}
		m.rebuildPackageRows()

	case resultsReadyMsg: // fallback: bulk-load all results at once
		m.results = msg.results
		m.loadingDone = len(msg.results)
		m.loading = false
		m.rebuildPackageRows()
		m.refreshDetail()

	case writeResultMsg:
		if msg.err != nil {
			cmds = append(cmds, m.setStatus("▲ Save failed: "+msg.err.Error(), true))
		} else {
			cmds = append(cmds, m.setStatus("✓ Saved", false))
		}

	case restoreResultMsg:
		m.restoring = false
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
		m.logLines = append(m.logLines, msg.line)
		m.updateLogView()

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
		if m.showSources {
			switch msg.String() {
			case "[":
				m.overlayWidthOffset -= 4
			case "]":
				m.overlayWidthOffset += 4
			case "esc", "s", "q":
				m.overlayWidthOffset = 0
				m.showSources = false
				m.statusLine = ""
			}
			return m, bubble_tea.Batch(cmds...)
		}
		if m.showHelp {
			switch msg.String() {
			case "[":
				m.overlayWidthOffset -= 4
				m.refreshHelpView()
			case "]":
				m.overlayWidthOffset += 4
				m.refreshHelpView()
			case "esc", "?", "q":
				m.overlayWidthOffset = 0
				m.showHelp = false
				m.statusLine = ""
			default:
				var cmd bubble_tea.Cmd
				m.helpView, cmd = m.helpView.Update(msg)
				cmds = append(cmds, cmd)
			}
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
		if m.confirm.active {
			cmds = append(cmds, m.handleConfirmKey(msg))
			return m, bubble_tea.Batch(cmds...)
		}
		cmds = append(cmds, m.handleKey(msg))
	}

	if !m.picker.active && !m.search.active && !m.confirm.active {
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
			var cmd bubble_tea.Cmd
			m.detailView, cmd = m.detailView.Update(msg)
			cmds = append(cmds, cmd)
		case focusLog:
			if m.showLogs {
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
	m.statusLine = text
	m.statusIsErr = isErr
	return nil
}

func (m *Model) handleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		return bubble_tea.Quit

	case "tab":
		if m.showLogs {
			m.focus = (m.focus + 1) % 4
		} else {
			m.focus = (m.focus + 1) % 3
		}

	case "shift+tab":
		// Reverse-cycle: adding (n-1) mod n is equivalent to subtracting 1 without going negative.
		if m.showLogs {
			m.focus = (m.focus + 3) % 4
		} else {
			m.focus = (m.focus + 2) % 3
		}

	case "l":
		m.showLogs = !m.showLogs
		if !m.showLogs && m.focus == focusLog {
			m.focus = focusPackages
		}
		if m.showLogs {
			m.updateLogView()
		}
		m.relayout()

	case "s":
		m.showSources = !m.showSources
		if m.showSources {
			m.statusLine = ""
		}

	case "?":
		m.showHelp = !m.showHelp
		if m.showHelp {
			m.statusLine = ""
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
		if !m.restoring {
			return m.restore(scopeSelected)
		}

	case "R":
		if !m.restoring {
			return m.restore(scopeAll)
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
			m.confirm = confirmRemove{
				active:  true,
				pkgName: m.packageRows[m.packageCursor].ref.Name,
			}
			m.statusLine = ""
		}

	case "/":
		if m.selectedProject() == nil {
			return m.setStatus("▲ Select project", true)
		}
		m.search = packageSearch{input: m.search.input}
		m.search.input.Reset()
		m.search.active = true
		m.statusLine = ""
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

	switch m.focus {
	case focusProjects:
		m.leftWidthOffset += delta
	case focusPackages:
		m.rightWidthOffset -= delta
		m.refreshDetail()
	case focusDetail:
		m.rightWidthOffset += delta
		m.refreshDetail()
	}

	left := 30 + m.leftWidthOffset
	right := 50 + m.rightWidthOffset
	mid := lw - left - right - borders

	if left < minW || mid < minW || right < minW {
		switch m.focus {
		case focusProjects:
			m.leftWidthOffset -= delta
		case focusPackages:
			m.rightWidthOffset += delta
		case focusDetail:
			m.rightWidthOffset -= delta
		}
	}
}

func (m *Model) handlePickerKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		m.overlayWidthOffset -= 4
		return nil
	case "]":
		m.overlayWidthOffset += 4
		return nil
	case "esc", "q":
		m.overlayWidthOffset = 0
		m.picker.active = false
		m.picker.addMode = false
		m.picker.targetProject = nil
		m.statusLine = ""
	case "up", "k":
		if m.picker.cursor > 0 {
			m.picker.cursor--
		}
	case "down", "j":
		if m.picker.cursor < len(m.picker.versions)-1 {
			m.picker.cursor++
		}
	case "u":
		return m.applyPickerVersion(scopeSelected)
	case "U":
		return m.applyPickerVersion(scopeAll)
	case "enter":
		if v := m.picker.selectedVersion(); v != nil {
			m.overlayWidthOffset = 0
			m.picker.active = false
			if m.picker.addMode && m.picker.targetProject != nil {
				return m.addPackageToProject(m.picker.pkgName, v.SemVer.String(), m.picker.targetProject)
			}
			return m.applyVersion(m.picker.pkgName, v.SemVer.String(), m.picker.targetProject)
		}
	}
	return nil
}

func (m *Model) handleSearchKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		m.overlayWidthOffset -= 4
		return nil
	case "]":
		m.overlayWidthOffset += 4
		return nil
	case "esc":
		m.overlayWidthOffset = 0
		m.search.active = false
		m.search.input.Blur()
		m.statusLine = ""
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
		if cached, ok := m.results[selected.ID]; ok && cached.pkg != nil {
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

func (m *Model) updatePackage(useStable bool, scope actionScope) bubble_tea.Cmd {
	if m.packageCursor >= len(m.packageRows) {
		return nil
	}
	row := m.packageRows[m.packageCursor]
	if row.err != nil {
		return nil
	}
	var target *PackageVersion
	if useStable {
		target = row.latestStable
	} else {
		target = row.latestCompatible
	}
	if target == nil {
		return nil
	}
	var project *ParsedProject
	if scope == scopeSelected {
		project = m.selectedProject()
	}
	return m.applyVersion(row.ref.Name, target.SemVer.String(), project)
}

func (m *Model) isPropsProject(p *ParsedProject) bool {
	for _, pp := range m.propsProjects {
		if pp == p {
			return true
		}
	}
	return false
}

// allProjects returns every project (parsed + props) for propagation purposes.
func (m *Model) allProjects() []*ParsedProject {
	all := make([]*ParsedProject, 0, len(m.parsedProjects)+len(m.propsProjects))
	all = append(all, m.parsedProjects...)
	all = append(all, m.propsProjects...)
	return all
}

func (m *Model) applyVersion(pkgName, version string, targetProject *ParsedProject) bubble_tea.Cmd {
	projects := m.parsedProjects
	if targetProject != nil {
		projects = []*ParsedProject{targetProject}
	}
	type writeTarget struct {
		filePath string
	}
	var toWrite []writeTarget
	// Determine the on-disk source file so we know which .props (if any) to propagate.
	var propsSource string
	for _, p := range projects {
		updated := NewSet[PackageReference]()
		changed := false
		for ref := range p.Packages {
			if ref.Name == pkgName {
				ref.Version = ParseSemVer(version)
				changed = true
			}
			updated.Add(ref)
		}
		p.Packages = updated
		if changed {
			sourceFile := p.SourceFileForPackage(pkgName)
			if sourceFile != "" {
				toWrite = append(toWrite, writeTarget{filePath: sourceFile})
				if strings.HasSuffix(strings.ToLower(sourceFile), ".props") {
					propsSource = sourceFile
				}
			}
		}
	}
	// When the package lives in a .props file, propagate the version change
	// to every other project that inherits from the same file.
	if propsSource != "" {
		for _, p := range m.allProjects() {
			if p.SourceFileForPackage(pkgName) != propsSource {
				continue
			}
			updated := NewSet[PackageReference]()
			for ref := range p.Packages {
				if ref.Name == pkgName {
					ref.Version = ParseSemVer(version)
				}
				updated.Add(ref)
			}
			p.Packages = updated
		}
	}
	m.rebuildPackageRows()
	m.refreshDetail()

	logInfo("applyVersion: %s → %s (%d file(s) to write)", pkgName, version, len(toWrite))
	if len(toWrite) == 0 {
		return nil
	}
	return func() bubble_tea.Msg {
		seen := make(map[string]bool)
		for _, wt := range toWrite {
			if seen[wt.filePath] {
				continue
			}
			seen[wt.filePath] = true
			logDebug("writing %s to %s", pkgName, wt.filePath)
			if err := UpdatePackageVersion(wt.filePath, pkgName, version); err != nil {
				logWarn("write failed for %s: %v", wt.filePath, err)
				return writeResultMsg{err: err}
			}
		}
		return writeResultMsg{err: nil}
	}
}

func (m *Model) restore(scope actionScope) bubble_tea.Cmd {
	m.restoring = true
	if scope == scopeSelected {
		sel := m.selectedProject()
		if sel != nil && !m.isPropsProject(sel) {
			return runDotnetRestore([]*ParsedProject{sel})
		}
	}
	// scopeAll, or "All Projects" selected, or .props file — restore all actual project files.
	return runDotnetRestore(m.parsedProjects)
}

func runDotnetRestore(projects []*ParsedProject) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		var lastErr error
		for _, p := range projects {
			if p.FilePath == "" {
				continue
			}
			logDebug("dotnet restore: %s", p.FilePath)
			cmd := exec.Command("dotnet", "restore", p.FilePath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				logWarn("restore failed for %s: %v\n%s", p.FilePath, err, strings.TrimSpace(string(out)))
				lastErr = fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
			} else {
				logInfo("restore succeeded for %s", p.FileName)
			}
		}
		return restoreResultMsg{err: lastErr}
	}
}

func runDepTreeCmd(project *ParsedProject) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		cmd := exec.Command("dotnet", "list", project.FilePath, "package", "--include-transitive")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return depTreeReadyMsg{err: fmt.Errorf("dotnet list: %w\n%s", err, strings.TrimSpace(string(out)))}
		}
		return depTreeReadyMsg{content: string(out)}
	}
}

func (m *Model) openDepTree() bubble_tea.Cmd {
	if m.packageCursor >= len(m.packageRows) {
		return nil
	}
	row := m.packageRows[m.packageCursor]
	if row.info == nil {
		return nil
	}
	m.statusLine = ""
	// Find the installed version's dependency groups.
	var installedVer *PackageVersion
	for i := range row.info.Versions {
		if row.info.Versions[i].SemVer.String() == row.ref.Version.String() {
			installedVer = &row.info.Versions[i]
			break
		}
	}
	overlayW, overlayH := m.depTreeOverlaySize()
	vp := bubbles_viewport.New(overlayW-6, overlayH-8)
	vp.SetContent(m.formatDepGroups(installedVer))
	m.depTree = depTreeOverlay{
		active:  true,
		title:   row.ref.Name + " " + row.ref.Version.String(),
		content: vp.View(),
		vp:      vp,
	}
	return nil
}

func (m *Model) openTransitiveDepTree() bubble_tea.Cmd {
	proj := m.selectedProject()
	if proj == nil {
		return m.setStatus("▲ Select a project first", true)
	}
	m.statusLine = ""
	overlayW, overlayH := m.depTreeOverlaySize()
	vp := bubbles_viewport.New(overlayW-6, overlayH-8)
	m.depTree = depTreeOverlay{
		active:  true,
		loading: true,
		title:   proj.FileName + " (transitive packages)",
		vp:      vp,
	}
	return runDepTreeCmd(proj)
}

// overlayHeight returns the number of terminal rows available for overlay
// content (full height minus the footer).
func (m Model) overlayHeight() int {
	return m.height - m.footerLines() - 1 // -1 for footer top border
}

func (m Model) depTreeOverlaySize() (w, h int) {
	w = clampW(m.width*80/100+m.overlayWidthOffset, 40, m.width-4)
	h = m.overlayHeight() - 4 // fill available space (minus box chrome)
	return
}

// formatVersionRange converts NuGet version range notation to a readable form.
// e.g. "[8.0.0, )" → ">= 8.0.0",  "[1.0, 2.0)" → ">= 1.0 < 2.0",  "[1.0.0]" → "1.0.0"
func formatVersionRange(r string) string {
	r = strings.TrimSpace(r)
	if r == "" {
		return "any"
	}
	if len(r) < 2 {
		return r
	}
	startInclusive := r[0] == '['
	endInclusive := r[len(r)-1] == ']'

	// Exact version: [1.0.0] with no comma
	if startInclusive && endInclusive && !strings.Contains(r, ",") {
		return strings.Trim(r, "[]")
	}

	inner := r[1 : len(r)-1]
	parts := strings.SplitN(inner, ",", 2)
	if len(parts) != 2 {
		return r // not a recognised range — return as-is
	}
	low := strings.TrimSpace(parts[0])
	high := strings.TrimSpace(parts[1])

	var result strings.Builder
	if low != "" {
		if startInclusive {
			result.WriteString(">= ")
		} else {
			result.WriteString("> ")
		}
		result.WriteString(low)
	}
	if high != "" {
		if result.Len() > 0 {
			result.WriteString(" ")
		}
		if endInclusive {
			result.WriteString("<= ")
		} else {
			result.WriteString("< ")
		}
		result.WriteString(high)
	}
	if result.Len() == 0 {
		return "any"
	}
	return result.String()
}

func (m *Model) formatDepGroups(v *PackageVersion) string {
	if v == nil || len(v.DependencyGroups) == 0 {
		return styleMuted.Render("(no dependency information available)")
	}
	// Compute max dependency name width for column alignment.
	maxNameW := 20
	for _, dg := range v.DependencyGroups {
		for _, dep := range dg.Dependencies {
			if w := lipgloss.Width(dep.ID); w > maxNameW {
				maxNameW = w
			}
		}
	}
	maxNameW += 2

	var sb strings.Builder
	for _, dg := range v.DependencyGroups {
		fw := dg.TargetFramework
		if fw == "" {
			fw = "any"
		}
		sb.WriteString(styleAccentBold.Render("["+fw+"]") + "\n")
		if len(dg.Dependencies) == 0 {
			sb.WriteString(styleMuted.Render("  (no dependencies)") + "\n")
		} else {
			for _, dep := range dg.Dependencies {
				icon, iconStyle := " ", styleMuted
				if row := m.rowByName(dep.ID); row != nil {
					icon, iconStyle = row.statusIcon(), row.statusStyle()
				}
				rangeStr := formatVersionRange(dep.Range)
				sb.WriteString("  " + iconStyle.Render(icon) + " ")
				sb.WriteString(styleText.Render(padRight(dep.ID, maxNameW)) +
					styleSubtle.Render(rangeStr) + "\n")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (m Model) rowByName(name string) *packageRow {
	for i := range m.packageRows {
		if strings.EqualFold(m.packageRows[i].ref.Name, name) {
			return &m.packageRows[i]
		}
	}
	return nil
}

func (m *Model) buildDepTreeContent() string {
	if m.depTree.err != nil {
		return styleRed.Render("Error: " + m.depTree.err.Error())
	}
	if m.depTree.loading {
		return "Loading…"
	}
	return m.depTree.content
}

type dotnetListPkg struct {
	Name      string
	Requested string // empty for transitive packages
	Resolved  string
}

type dotnetListFramework struct {
	Name       string
	TopLevel   []dotnetListPkg
	Transitive []dotnetListPkg
}

type dotnetListProject struct {
	Name       string
	Frameworks []dotnetListFramework
}

func parseDotnetListOutput(raw string) []dotnetListProject {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	var projects []dotnetListProject
	var curProj *dotnetListProject
	var curFW *dotnetListFramework
	inTransitive := false

	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(stripped, "Project '"):
			name := stripped
			if i := strings.Index(stripped, "'"); i >= 0 {
				rest := stripped[i+1:]
				if j := strings.Index(rest, "'"); j >= 0 {
					name = rest[:j]
				}
			}
			projects = append(projects, dotnetListProject{Name: name})
			curProj = &projects[len(projects)-1]
			curFW = nil
			inTransitive = false

		case strings.HasPrefix(stripped, "[") &&
			(strings.HasSuffix(stripped, "]:") || strings.HasSuffix(stripped, "]")):
			if curProj == nil {
				continue
			}
			fw := strings.TrimSuffix(stripped, ":")
			curProj.Frameworks = append(curProj.Frameworks, dotnetListFramework{Name: fw})
			curFW = &curProj.Frameworks[len(curProj.Frameworks)-1]
			inTransitive = false

		case strings.Contains(stripped, "Top-level"):
			inTransitive = false

		case strings.Contains(stripped, "Transitive"):
			inTransitive = true

		case strings.HasPrefix(stripped, ">"):
			if curFW == nil {
				continue
			}
			// Rejoin interval tokens split by whitespace, e.g. "[2.0.3," ")" → "[2.0.3, )"
			fields := rejoinIntervals(strings.Fields(strings.TrimSpace(strings.TrimPrefix(stripped, ">"))))
			if len(fields) == 0 {
				continue
			}
			pkg := dotnetListPkg{Name: fields[0]}
			rest := fields[1:]
			// Skip the "(A)" auto-referenced marker emitted by dotnet list.
			if len(rest) > 0 && rest[0] == "(A)" {
				rest = rest[1:]
			}
			if inTransitive {
				if len(rest) >= 1 {
					pkg.Resolved = rest[0]
				}
				curFW.Transitive = append(curFW.Transitive, pkg)
			} else {
				if len(rest) >= 1 {
					pkg.Requested = rest[0]
				}
				if len(rest) >= 2 {
					pkg.Resolved = rest[1]
				}
				curFW.TopLevel = append(curFW.TopLevel, pkg)
			}
		}
	}
	return projects
}

// rejoinIntervals merges fields that are parts of a split NuGet interval
// notation, e.g. ["[2.0.3,", ")"] → ["[2.0.3, )"].
func rejoinIntervals(fields []string) []string {
	var result []string
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if (strings.HasPrefix(f, "[") || strings.HasPrefix(f, "(")) &&
			!strings.HasSuffix(f, ")") && !strings.HasSuffix(f, "]") {
			for i+1 < len(fields) {
				i++
				f += " " + fields[i]
				if strings.HasSuffix(f, ")") || strings.HasSuffix(f, "]") {
					break
				}
			}
		}
		result = append(result, f)
	}
	return result
}

func (m Model) renderParsedDotnetList(projects []dotnetListProject) string {
	// Compute max package name width across all frameworks so the version
	// column starts at the same position regardless of name length.
	maxNameW := 20
	for _, proj := range projects {
		for _, fw := range proj.Frameworks {
			for _, pkg := range fw.TopLevel {
				if w := lipgloss.Width(pkg.Name); w > maxNameW {
					maxNameW = w
				}
			}
			for _, pkg := range fw.Transitive {
				if w := lipgloss.Width(pkg.Name); w > maxNameW {
					maxNameW = w
				}
			}
		}
	}
	maxNameW += 2 // breathing room

	var sb strings.Builder
	for pi, proj := range projects {
		if pi > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(styleAccentBold.Render("◈ "+proj.Name) + "\n")
		for _, fw := range proj.Frameworks {
			sb.WriteString("\n" + styleAccentBold.Render(fw.Name) + "\n")
			if len(fw.TopLevel) > 0 {
				sb.WriteString(styleSubtle.Render("  top-level") + "\n")
				for _, pkg := range fw.TopLevel {
					icon, iconStyle := " ", styleMuted
					if row := m.rowByName(pkg.Name); row != nil {
						icon, iconStyle = row.statusIcon(), row.statusStyle()
					}
					sb.WriteString("  " + iconStyle.Render(icon) + " ")
					sb.WriteString(styleText.Render(padRight(pkg.Name, maxNameW)))
					// Only show Requested when it is a specific pinned version
					// (not a range like "[2.0.3, )") that differs from Resolved.
					isRange := strings.ContainsAny(pkg.Requested, "[]()")
					showReq := pkg.Requested != "" && !isRange && pkg.Requested != pkg.Resolved
					if showReq {
						sb.WriteString(styleMuted.Render(padRight(pkg.Requested, 14)))
					} else {
						sb.WriteString(strings.Repeat(" ", 14))
					}
					if pkg.Resolved != "" {
						vs := styleMuted
						if showReq {
							vs = styleYellow
						}
						sb.WriteString(vs.Render(pkg.Resolved))
					}
					sb.WriteString("\n")
				}
			}
			if len(fw.Transitive) > 0 {
				sb.WriteString("\n" + styleSubtle.Render("  transitive") + "\n")
				for _, pkg := range fw.Transitive {
					icon, iconStyle := " ", styleMuted
					if row := m.rowByName(pkg.Name); row != nil {
						icon, iconStyle = row.statusIcon(), row.statusStyle()
					}
					sb.WriteString("  " + iconStyle.Render(icon) + " ")
					sb.WriteString(styleSubtle.Render(padRight(pkg.Name, maxNameW)))
					if pkg.Resolved != "" {
						sb.WriteString(styleMuted.Render(formatVersionRange(pkg.Resolved)))
					}
					sb.WriteString("\n")
				}
			}
			if len(fw.TopLevel) == 0 && len(fw.Transitive) == 0 {
				sb.WriteString("  " + styleMuted.Render("(no packages)") + "\n")
			}
		}
	}
	return sb.String()
}

func (m Model) renderDepTreeOverlay() string {
	overlayW, overlayH := m.depTreeOverlaySize()
	innerW := overlayW - 6

	var lines []string
	lines = append(lines,
		styleAccentBold.Render(m.depTree.title),
	)
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	if m.depTree.loading {
		lines = append(lines,
			m.spinner.View()+" "+
				styleSubtle.Render("Loading dependency tree…"),
		)
		// pad to fill viewport height
		vpH := overlayH - 8
		for i := 1; i < vpH; i++ {
			lines = append(lines, "")
		}
	} else {
		lines = append(lines, m.depTree.vp.View())
	}

	box := styleOverlay.
		Width(overlayW).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) applyPickerVersion(scope actionScope) bubble_tea.Cmd {
	v := m.picker.selectedVersion()
	if v == nil {
		return nil
	}
	m.overlayWidthOffset = 0
	m.picker.active = false
	if m.picker.addMode && m.picker.targetProject != nil {
		return m.addPackageToProject(m.picker.pkgName, v.SemVer.String(), m.picker.targetProject)
	}
	var project *ParsedProject
	if scope == scopeSelected {
		project = m.selectedProject()
	}
	return m.applyVersion(m.picker.pkgName, v.SemVer.String(), project)
}

func (m *Model) openVersionPicker() {
	if m.packageCursor >= len(m.packageRows) {
		return
	}
	row := m.packageRows[m.packageCursor]
	if row.info == nil {
		return
	}
	m.statusLine = ""
	m.picker = versionPicker{
		active:        true,
		pkgName:       row.ref.Name,
		versions:      row.info.Versions,
		cursor:        defaultVersionCursor(row.info.Versions, row.project.TargetFrameworks),
		targets:       row.project.TargetFrameworks, // used for compatibility display only
		targetProject: m.selectedProject(),          // nil = all projects, specific = scoped
	}
}

func (m *Model) searchDebounceCmd(query string) bubble_tea.Cmd {
	m.search.debounceID++
	id := m.search.debounceID
	return bubble_tea.Tick(500*time.Millisecond, func(t time.Time) bubble_tea.Msg {
		return searchDebounceMsg{id: id, query: query}
	})
}

func (m *Model) doSearchCmd(query string) bubble_tea.Cmd {
	services := m.nugetServices
	sourceMapping := m.sourceMapping
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
	services := FilterServices(m.nugetServices, m.sourceMapping, id)
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

func (m *Model) addPackageToProject(pkgName, version string, project *ParsedProject) bubble_tea.Cmd {
	project.Packages.Add(PackageReference{Name: pkgName, Version: ParseSemVer(version)})
	project.PackageSources[strings.ToLower(pkgName)] = project.FilePath
	if m.results == nil {
		m.results = make(map[string]nugetResult)
	}
	if m.search.fetchedInfo != nil {
		m.results[pkgName] = nugetResult{pkg: m.search.fetchedInfo, source: m.search.fetchedSource}
		m.search.fetchedInfo = nil
		m.search.fetchedSource = ""
	}
	m.rebuildPackageRows()
	for i, row := range m.packageRows {
		if strings.EqualFold(row.ref.Name, pkgName) {
			m.packageCursor = i
			break
		}
	}
	m.clampOffset()
	m.refreshDetail()
	m.focus = focusPackages
	filePath := project.FilePath
	return func() bubble_tea.Msg {
		logInfo("AddPackageReference: %s %s → %s", pkgName, version, filePath)
		if err := AddPackageReference(filePath, pkgName, version); err != nil {
			return writeResultMsg{err: err}
		}
		return writeResultMsg{err: nil}
	}
}

func (m *Model) handleConfirmKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		m.overlayWidthOffset -= 4
		return nil
	case "]":
		m.overlayWidthOffset += 4
		return nil
	case "esc", "n", "q":
		m.overlayWidthOffset = 0
		m.confirm.active = false
		m.statusLine = ""
	case "enter", "y":
		m.overlayWidthOffset = 0
		m.confirm.active = false
		return m.removePackage(m.confirm.pkgName)
	}
	return nil
}

func (m *Model) removePackage(pkgName string) bubble_tea.Cmd {
	targetProject := m.selectedProject() // nil = all projects
	type writeTarget struct {
		filePath string
	}
	var toWrite []writeTarget
	var propsSource string

	// Determine which projects to operate on.
	projects := m.parsedProjects
	if targetProject != nil {
		if m.isPropsProject(targetProject) {
			projects = []*ParsedProject{targetProject}
		} else {
			projects = []*ParsedProject{targetProject}
		}
	}

	for _, p := range projects {
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, pkgName) {
				sourceFile := p.SourceFileForPackage(pkgName)
				p.Packages.Remove(ref)
				if sourceFile != "" {
					toWrite = append(toWrite, writeTarget{filePath: sourceFile})
					if strings.HasSuffix(strings.ToLower(sourceFile), ".props") {
						propsSource = sourceFile
					}
				}
				delete(p.PackageSources, strings.ToLower(pkgName))
				break
			}
		}
	}

	// When the package lived in a .props file, propagate the removal to
	// every other project that inherited it from the same file.
	if propsSource != "" {
		for _, p := range m.allProjects() {
			if p.SourceFileForPackage(pkgName) != propsSource {
				continue
			}
			for ref := range p.Packages {
				if strings.EqualFold(ref.Name, pkgName) {
					p.Packages.Remove(ref)
					delete(p.PackageSources, strings.ToLower(pkgName))
					break
				}
			}
		}
	}

	// Clean up results cache if the package is gone from every project.
	stillExists := false
	for _, p := range m.allProjects() {
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, pkgName) {
				stillExists = true
				break
			}
		}
		if stillExists {
			break
		}
	}
	if !stillExists {
		delete(m.results, pkgName)
	}

	m.rebuildPackageRows()
	if m.packageCursor >= len(m.packageRows) && len(m.packageRows) > 0 {
		m.packageCursor = len(m.packageRows) - 1
	}
	m.clampOffset()
	m.refreshDetail()

	logInfo("removePackage: %s (%d file(s) to write)", pkgName, len(toWrite))
	if len(toWrite) == 0 {
		return nil
	}
	return func() bubble_tea.Msg {
		seen := make(map[string]bool)
		for _, wt := range toWrite {
			if seen[wt.filePath] {
				continue
			}
			seen[wt.filePath] = true
			logDebug("RemovePackageReference: %s from %s", pkgName, wt.filePath)
			if err := RemovePackageReference(wt.filePath, pkgName); err != nil {
				logWarn("remove failed for %s: %v", wt.filePath, err)
				return writeResultMsg{err: err}
			}
		}
		return writeResultMsg{err: nil}
	}
}

func (m *Model) handleDepTreeKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		m.overlayWidthOffset -= 4
		return nil
	case "]":
		m.overlayWidthOffset += 4
		return nil
	case "esc", "q":
		m.overlayWidthOffset = 0
		m.depTree.active = false
		m.statusLine = ""
		return nil
	default:
		var cmd bubble_tea.Cmd
		m.depTree.vp, cmd = m.depTree.vp.Update(msg)
		return cmd
	}
}

func (m Model) renderConfirmOverlay() string {
	w := clampW(48+m.overlayWidthOffset, 36, m.width-4)
	lines := []string{
		styleRedBold.Render("Remove package?"),
		styleSubtle.Render(m.confirm.pkgName),
	}
	box := styleOverlayDanger.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) selectedProject() *ParsedProject {
	if m.projectCursor >= 0 && m.projectCursor < len(m.projectItems) {
		return m.projectItems[m.projectCursor].project
	}
	return nil
}

func (m *Model) rebuildPackageRows() {
	if m.results == nil {
		return
	}

	var rows []packageRow
	sel := m.selectedProject()

	if sel == nil {
		// All Projects — merge by package name
		type group struct {
			refs    []PackageReference
			project *ParsedProject
		}
		grouped := make(map[string]*group)

		for _, p := range m.parsedProjects {
			for ref := range p.Packages {
				g, ok := grouped[ref.Name]
				if !ok {
					g = &group{project: p}
					grouped[ref.Name] = g
				}
				g.refs = append(g.refs, ref)
			}
		}

		for name, g := range grouped {
			res := m.results[name]

			newest := g.refs[0].Version
			oldest := g.refs[0].Version
			for _, ref := range g.refs[1:] {
				if ref.Version.IsNewerThan(newest) {
					newest = ref.Version
				}
				if oldest.IsNewerThan(ref.Version) {
					oldest = ref.Version
				}
			}

			row := packageRow{
				ref:      PackageReference{Name: name, Version: newest},
				project:  g.project,
				info:     res.pkg,
				source:   res.source,
				err:      res.err,
				diverged: oldest != newest,
				oldest:   oldest,
			}
			if res.pkg != nil {
				row.latestCompatible = res.pkg.LatestStableForFramework(g.project.TargetFrameworks)
				row.latestStable = res.pkg.LatestStable()
				row.deprecated = res.pkg.Deprecated
				// Check both newest and oldest versions for vulnerabilities
				// so the icon reflects the worst case across all projects.
				for _, v := range res.pkg.Versions {
					vs := v.SemVer.String()
					if vs == newest.String() || vs == oldest.String() {
						if len(v.Vulnerabilities) > 0 {
							row.vulnerable = true
							break
						}
					}
				}
			}
			rows = append(rows, row)
		}
	} else {
		for ref := range sel.Packages {
			res := m.results[ref.Name]
			row := packageRow{
				ref:     ref,
				project: sel,
				info:    res.pkg,
				source:  res.source,
				err:     res.err,
			}
			if res.pkg != nil {
				row.latestCompatible = res.pkg.LatestStableForFramework(sel.TargetFrameworks)
				row.latestStable = res.pkg.LatestStable()
				row.deprecated = res.pkg.Deprecated
				for _, v := range res.pkg.Versions {
					if v.SemVer.String() == ref.Version.String() {
						row.vulnerable = len(v.Vulnerabilities) > 0
						break
					}
				}
			}
			rows = append(rows, row)
		}
	}

	switch m.packageSortMode {
	case sortByName:
		sortPackageRowsByName(rows)
	case sortBySource:
		sortPackageRowsByName(rows)
		sortPackageRowsBySource(rows)
	case sortByPublished:
		sortPackageRowsByName(rows)
		sortPackageRowsByPublished(rows)
	default: // sortByStatus
		sortPackageRowsByName(rows)
		sortPackageRowsByStatus(rows)
	}

	if !m.packageSortDir {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}

	m.packageRows = rows
	if m.packageCursor >= len(rows) {
		m.packageCursor = imax(0, len(rows)-1)
	}
	m.clampOffset()
}

func sortPackageRowsByName(rows []packageRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].ref.Name < rows[j-1].ref.Name; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

func sortPackageRowsByStatus(rows []packageRow) {
	priority := func(r packageRow) int {
		if r.err != nil {
			return 0
		}
		if r.vulnerable {
			return 1
		}
		ver := r.effectiveVersion()
		check := r.latestCompatible
		if check == nil {
			check = r.latestStable
		}
		if check != nil && check.SemVer.IsNewerThan(ver) {
			return 2
		}
		if r.deprecated {
			return 3
		}
		return 4
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && priority(rows[j]) < priority(rows[j-1]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

func sortPackageRowsBySource(rows []packageRow) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].source < rows[j-1].source; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// sortPackageRowsByPublished sorts by the published date of the installed
// version's latest compatible (or latest stable) match, newest first.
func sortPackageRowsByPublished(rows []packageRow) {
	published := func(r packageRow) time.Time {
		v := r.latestCompatible
		if v == nil {
			v = r.latestStable
		}
		if v != nil {
			return v.Published
		}
		return time.Time{}
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && published(rows[j]).Before(published(rows[j-1])); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

func (m *Model) refreshDetail() {
	if m.packageCursor >= len(m.packageRows) {
		m.detailView.SetContent("")
		return
	}
	m.detailView.SetContent(m.renderDetail(m.packageRows[m.packageCursor]))
	m.detailView.GotoTop()
}

func (m *Model) clampOffset() {
	visible := m.packageListHeight()
	if m.packageCursor < m.packageOffset {
		m.packageOffset = m.packageCursor
	}
	pad := 0
	if m.packageCursor == len(m.packageRows)-1 {
		pad = 1
	}
	if m.packageCursor+pad >= m.packageOffset+visible {
		m.packageOffset = m.packageCursor + pad - visible + 1
	}
}

func (m Model) footerKeys() []struct{ k, v string } {
	type kv = struct{ k, v string }

	// Overlay contexts — show only the overlay's keys.
	if m.depTree.active {
		return []kv{{"↑↓", "scroll"}, {"esc", "close"}}
	}
	if m.search.active {
		return []kv{{"↑↓", "nav"}, {"enter", "select"}, {"esc", "close"}}
	}
	if m.picker.active {
		return []kv{{"↑↓", "nav"}, {"u/U", "update/all"}, {"esc", "close"}}
	}
	if m.confirm.active {
		return []kv{{"enter/y", "confirm"}, {"esc", "cancel"}}
	}
	if m.showSources {
		return []kv{{"esc", "close"}}
	}
	if m.showHelp {
		return []kv{{"↑↓", "scroll"}, {"esc", "close"}}
	}

	// Main screen — varies by focused panel.
	isAllProjects := m.selectedProject() == nil

	switch m.focus {
	case focusProjects:
		return []kv{
			{"tab/↑↓", "nav"},
			{"enter", "packages"},
			{"r/R", "restore/all"},
			{"T", "deps"},
			{"/", "add"},
			{"?", "help"},
			{"esc/q", "quit"},
		}

	case focusPackages:
		if isAllProjects {
			return []kv{
				{"tab/↑↓", "nav"},
				{"u/U", "update compat"},
				{"a/A", "update stable"},
				{"v", "version"},
				{"d", "del"},
				{"o/O", "sort/dir"},
				{"t/T", "deps"},
				{"r/R", "restore"},
				{"/", "add"},
				{"?", "help"},
				{"esc/q", "quit"},
			}
		}
		return []kv{
			{"tab/↑↓", "nav"},
			{"u/U", "update/all"},
			{"a/A", "stable/all"},
			{"v", "version"},
			{"d", "del"},
			{"o/O", "sort/dir"},
			{"t/T", "deps"},
			{"r/R", "restore/all"},
			{"/", "add"},
			{"?", "help"},
			{"esc/q", "quit"},
		}

	case focusDetail:
		return []kv{
			{"tab", "focus"},
			{"↑↓", "scroll"},
			{"r/R", "restore/all"},
			{"?", "help"},
			{"esc/q", "quit"},
		}

	case focusLog:
		return []kv{
			{"tab", "focus"},
			{"↑↓", "scroll"},
			{"l", "close"},
			{"?", "help"},
			{"esc/q", "quit"},
		}
	}

	return []kv{{"?", "help"}, {"esc/q", "quit"}}
}

func (m *Model) footerLines() int {
	keys := m.footerKeys()
	w := m.layoutWidth() - 4
	lines, curW := 1, 0
	for _, pair := range keys {
		ew := lipgloss.Width(pair.k) + 1 + lipgloss.Width(pair.v)
		needed := ew
		if curW > 0 {
			needed += 5
		}
		if curW+needed > w && curW > 0 {
			lines++
			curW = ew
		} else {
			curW += needed
		}
	}
	return lines + 1 // +1 for status row
}

func (m *Model) bodyOuterHeight() int {
	// 3 = header row (1) + header border (1) + footer border (1)
	h := m.height - 3 - m.footerLines()
	if m.showLogs {
		h -= logPanelOuterHeight
	}
	return imax(4, h)
}

func (m *Model) packageListHeight() int {
	// 2 = column header (1) + divider (1)
	return imax(1, m.bodyOuterHeight()-2)
}

func (m *Model) projectListHeight() int {
	// 2 = title row (1) + divider row (1)
	// each item = 3 lines (title + desc + spacing), last item needs only 2
	avail := m.bodyOuterHeight() - 2
	if avail < 2 {
		return 1
	}
	return 1 + (avail-2)/3
}

func (m *Model) clampProjectOffset() {
	visible := m.projectListHeight()
	if m.projectCursor < m.projectOffset {
		m.projectOffset = m.projectCursor
	}
	if m.projectCursor >= m.projectOffset+visible {
		m.projectOffset = m.projectCursor - visible + 1
	}
}

func (m *Model) relayout() {
	_, _, rightW := m.panelWidths()
	innerH := m.bodyOuterHeight() - 2
	m.detailView.Width = rightW - 4
	m.detailView.Height = innerH
	if m.showLogs {
		m.logView.Width = m.layoutWidth() - 6
		m.logView.Height = logPanelLines
	}
}

func (m *Model) panelWidths() (left, mid, right int) {
	lw := m.layoutWidth()
	const borders = 6 // 2 per panel (left+right rounded border chars)

	left = 30 + m.leftWidthOffset
	right = 50 + m.rightWidthOffset
	if left < 10 {
		left = 10
	}
	if right < 10 {
		right = 10
	}
	mid = lw - left - right - borders

	if mid > 130 {
		// Cap the package list — beyond this the columns just float apart.
		mid = 130
		right = lw - left - mid - borders
	}

	// Shrink panels proportionally when the terminal is too narrow.
	minLeft, minMid, minRight := 10, 10, 10
	total := left + mid + right + borders
	if total > lw {
		// First shrink right panel
		right = lw - left - borders - mid
		if right < minRight {
			right = minRight
		}
		mid = lw - left - right - borders
		// Then shrink left panel if still too wide
		if mid < minMid {
			left = lw - minMid - right - borders
			if left < minLeft {
				left = minLeft
			}
			mid = lw - left - right - borders
		}
		if mid < minMid {
			mid = minMid
		}
	}

	return
}

func (m Model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	if m.loading {
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			styleAccent.Render(
				fmt.Sprintf("%s Loading packages... (%d/%d)", m.spinner.View(), m.loadingDone, m.loadingTotal),
			),
		)
	}

	footer := m.renderFooter()
	footerH := lipgloss.Height(footer)

	// Overlay views — render in the space above the footer.
	var overlay string
	switch {
	case m.depTree.active:
		overlay = m.renderDepTreeOverlay()
	case m.search.active:
		overlay = m.renderSearchOverlay()
	case m.picker.active:
		overlay = m.renderPickerOverlay()
	case m.confirm.active:
		overlay = m.renderConfirmOverlay()
	case m.showSources:
		overlay = m.renderSourcesOverlay()
	case m.showHelp:
		overlay = m.renderHelpOverlay()
	}

	if overlay != "" {
		// Overlay renderers use overlayHeight() to fit above the footer.
		// Safety trim in case the overlay output still exceeds the space.
		overlayLines := strings.Split(overlay, "\n")
		maxLines := m.height - footerH
		if len(overlayLines) > maxLines {
			overlayLines = overlayLines[:maxLines]
		}
		trimmed := strings.Join(overlayLines, "\n")
		return trimmed + "\n" + footer
	}

	leftW, midW, rightW := m.panelWidths()

	left := m.renderProjectPanel(leftW)
	mid := m.renderPackagePanel(midW)
	right := m.renderDetailPanel(rightW)

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, mid, right)

	parts := []string{m.renderHeader(), body}
	if m.showLogs {
		parts = append(parts, m.renderLogPanel())
	}
	parts = append(parts, footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Center the content when the terminal is wider than the max layout width.
	if m.width > m.layoutWidth() {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, content)
	}
	return content
}

func (m Model) renderHeader() string {
	title := styleHeaderTitle.Render("◈ GoNuget")
	subtitle := styleSubtle.Render("NuGet package manager")

	return styleHeaderBar.
		Width(m.layoutWidth()).
		Render(lipgloss.JoinHorizontal(lipgloss.Center, title, "  ", subtitle))
}

func (m Model) renderProjectPanel(w int) string {
	focused := m.focus == focusProjects
	innerW := w - 2 // border only, no padding
	visibleH := m.projectListHeight()

	var lines []string

	// Title
	lines = append(lines, " "+styleAccentBold.Render("Projects"))
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	end := m.projectOffset + visibleH
	if end > len(m.projectItems) {
		end = len(m.projectItems)
	}

	for i := m.projectOffset; i < end; i++ {
		item := m.projectItems[i]
		selected := i == m.projectCursor

		title := item.Title()
		desc := item.Description()

		title = truncate(title, innerW-3)
		desc = truncate(desc, innerW-5)

		if selected && focused {
			lines = append(lines, " "+styleAccentBold.Render(title))
			lines = append(lines, "   "+styleSubtle.Render(desc))
		} else {
			lines = append(lines, " "+styleText.Render(title))
			lines = append(lines, "   "+styleMuted.Render(desc))
		}
		if i < end-1 {
			lines = append(lines, "")
		}
	}

	content := strings.Join(lines, "\n")

	s := stylePanelNoPad
	if focused {
		s = s.BorderForeground(colorAccent)
	}
	return s.Width(w).Height(m.bodyOuterHeight()).Render(content)
}

func currentVersionText(row packageRow) string {
	if row.diverged {
		return row.oldest.String() + "–" + row.ref.Version.String()
	}
	return row.ref.Version.String()
}

// availableVersionText returns the plain text for the merged available column.
func availableVersionText(row packageRow) string {
	if row.latestCompatible == nil {
		return "-"
	}
	compat := row.latestCompatible.SemVer.String()
	if row.latestStable != nil && row.latestStable.SemVer.String() != compat {
		return compat + " (" + row.latestStable.SemVer.String() + ")"
	}
	return compat
}

// renderAvailableVersion returns the styled string for the merged available column.
func renderAvailableVersion(row packageRow) string {
	if row.latestCompatible == nil {
		return styleSubtle.Render("-")
	}
	compat := row.latestCompatible.SemVer.String()
	compStyle := styleGreen
	if row.latestCompatible.SemVer.IsNewerThan(row.ref.Version) {
		compStyle = styleYellow
	}
	if row.latestStable != nil && row.latestStable.SemVer.String() != compat {
		latest := row.latestStable.SemVer.String()
		latestStyle := styleGreen
		if row.latestStable.SemVer.IsNewerThan(row.ref.Version) {
			latestStyle = stylePurple
		}
		return compStyle.Render(compat) + " " + styleMuted.Render("(") + latestStyle.Render(latest) + styleMuted.Render(")")
	}
	return compStyle.Render(compat)
}

func (m Model) renderPackagePanel(w int) string {
	focused := m.focus == focusPackages

	visibleH := m.packageListHeight()
	var lines []string

	innerW := w - 4 // border + padding

	const (
		colPrefix = 4 // "▶ " + icon + space
		minNameW  = 20
		colPad    = 2 // padding between columns
	)

	// Compute column widths from actual data.
	colCurrent := len("Current")
	colAvail := len("Available")
	colSource := len("Source")
	for _, row := range m.packageRows {
		if n := len(currentVersionText(row)); n > colCurrent {
			colCurrent = n
		}
		if n := len(availableVersionText(row)); n > colAvail {
			colAvail = n
		}
		if n := len(row.source); n > colSource {
			colSource = n
		}
	}
	colCurrent += colPad
	colAvail += colPad
	colSource += colPad

	// Reserve columns: source hides first, then available.
	budget := innerW - colPrefix - colCurrent
	showSource := budget >= minNameW+colAvail+colSource
	if showSource {
		budget -= colSource
	}
	showAvail := budget >= minNameW+colAvail
	if showAvail {
		budget -= colAvail
	}
	nameW := budget
	if nameW < minNameW {
		nameW = minNameW
	}

	// Header
	hStyle := styleSubtleBold
	sortArrow := "▼"
	if m.packageSortDir {
		sortArrow = "▲"
	}
	pkgHeader := "Package (by " + m.packageSortMode.label() + " " + sortArrow + ")"
	header := "  " + padRight(hStyle.Render(pkgHeader), nameW) +
		padRight(hStyle.Render("Current"), colCurrent)
	if showAvail {
		header += padRight(hStyle.Render("Available"), colAvail)
	}
	if showSource {
		header += hStyle.Render("Source")
	}
	lines = append(lines, header)
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	// rows
	if len(m.packageRows) == 0 {
		lines = append(lines, "")
		lines = append(lines, styleMuted.Render("  No packages found"))
		lines = append(lines, styleMuted.Render("  Press / to search NuGet"))
	}

	end := m.packageOffset + visibleH
	if end > len(m.packageRows) {
		end = len(m.packageRows)
	}

	for i := m.packageOffset; i < end; i++ {
		row := m.packageRows[i]
		selected := i == m.packageCursor

		// icon
		icon := row.statusStyle().Render(row.statusIcon())

		// name
		rawName := truncate(row.ref.Name, nameW-1)
		nameStyle := styleText
		if selected {
			nameStyle = styleAccentBold
		}
		name := padRight(nameStyle.Render(rawName), nameW)

		var current string
		if row.diverged {
			low := styleSubtle.Render(row.oldest.String())
			sep := styleMuted.Render("–")
			high := styleYellow.Render(row.ref.Version.String())
			current = padRight(low+sep+high, colCurrent)
		} else {
			current = padRight(
				styleSubtle.Render(row.ref.Version.String()), colCurrent)
		}

		line := ""
		prefix := "  "
		if selected && focused {
			prefix = styleAccent.Render("▶ ")
		}
		line += prefix + icon + " " + name + current

		// available version (merged compatible + latest)
		if showAvail {
			line += padRight(renderAvailableVersion(row), colAvail)
		}

		if showSource {
			line += styleMuted.Render(row.source)
		}

		lines = append(lines, line)
	}

	for i, line := range lines {
		if lipgloss.Width(line) > innerW {
			lines[i] = truncateStyled(line, innerW)
		}
	}
	content := strings.Join(lines, "\n")

	s := stylePanel
	if focused {
		s = s.BorderForeground(colorAccent)
	}
	return s.Width(w).Height(m.bodyOuterHeight()).
		Render(content)
}

func (m Model) renderDetailPanel(w int) string {
	s := stylePanel
	if m.focus == focusDetail {
		s = s.BorderForeground(colorAccent)
	}

	title := styleAccentBold.Render("Package Detail")
	divider := styleBorder.Render(strings.Repeat("─", w-6))

	content := lipgloss.JoinVertical(lipgloss.Left, title, divider, m.detailView.View())

	return s.Width(w).Height(m.bodyOuterHeight()).
		Render(content)
}

func (m Model) renderDetail(row packageRow) string {
	if row.err != nil {
		return styleRed.Render("Error: " + row.err.Error())
	}
	if row.info == nil {
		return "No data"
	}

	w := m.detailView.Width - 2
	if w < 10 {
		w = 10
	}

	var s strings.Builder

	label := func(text string) string {
		return styleMuted.Render(text)
	}
	value := func(text string) string {
		return styleText.Render(text)
	}

	// name + verified — link to project URL, nuget.org URL, or constructed nuget.org link
	pkgLink := row.info.ProjectURL
	if pkgLink == "" {
		if row.info.NugetOrgURL != "" {
			pkgLink = row.info.NugetOrgURL
		} else if strings.EqualFold(row.source, "nuget.org") {
			pkgLink = "https://www.nuget.org/packages/" + row.info.ID
		}
	}
	name := hyperlink(pkgLink, styleAccentBold.Render(row.info.ID))
	verified := ""
	if row.info.Verified {
		verified = " " + styleGreen.Render("✓ verified")
	}
	s.WriteString(name + verified + "\n\n")

	// description
	if row.info.Description != "" {
		s.WriteString(styleSubtle.
			Render(wordWrap(row.info.Description, w)) + "\n\n")
	}

	// authors
	if row.info.Authors.Len() > 0 {
		var authors []string
		for a := range row.info.Authors {
			authors = append(authors, a)
		}
		s.WriteString(label("Authors") + "\n")
		s.WriteString(value(strings.Join(authors, ", ")) + "\n\n")
	}

	// vulnerabilities in the installed version
	if row.vulnerable {
		var vulns []PackageVulnerability
		for _, v := range row.info.Versions {
			if v.SemVer.String() == row.ref.Version.String() {
				vulns = v.Vulnerabilities
				break
			}
		}
		if len(vulns) > 0 {
			s.WriteString(styleRedBold.Render("Vulnerabilities") + "\n")
			for _, vuln := range vulns {
				sev := vuln.SeverityLabel()
				var sevStyle lipgloss.Style
				switch sev {
				case "critical", "high":
					sevStyle = styleRedBold
				case "moderate":
					sevStyle = styleYellowBold
				default:
					sevStyle = styleTextBold
				}
				sevStr := sevStyle.Render(sev)
				label := hyperlink(vuln.AdvisoryURL, styleSubtle.Render(advisoryLabel(vuln.AdvisoryURL)))
				s.WriteString("  " + sevStr + "  " + label + "\n")
			}
			s.WriteString("\n")
		}
	}

	// deprecation
	if row.info.Deprecated {
		s.WriteString(styleYellowBold.Render("Deprecated") + "\n")
		if row.info.DeprecationMessage != "" {
			s.WriteString(value(wordWrap(row.info.DeprecationMessage, w)) + "\n")
		}
		if row.info.AlternatePackageID != "" {
			s.WriteString(label("Use instead: ") + value(row.info.AlternatePackageID) + "\n")
		}
		s.WriteString("\n")
	}

	// downloads
	s.WriteString(label("Downloads") + "\n")
	s.WriteString(value(formatDownloads(row.info.TotalDownloads)) + "\n\n")

	// last updated
	pub := row.latestCompatible
	if pub == nil {
		pub = row.latestStable
	}
	if pub != nil && !pub.Published.IsZero() {
		s.WriteString(label("Last updated") + "\n")
		s.WriteString(value(timeAgo(pub.Published)) + "\n\n")
	}

	// source — link to the package page on the source
	sourceURL := ""
	for _, svc := range m.nugetServices {
		if strings.EqualFold(svc.SourceName(), row.source) {
			sourceURL = svc.PackageURL(row.info.ID, row.ref.Version.String(), row.info.ProjectURL)
			break
		}
	}
	s.WriteString(label("Source") + "\n")
	s.WriteString(hyperlink(sourceURL, styleSubtle.Render(row.source)) + "\n")
	if row.info.NugetOrgURL != "" && !strings.EqualFold(row.source, "nuget.org") {
		s.WriteString(hyperlink(row.info.NugetOrgURL, styleMuted.Render("nuget.org")) + "\n")
	}
	s.WriteString("\n")

	// show defining file if it's from a .props file
	if sel := m.selectedProject(); sel != nil {
		sourceFile := sel.SourceFileForPackage(row.ref.Name)
		if sourceFile != sel.FilePath {
			s.WriteString(label("Defined in") + "\n")
			s.WriteString(styleCyan.
				Render(filepath.Base(sourceFile)) + "\n\n")
		}
	}

	// diverged project breakdown
	if row.diverged || m.selectedProject() == nil {
		s.WriteString(label("Project versions") + "\n")
		for _, p := range m.parsedProjects {
			for ref := range p.Packages {
				if ref.Name == row.ref.Name {
					proj := styleSubtle.
						Render(fmt.Sprintf("  %-20s", truncate(p.FileName, 20)))
					ver := styleText.
						Render(ref.Version.String())
					line := proj + " " + ver
					sourceFile := p.SourceFileForPackage(ref.Name)
					if sourceFile != p.FilePath {
						line += " " + styleCyan.
							Render("("+filepath.Base(sourceFile)+")")
					}
					s.WriteString(line + "\n")
				}
			}
		}
		s.WriteString("\n")
	}

	// versions — all stable releases + only the latest pre-release
	var displayVersions []PackageVersion
	preAdded := false
	for _, v := range row.info.Versions {
		if v.SemVer.IsPreRelease() {
			if !preAdded {
				displayVersions = append(displayVersions, v)
				preAdded = true
			}
		} else {
			displayVersions = append(displayVersions, v)
		}
	}

	s.WriteString(label("Versions") + "\n")
	const limit = 12 // max version rows shown before "… and N more"

	// Identify versions that must always appear even if beyond the limit:
	// 1. The currently installed version.
	// 2. The latest non-pre-release patch in the same major.minor series.
	installedStr := row.ref.Version.String()
	curMajor, curMinor := row.ref.Version.Major, row.ref.Version.Minor
	latestPatchStr := ""
	for _, v := range displayVersions {
		if v.SemVer.Major == curMajor && v.SemVer.Minor == curMinor && !v.SemVer.IsPreRelease() {
			latestPatchStr = v.SemVer.String()
			break // displayVersions is newest-first
		}
	}

	pinnedSeen := NewSet[string]()
	var pinnedAfter []PackageVersion
	for i, v := range displayVersions {
		if i < limit {
			continue
		}
		vs := v.SemVer.String()
		isPinned := vs == installedStr || (latestPatchStr != "" && vs == latestPatchStr)
		if isPinned && !pinnedSeen.Contains(vs) {
			pinnedSeen.Add(vs)
			pinnedAfter = append(pinnedAfter, v)
		}
	}

	renderVRow := func(v PackageVersion) {
		marker := "  "
		vStyle := styleSubtle

		isCurrent := v.SemVer.String() == installedStr
		isCompat := row.latestCompatible != nil && v.SemVer.String() == row.latestCompatible.SemVer.String()
		isLatest := row.latestStable != nil && v.SemVer.String() == row.latestStable.SemVer.String()

		switch {
		case isCurrent:
			vStyle = styleAccent
			marker = "▶ "
		case isCompat:
			vStyle = styleYellow
			marker = "↑ "
		case isLatest:
			vStyle = stylePurple
			marker = "⬆ "
		}

		extras := ""
		if v.SemVer.IsPreRelease() {
			extras += styleMuted.Render(" pre")
		}
		if v.Downloads > 0 {
			extras += styleMuted.
				Render(fmt.Sprintf(" (%s)", formatDownloads(v.Downloads)))
		}
		verText := vStyle.Render(v.SemVer.String())
		if strings.EqualFold(row.source, "nuget.org") || row.info.NugetOrgURL != "" {
			verURL := "https://www.nuget.org/packages/" + row.info.ID + "/" + v.SemVer.String()
			verText = hyperlink(verURL, verText)
		}
		s.WriteString(vStyle.Render(marker) + verText + extras + "\n")
	}

	for i, v := range displayVersions {
		if i >= limit {
			break
		}
		renderVRow(v)
	}
	if len(displayVersions) > limit {
		hidden := len(displayVersions) - limit - len(pinnedAfter)
		if hidden > 0 {
			s.WriteString(styleMuted.
				Render(fmt.Sprintf("  … and %d more", hidden)) + "\n")
		}
		for _, pv := range pinnedAfter {
			renderVRow(pv)
		}
	}

	// frameworks
	if row.latestCompatible != nil && len(row.latestCompatible.Frameworks) > 0 {
		s.WriteString("\n" + label("Frameworks") + "\n")
		for _, fw := range row.latestCompatible.Frameworks {
			s.WriteString(styleSubtle.
				Render("  "+fw.String()) + "\n")
		}
	}

	return s.String()
}

// versionCompatible returns true when v is usable by all of the project's
// target frameworks. Empty Frameworks on the version means "any framework".
func versionCompatible(v PackageVersion, targets Set[TargetFramework]) bool {
	if targets.Len() == 0 || len(v.Frameworks) == 0 {
		return true
	}
	for target := range targets {
		ok := false
		for _, fw := range v.Frameworks {
			if target.IsCompatibleWith(fw) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// defaultVersionCursor returns the index of the first stable, compatible
// version in a newest-first sorted slice — the natural default selection.
// Falls back to 0 if nothing matches.
func defaultVersionCursor(versions []PackageVersion, targets Set[TargetFramework]) int {
	for i, v := range versions {
		if !v.SemVer.IsPreRelease() && versionCompatible(v, targets) {
			return i
		}
	}
	return 0
}

// refreshHelpView rebuilds the help content and configures the viewport.
// Must be called on a *Model (not value receiver) so changes persist.
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
				{"o", "cycle sort order (status, name, source, published)"},
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
	w := clampW(m.width*60/100+m.overlayWidthOffset, 56, m.width-4)

	content := strings.Join(lines, "\n")
	// Available height for content inside the overlay box:
	// overlay area - border (2) - padding (2) - margin (2)
	maxH := m.overlayHeight() - 6
	if maxH < 8 {
		maxH = 8
	}

	m.helpView.Width = w - 4
	m.helpView.Height = maxH
	m.helpView.SetContent(content)
}

func (m Model) renderHelpOverlay() string {
	w := clampW(m.width*60/100+m.overlayWidthOffset, 56, m.width-4)

	content := m.helpView.View()

	box := styleOverlay.
		Width(w).
		Render(content)

	return lipgloss.Place(m.width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m Model) renderSourcesOverlay() string {
	w := clampW(90+m.overlayWidthOffset, 40, m.width-4)
	innerW := w - 6 // border (2) + padding (2*2)

	var lines []string
	lines = append(lines,
		styleAccentBold.Render("NuGet Sources"),
	)
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	if len(m.sources) == 0 {
		lines = append(lines,
			styleMuted.Render("No sources detected"),
		)
	} else {
		for _, src := range m.sources {
			nameStyle := styleTextBold
			name := nameStyle.Render(truncate(src.Name, innerW-18))
			auth := ""
			if src.Username != "" {
				auth = "  " + styleMuted.Render("🔒 "+src.Username)
			}
			lines = append(lines, name+auth)
			lines = append(lines,
				"  "+hyperlink(src.URL, styleSubtle.Render(truncate(src.URL, innerW-2))),
			)
			lines = append(lines, "")
		}
	}

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m Model) renderSearchOverlay() string {
	w := clampW(90+m.overlayWidthOffset, 56, m.width-4)
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
			m.spinner.View()+" "+
				styleAccent.Render("Fetching package info…"))

	case m.search.loading:
		lines = append(lines,
			m.spinner.View()+" "+
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

	return lipgloss.Place(m.width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m Model) renderPickerOverlay() string {
	w := clampW(76+m.overlayWidthOffset, 40, m.width-4)
	maxVisible := 16
	versions := m.picker.versions

	start := 0
	if m.picker.cursor > maxVisible-1 {
		start = m.picker.cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(versions) {
		end = len(versions)
	}

	// Look up package-level info for deprecation notice and source.
	var pkgInfo *PackageInfo
	var pkgSource string
	if res, ok := m.results[m.picker.pkgName]; ok {
		pkgInfo = res.pkg
		pkgSource = res.source
	}

	var lines []string
	lines = append(lines,
		styleAccentBold.Render("Select version"),
	)
	lines = append(lines,
		styleSubtle.Render(m.picker.pkgName),
	)
	// Deprecation notice directly under the name.
	if pkgInfo != nil && pkgInfo.Deprecated {
		notice := styleYellow.Render("~ deprecated")
		if pkgInfo.AlternatePackageID != "" {
			notice += styleMuted.
				Render("  use: " + pkgInfo.AlternatePackageID)
		}
		lines = append(lines, notice)
	}
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", w-6)),
	)

	for i := start; i < end; i++ {
		v := versions[i]
		selected := i == m.picker.cursor
		compat := versionCompatible(v, m.picker.targets)
		isPre := v.SemVer.IsPreRelease()
		isVulnerable := len(v.Vulnerabilities) > 0

		// Compute highest vulnerability severity for colouring.
		maxSeverity := 0
		for _, vuln := range v.Vulnerabilities {
			if int(vuln.Severity) > maxSeverity {
				maxSeverity = int(vuln.Severity)
			}
		}
		vulnStyle := styleYellow // moderate
		if maxSeverity >= 2 {
			vulnStyle = styleRed // high / critical
		}

		var style lipgloss.Style
		prefix := "  "
		if selected {
			style = styleAccentBold
			prefix = "▶ "
		} else {
			switch {
			case isVulnerable:
				style = vulnStyle
			case !compat:
				style = styleRed
			case isPre:
				style = styleYellow
			default:
				style = styleGreen
			}
		}

		extras := ""
		if isVulnerable {
			extras += styleRed.Render(" ▲")
		}
		if isPre {
			extras += styleMuted.Render(" pre")
		}
		if selected {
			if compat {
				extras += styleGreen.Render(" ✓")
			} else {
				extras += styleRed.Render(" ✗")
			}
		}

		verStr := style.Render(v.SemVer.String())
		if strings.EqualFold(pkgSource, "nuget.org") || (pkgInfo != nil && pkgInfo.NugetOrgURL != "") {
			verURL := "https://www.nuget.org/packages/" + m.picker.pkgName + "/" + v.SemVer.String()
			verStr = hyperlink(verURL, verStr)
		}
		verText := style.Render(prefix) + verStr + extras

		ago := timeAgo(v.Published)
		if ago != "" {
			agoRendered := styleMuted.Render(ago)
			leftW := lipgloss.Width(verText)
			agoW := lipgloss.Width(agoRendered)
			innerW := w - 6
			gap := innerW - leftW - agoW
			if gap > 0 {
				verText += strings.Repeat(" ", gap) + agoRendered
			}
		}
		lines = append(lines, verText)
	}

	lines = append(lines, "")
	legend := styleGreen.Render("■") + " compat  " +
		styleYellow.Render("■") + " pre  " +
		styleRed.Render("■") + " incompat  " +
		styleRed.Render("▲") + " vuln"
	lines = append(lines, styleMuted.Render(legend))

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (m *Model) updateLogView() {
	var colored []string
	for _, line := range m.logLines {
		colored = append(colored, colorizeLogLine(line))
	}
	m.logView.SetContent(strings.Join(colored, "\n"))
	m.logView.GotoBottom()
}

func colorizeLogLine(line string) string {
	switch {
	case strings.HasPrefix(line, "[TRACE]"):
		return styleMuted.Render(line)
	case strings.HasPrefix(line, "[DEBUG]"):
		return styleCyan.Render(line)
	case strings.HasPrefix(line, "[INFO]"):
		return styleGreen.Render(line)
	case strings.HasPrefix(line, "[WARN]"):
		return styleYellow.Render(line)
	case strings.HasPrefix(line, "[ERROR]"), strings.HasPrefix(line, "[FATAL]"):
		return styleRed.Render(line)
	default:
		return styleText.Render(line)
	}
}

func (m Model) renderLogPanel() string {
	s := stylePanel
	if m.focus == focusLog {
		s = s.BorderForeground(colorAccent)
	}

	title := styleAccentBold.Render("Logs")
	div := styleBorder.Render(strings.Repeat("─", m.layoutWidth()-6))
	content := lipgloss.JoinVertical(lipgloss.Left, title, div, m.logView.View())

	return s.Width(m.layoutWidth()).
		Render(content)
}

func (m Model) renderFooter() string {
	keys := m.footerKeys()

	w := m.layoutWidth() - 4 // padding
	var lines []string
	var cur []string
	curW := 0
	sep := "  ·  "
	sepW := 5

	for _, pair := range keys {
		k := styleAccentBold.Render(pair.k)
		v := styleSubtle.Render(pair.v)
		entry := k + " " + v
		entryW := lipgloss.Width(pair.k) + 1 + lipgloss.Width(pair.v)

		needed := entryW
		if len(cur) > 0 {
			needed += sepW
		}
		if curW+needed > w && len(cur) > 0 {
			lines = append(lines, strings.Join(cur, sep))
			cur = nil
			curW = 0
		}
		cur = append(cur, entry)
		if curW > 0 {
			curW += sepW
		}
		curW += entryW
	}
	if len(cur) > 0 {
		lines = append(lines, strings.Join(cur, sep))
	}
	keybinds := strings.Join(lines, "\n")

	// Status line — always reserve the row so height is stable.
	statusStr := ""
	if m.restoring {
		statusStr = m.spinner.View() + styleAccent.Render(" restoring...")
	} else if m.statusLine != "" {
		s := styleGreen
		if m.statusIsErr {
			s = styleRed
		}
		statusStr = s.Render(m.statusLine)
	}

	return styleFooterBar.
		Width(m.layoutWidth()).
		Render(statusStr + "\n" + keybinds)
}

// padRight pads a styled string to the given visible width.
// Uses lipgloss.Width to measure, ignoring ANSI escape codes.
func timeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	days := int(d.Hours() / 24)
	if days < 1 {
		return "today"
	}
	months := days / 30
	years := days / 365
	if years > 0 {
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
	if months > 0 {
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	}
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

func clampW(w, minW, maxW int) int {
	if w < minW {
		w = minW
	}
	if w > maxW {
		w = maxW
	}
	if w < 10 {
		w = 10
	}
	return w
}

func padRight(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func truncateStyled(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	var visible int
	var result strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			result.WriteRune(r)
			continue
		}
		if inEsc {
			result.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if visible >= n {
			break
		}
		result.WriteRune(r)
		visible++
	}
	result.WriteString("\x1b[0m")
	return result.String()
}

// hyperlinkEnabled controls whether OSC 8 escape codes are emitted.
// Disabled when --no-color is active.
var hyperlinkEnabled = true

// hyperlink wraps text in an OSC 8 terminal hyperlink.
// Unsupported terminals silently ignore the escape codes.
func hyperlink(url, text string) string {
	if !hyperlinkEnabled || url == "" {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\ ↗"
}

// advisoryLabel extracts a short display label from an advisory URL.
// e.g. "https://github.com/advisories/GHSA-5crp-9r3c-p9vr" → "GHSA-5crp-9r3c-p9vr"
func advisoryLabel(url string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 && i < len(url)-1 {
		return url[i+1:]
	}
	return url
}

func wordWrap(s string, width int) string {
	words := strings.Fields(s)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len()+len(w)+1 > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteString(" ")
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "\n")
}

func formatDownloads(n int) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}
