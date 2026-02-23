package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"logger"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─────────────────────────────────────────────
// Color palette
// ─────────────────────────────────────────────

var (
	colorBg      = lipgloss.Color("#0d1117")
	colorSurface = lipgloss.Color("#161b22")
	colorBorder  = lipgloss.Color("#30363d")
	colorMuted   = lipgloss.Color("#484f58")
	colorText    = lipgloss.Color("#e6edf3")
	colorSubtle  = lipgloss.Color("#8b949e")
	colorAccent  = lipgloss.Color("#58a6ff")
	colorGreen   = lipgloss.Color("#3fb950")
	colorYellow  = lipgloss.Color("#d29922")
	colorRed     = lipgloss.Color("#f85149")
	colorPurple  = lipgloss.Color("#bc8cff")
	colorCyan    = lipgloss.Color("#56d7c2")
)

// ─────────────────────────────────────────────
// Log panel dimensions
// ─────────────────────────────────────────────

const (
	logPanelLines       = 6
	logPanelOuterHeight = logPanelLines + 4 // border(2) + title(1) + divider(1)
)

// ─────────────────────────────────────────────
// Panel focus
// ─────────────────────────────────────────────

type focusPanel int

const (
	focusProjects focusPanel = iota
	focusPackages
	focusDetail
	focusLog
)

// ─────────────────────────────────────────────
// Messages
// ─────────────────────────────────────────────

type resultsReadyMsg struct {
	results map[string]nugetResult
}

type writeResultMsg struct {
	err error
}

type restoreResultMsg struct {
	err error
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

// ─────────────────────────────────────────────
// Project list item
// ─────────────────────────────────────────────

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
	return strings.Join(fws, ", ")
}

func (p projectItem) FilterValue() string { return p.name }

// ─────────────────────────────────────────────
// Package row
// ─────────────────────────────────────────────

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
}

func (r packageRow) statusIcon() string {
	if r.err != nil {
		return "✗"
	}
	check := r.latestCompatible
	if check == nil {
		check = r.latestStable
	}
	if check != nil && check.SemVer.IsNewerThan(r.ref.Version) {
		if r.latestStable != nil && r.latestCompatible != nil &&
			r.latestStable.SemVer.IsNewerThan(r.latestCompatible.SemVer) {
			return "⬆"
		}
		return "↑"
	}
	return "✓"
}

func (r packageRow) statusColor() lipgloss.Color {
	if r.err != nil {
		return colorRed
	}
	check := r.latestCompatible
	if check == nil {
		check = r.latestStable
	}
	if check != nil && check.SemVer.IsNewerThan(r.ref.Version) {
		if r.latestStable != nil && r.latestCompatible != nil &&
			r.latestStable.SemVer.IsNewerThan(r.latestCompatible.SemVer) {
			return colorPurple
		}
		return colorYellow
	}
	return colorGreen
}

// ─────────────────────────────────────────────
// Version picker overlay
// ─────────────────────────────────────────────

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

// ─────────────────────────────────────────────
// Package search overlay
// ─────────────────────────────────────────────

type packageSearch struct {
	active          bool
	input           textinput.Model
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

// ─────────────────────────────────────────────
// Confirm remove overlay
// ─────────────────────────────────────────────

type confirmRemove struct {
	active  bool
	pkgName string
}

// ─────────────────────────────────────────────
// Model
// ─────────────────────────────────────────────

type Model struct {
	width  int
	height int
	focus  focusPanel

	parsedProjects []*ParsedProject
	nugetServices  []*NugetService
	results        map[string]nugetResult
	loading        bool
	spinner        spinner.Model

	projectList list.Model

	packageRows   []packageRow
	packageCursor int
	packageOffset int

	detailView viewport.Model

	picker  versionPicker
	search  packageSearch
	confirm confirmRemove
	noColor bool

	statusLine  string
	statusIsErr bool
	restoring   bool

	logLines []string
	logView  viewport.Model
	showLogs bool
}

func NewModel(parsedProjects []*ParsedProject, nugetServices []*NugetService, noColor bool, initialLogLines []string) Model {
	if noColor {
		lipgloss.SetColorProfile(0)
	}

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorAccent)

	items := []list.Item{
		projectItem{name: "All Projects", project: nil},
	}
	for _, p := range parsedProjects {
		items = append(items, projectItem{name: p.FileName, project: p})
	}

	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(colorAccent).
		BorderLeftForeground(colorAccent)
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(colorSubtle).
		BorderLeftForeground(colorAccent)
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.Foreground(colorText)
	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.Foreground(colorMuted)

	l := list.New(items, delegate, 28, 20)
	l.Title = "Projects"
	l.Styles.Title = lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Padding(0, 1)
	l.Styles.TitleBar = lipgloss.NewStyle().Background(colorSurface)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)

	dv := viewport.New(40, 20)
	lv := viewport.New(80, logPanelLines)

	ti := textinput.New()
	ti.Placeholder = "Type a package name…"
	ti.CharLimit = 100
	ti.Width = 44

	return Model{
		parsedProjects: parsedProjects,
		nugetServices:  nugetServices,
		loading:        true,
		spinner:        sp,
		projectList:    l,
		detailView:     dv,
		noColor:        noColor,
		search:         packageSearch{input: ti},
		logLines:       initialLogLines,
		logView:        lv,
	}
}

// ─────────────────────────────────────────────
// Init
// ─────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return m.spinner.Tick
}

// ─────────────────────────────────────────────
// Update
// ─────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case resultsReadyMsg:
		m.loading = false
		m.results = msg.results
		m.rebuildPackageRows()
		m.refreshDetail()

	case writeResultMsg:
		if msg.err != nil {
			m.statusLine = "⚠ Save failed: " + msg.err.Error()
			m.statusIsErr = true
		} else {
			m.statusLine = "✓ Saved"
			m.statusIsErr = false
		}

	case restoreResultMsg:
		m.restoring = false
		if msg.err != nil {
			m.statusLine = "✗ Restore failed: " + msg.err.Error()
			m.statusIsErr = true
		} else {
			m.statusLine = "✓ Restore complete"
			m.statusIsErr = false
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
		m.search.active = false
		m.search.input.Blur()
		m.picker = versionPicker{
			active:        true,
			pkgName:       msg.info.ID,
			versions:      msg.info.Versions,
			cursor:        0,
			targets:       proj.TargetFrameworks,
			addMode:       true,
			targetProject: proj,
		}

	case logLineMsg:
		m.logLines = append(m.logLines, msg.line)
		m.updateLogView()

	case tea.KeyMsg:
		m.statusLine = ""
		if m.search.active {
			cmds = append(cmds, m.handleSearchKey(msg))
			return m, tea.Batch(cmds...)
		}
		if m.picker.active {
			cmds = append(cmds, m.handlePickerKey(msg))
			return m, tea.Batch(cmds...)
		}
		if m.confirm.active {
			cmds = append(cmds, m.handleConfirmKey(msg))
			return m, tea.Batch(cmds...)
		}
		cmds = append(cmds, m.handleKey(msg))
	}

	if !m.picker.active && !m.search.active && !m.confirm.active {
		switch m.focus {
		case focusProjects:
			var cmd tea.Cmd
			prev := m.projectList.Index()
			m.projectList, cmd = m.projectList.Update(msg)
			cmds = append(cmds, cmd)
			if m.projectList.Index() != prev {
				m.packageCursor = 0
				m.packageOffset = 0
				m.rebuildPackageRows()
				m.refreshDetail()
			}
		case focusDetail:
			var cmd tea.Cmd
			m.detailView, cmd = m.detailView.Update(msg)
			cmds = append(cmds, cmd)
		case focusLog:
			if m.showLogs {
				var cmd tea.Cmd
				m.logView, cmd = m.logView.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "q":
		return tea.Quit

	case "tab":
		if m.showLogs {
			m.focus = (m.focus + 1) % 4
		} else {
			m.focus = (m.focus + 1) % 3
		}

	case "shift+tab":
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
			return m.updateSelected(false)
		}

	case "U":
		if m.focus == focusPackages {
			return m.updateSelected(true)
		}

	case "r":
		if m.focus == focusPackages {
			m.openVersionPicker()
		}

	case "a":
		if m.focus == focusPackages {
			return m.updateAllProjects()
		}

	case "R":
		if !m.restoring {
			return m.triggerRestore()
		}

	case "d":
		if m.focus == focusPackages && m.packageCursor < len(m.packageRows) {
			m.confirm = confirmRemove{
				active:  true,
				pkgName: m.packageRows[m.packageCursor].ref.Name,
			}
		}

	case "/":
		if m.selectedProject() == nil {
			m.statusLine = "⚠ Select a specific project to add a package"
			m.statusIsErr = true
			return nil
		}
		m.search = packageSearch{input: m.search.input}
		m.search.input.Reset()
		m.search.active = true
		return m.search.input.Focus()

	case "enter":
		if m.focus == focusProjects {
			m.focus = focusPackages
		}
	}
	return nil
}

func (m *Model) handlePickerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "q":
		m.picker.active = false
		m.picker.addMode = false
		m.picker.targetProject = nil
	case "up", "k":
		if m.picker.cursor > 0 {
			m.picker.cursor--
		}
	case "down", "j":
		if m.picker.cursor < len(m.picker.versions)-1 {
			m.picker.cursor++
		}
	case "enter":
		if v := m.picker.selectedVersion(); v != nil {
			m.picker.active = false
			if m.picker.addMode && m.picker.targetProject != nil {
				return m.addPackageToProject(m.picker.pkgName, v.SemVer.String(), m.picker.targetProject)
			}
			return m.applyVersion(m.picker.pkgName, v.SemVer.String())
		}
	}
	return nil
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.search.active = false
		m.search.input.Blur()
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
					m.statusLine = "⚠ " + selected.ID + " is already in this project"
					m.statusIsErr = true
					m.search.active = false
					m.search.input.Blur()
					return nil
				}
			}
		}
		m.search.fetchingVersion = true
		m.search.err = nil
		return m.fetchPackageCmd(selected.ID)
	}

	// Forward all other keys to the textinput
	var cmd tea.Cmd
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
		return tea.Batch(cmd, m.searchDebounceCmd(newQuery))
	}
	return cmd
}

// ─────────────────────────────────────────────
// Actions
// ─────────────────────────────────────────────

func (m *Model) updateSelected(useLatest bool) tea.Cmd {
	if m.packageCursor >= len(m.packageRows) {
		return nil
	}
	row := m.packageRows[m.packageCursor]
	if row.err != nil {
		return nil
	}
	var target *PackageVersion
	if useLatest {
		target = row.latestStable
	} else {
		target = row.latestCompatible
	}
	if target == nil {
		return nil
	}
	return m.applyVersion(row.ref.Name, target.SemVer.String())
}

func (m *Model) applyVersion(pkgName, version string) tea.Cmd {
	var toWrite []*ParsedProject
	for _, p := range m.parsedProjects {
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
		if changed && p.FilePath != "" {
			toWrite = append(toWrite, p)
		}
	}
	m.rebuildPackageRows()
	m.refreshDetail()

	logger.Info("applyVersion: %s → %s (%d file(s) to write)", pkgName, version, len(toWrite))
	if len(toWrite) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, p := range toWrite {
			logger.Debug("writing %s to %s", pkgName, p.FilePath)
			if err := UpdatePackageVersion(p.FilePath, pkgName, version); err != nil {
				logger.Warn("write failed for %s: %v", p.FilePath, err)
				return writeResultMsg{err: err}
			}
		}
		return writeResultMsg{err: nil}
	}
}

func (m *Model) triggerRestore() tea.Cmd {
	m.restoring = true
	var projects []*ParsedProject
	if sel := m.selectedProject(); sel != nil {
		projects = []*ParsedProject{sel}
	} else {
		projects = m.parsedProjects
	}
	return runDotnetRestore(projects)
}

func runDotnetRestore(projects []*ParsedProject) tea.Cmd {
	return func() tea.Msg {
		var lastErr error
		for _, p := range projects {
			if p.FilePath == "" {
				continue
			}
			logger.Debug("dotnet restore: %s", p.FilePath)
			cmd := exec.Command("dotnet", "restore", p.FilePath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				logger.Warn("restore failed for %s: %v\n%s", p.FilePath, err, strings.TrimSpace(string(out)))
				lastErr = fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
			} else {
				logger.Info("restore succeeded for %s", p.FileName)
			}
		}
		return restoreResultMsg{err: lastErr}
	}
}

func (m *Model) updateAllProjects() tea.Cmd {
	if m.packageCursor >= len(m.packageRows) {
		return nil
	}
	row := m.packageRows[m.packageCursor]
	if row.latestCompatible == nil {
		return nil
	}
	return m.applyVersion(row.ref.Name, row.latestCompatible.SemVer.String())
}

func (m *Model) openVersionPicker() {
	if m.packageCursor >= len(m.packageRows) {
		return
	}
	row := m.packageRows[m.packageCursor]
	if row.info == nil {
		return
	}
	m.picker = versionPicker{
		active:   true,
		pkgName:  row.ref.Name,
		versions: row.info.Versions,
		cursor:   0,
		targets:  row.project.TargetFrameworks,
	}
}

func (m *Model) searchDebounceCmd(query string) tea.Cmd {
	m.search.debounceID++
	id := m.search.debounceID
	return tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg {
		return searchDebounceMsg{id: id, query: query}
	})
}

func (m *Model) doSearchCmd(query string) tea.Cmd {
	services := m.nugetServices
	return func() tea.Msg {
		var lastErr error
		for _, svc := range services {
			results, err := svc.Search(query, 20)
			if err == nil {
				return searchResultsMsg{results: results, query: query}
			}
			lastErr = err
			logger.Warn("search source [%s] failed: %v", svc.SourceName(), err)
		}
		return searchResultsMsg{query: query, err: lastErr}
	}
}

func (m *Model) fetchPackageCmd(id string) tea.Cmd {
	services := m.nugetServices
	return func() tea.Msg {
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

func (m *Model) addPackageToProject(pkgName, version string, project *ParsedProject) tea.Cmd {
	project.Packages.Add(PackageReference{Name: pkgName, Version: ParseSemVer(version)})
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
	return func() tea.Msg {
		logger.Info("AddPackageReference: %s %s → %s", pkgName, version, filePath)
		if err := AddPackageReference(filePath, pkgName, version); err != nil {
			return writeResultMsg{err: err}
		}
		return writeResultMsg{err: nil}
	}
}

func (m *Model) handleConfirmKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "n", "q":
		m.confirm.active = false
	case "enter", "y":
		m.confirm.active = false
		return m.removePackage(m.confirm.pkgName)
	}
	return nil
}

func (m *Model) removePackage(pkgName string) tea.Cmd {
	targetProject := m.selectedProject() // nil = all projects
	var toWrite []*ParsedProject
	for _, p := range m.parsedProjects {
		if targetProject != nil && p != targetProject {
			continue
		}
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, pkgName) {
				p.Packages.Remove(ref)
				if p.FilePath != "" {
					toWrite = append(toWrite, p)
				}
				break
			}
		}
	}

	// Clean up results cache if the package is gone from every project.
	stillExists := false
	for _, p := range m.parsedProjects {
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

	logger.Info("removePackage: %s (%d file(s) to write)", pkgName, len(toWrite))
	if len(toWrite) == 0 {
		return nil
	}
	return func() tea.Msg {
		for _, p := range toWrite {
			logger.Debug("RemovePackageReference: %s from %s", pkgName, p.FilePath)
			if err := RemovePackageReference(p.FilePath, pkgName); err != nil {
				logger.Warn("remove failed for %s: %v", p.FilePath, err)
				return writeResultMsg{err: err}
			}
		}
		return writeResultMsg{err: nil}
	}
}

func (m Model) renderConfirmOverlay() string {
	w := 48
	lines := []string{
		lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render("Remove package?"),
		lipgloss.NewStyle().Foreground(colorSubtle).Render(m.confirm.pkgName),
		"",
		lipgloss.NewStyle().Foreground(colorMuted).Render("enter / y confirm  ·  esc / n cancel"),
	}
	box := lipgloss.NewStyle().
		Width(w).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorRed).
		Background(colorSurface).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// ─────────────────────────────────────────────
// Data helpers
// ─────────────────────────────────────────────

func (m *Model) selectedProject() *ParsedProject {
	if item, ok := m.projectList.SelectedItem().(projectItem); ok {
		return item.project
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
			}
			rows = append(rows, row)
		}
	}

	sortPackageRowsByName(rows)
	sortPackageRowsByStatus(rows)

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
		check := r.latestCompatible
		if check == nil {
			check = r.latestStable
		}
		if check != nil && check.SemVer.IsNewerThan(r.ref.Version) {
			return 1
		}
		return 2
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && priority(rows[j]) < priority(rows[j-1]); j-- {
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
}

func (m *Model) clampOffset() {
	visible := m.packageListHeight()
	if m.packageCursor < m.packageOffset {
		m.packageOffset = m.packageCursor
	}
	if m.packageCursor >= m.packageOffset+visible {
		m.packageOffset = m.packageCursor - visible + 1
	}
}

func (m *Model) bodyOuterHeight() int {
	h := m.height - 4
	if m.showLogs {
		h -= logPanelOuterHeight
	}
	return imax(4, h)
}

func (m *Model) packageListHeight() int {
	return imax(1, m.bodyOuterHeight()-4)
}

func (m *Model) relayout() {
	leftW, _, rightW := m.panelWidths()
	innerH := m.bodyOuterHeight() - 2
	m.projectList.SetSize(leftW-2, innerH)
	m.detailView.Width = rightW - 4
	m.detailView.Height = innerH
	if m.showLogs {
		m.logView.Width = m.width - 6
		m.logView.Height = logPanelLines
	}
}

func (m *Model) panelWidths() (left, mid, right int) {
	left = 30
	right = 38
	mid = m.width - left - right - 6
	if mid < 20 {
		mid = 20
	}
	return
}

// ─────────────────────────────────────────────
// View
// ─────────────────────────────────────────────

func (m Model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	if m.loading {
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			lipgloss.NewStyle().Foreground(colorAccent).Render(
				m.spinner.View()+" Fetching package information...",
			),
		)
	}

	if m.search.active {
		return m.renderSearchOverlay()
	}

	if m.picker.active {
		return m.renderPickerOverlay()
	}

	if m.confirm.active {
		return m.renderConfirmOverlay()
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
	parts = append(parts, m.renderFooter())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m Model) renderHeader() string {
	title := lipgloss.NewStyle().
		Foreground(colorAccent).Bold(true).Padding(0, 2).
		Render("◈ GoNuget")
	subtitle := lipgloss.NewStyle().
		Foreground(colorSubtle).
		Render("NuGet package manager")

	return lipgloss.NewStyle().
		Width(m.width).
		Background(colorSurface).
		BorderBottom(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottomForeground(colorBorder).
		Render(lipgloss.JoinHorizontal(lipgloss.Center, title, "  ", subtitle))
}

func (m Model) renderProjectPanel(w int) string {
	borderColor := colorBorder
	if m.focus == focusProjects {
		borderColor = colorAccent
	}
	return lipgloss.NewStyle().
		Width(w).Height(m.bodyOuterHeight()).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Render(m.projectList.View())
}

func (m Model) renderPackagePanel(w int) string {
	focused := m.focus == focusPackages
	borderColor := colorBorder
	if focused {
		borderColor = colorAccent
	}

	visibleH := m.packageListHeight()
	var lines []string

	// header
	hStyle := lipgloss.NewStyle().Foreground(colorSubtle).Bold(true)
	lines = append(lines,
		"  "+
			padRight(hStyle.Render("Package"), 33)+
			padRight(hStyle.Render("Current"), 19)+
			padRight(hStyle.Render("Compatible"), 13)+
			padRight(hStyle.Render("Latest"), 14)+
			hStyle.Render("Source"),
	)
	lines = append(lines,
		lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", w-4)),
	)

	// rows
	end := m.packageOffset + visibleH
	if end > len(m.packageRows) {
		end = len(m.packageRows)
	}

	for i := m.packageOffset; i < end; i++ {
		row := m.packageRows[i]
		selected := i == m.packageCursor

		// icon
		icon := lipgloss.NewStyle().Foreground(row.statusColor()).Render(row.statusIcon())

		// name
		rawName := truncate(row.ref.Name, 32)
		nameStyle := lipgloss.NewStyle().Foreground(colorText)
		if selected {
			nameStyle = nameStyle.Foreground(colorAccent).Bold(true)
		}
		name := padRight(nameStyle.Render(rawName), 33)

		// current (19 chars wide: 10 version + space + optional 8 warn)
		rawCurrent := truncate(row.ref.Version.String(), 10)
		var current string
		if row.diverged {
			ver := lipgloss.NewStyle().Foreground(colorYellow).Render(rawCurrent)
			warn := lipgloss.NewStyle().Foreground(colorRed).Render(
				"⚠ " + truncate(row.oldest.String(), 6))
			current = padRight(ver+" "+warn, 19)
		} else {
			current = padRight(
				lipgloss.NewStyle().Foreground(colorSubtle).Render(rawCurrent), 19)
		}

		// compatible
		rawComp := "-"
		compColor := colorSubtle
		if row.latestCompatible != nil {
			rawComp = truncate(row.latestCompatible.SemVer.String(), 12)
			if row.latestCompatible.SemVer.IsNewerThan(row.ref.Version) {
				compColor = colorYellow
			} else {
				compColor = colorGreen
			}
		}
		comp := padRight(lipgloss.NewStyle().Foreground(compColor).Render(rawComp), 13)

		// latest
		rawLatest := "-"
		latestColor := colorSubtle
		if row.latestStable != nil {
			rawLatest = truncate(row.latestStable.SemVer.String(), 12)
			if row.latestStable.SemVer.IsNewerThan(row.ref.Version) {
				latestColor = colorPurple
			} else {
				latestColor = colorGreen
			}
		}
		latest := padRight(lipgloss.NewStyle().Foreground(latestColor).Render(rawLatest), 14)

		// source
		src := truncate(row.source, 16)
		source := lipgloss.NewStyle().Foreground(colorMuted).Render(src)

		prefix := "  "
		if selected && focused {
			prefix = lipgloss.NewStyle().Foreground(colorAccent).Render("▶ ")
		}

		line := prefix + icon + " " + name + current + comp + latest + source

		if selected && focused {
			line = lipgloss.NewStyle().
				Background(colorSurface).
				Width(w - 4).
				Render(line)
		}

		lines = append(lines, line)
	}

	content := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Width(w).Height(m.bodyOuterHeight()).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Render(content)
}

func (m Model) renderDetailPanel(w int) string {
	borderColor := colorBorder
	if m.focus == focusDetail {
		borderColor = colorAccent
	}

	title := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Package Detail")
	divider := lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", w-6))

	content := lipgloss.JoinVertical(lipgloss.Left, title, divider, m.detailView.View())

	return lipgloss.NewStyle().
		Width(w).Height(m.bodyOuterHeight()).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Render(content)
}

func (m Model) renderDetail(row packageRow) string {
	if row.err != nil {
		return lipgloss.NewStyle().Foreground(colorRed).Render("Error: " + row.err.Error())
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
		return lipgloss.NewStyle().Foreground(colorMuted).Render(text)
	}
	value := func(text string) string {
		return lipgloss.NewStyle().Foreground(colorText).Render(text)
	}

	// name + verified
	name := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(row.info.ID)
	verified := ""
	if row.info.Verified {
		verified = " " + lipgloss.NewStyle().Foreground(colorGreen).Render("✓ verified")
	}
	s.WriteString(name + verified + "\n\n")

	// description
	if row.info.Description != "" {
		s.WriteString(lipgloss.NewStyle().Foreground(colorSubtle).
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

	// downloads
	s.WriteString(label("Downloads") + "\n")
	s.WriteString(value(formatDownloads(row.info.TotalDownloads)) + "\n\n")

	// source
	s.WriteString(label("Source") + "\n")
	s.WriteString(lipgloss.NewStyle().Foreground(colorSubtle).Render(row.source) + "\n\n")

	// diverged project breakdown
	if row.diverged {
		s.WriteString(label("Project versions") + "\n")
		for _, p := range m.parsedProjects {
			for ref := range p.Packages {
				if ref.Name == row.ref.Name {
					proj := lipgloss.NewStyle().Foreground(colorSubtle).
						Render(fmt.Sprintf("  %-20s", truncate(p.FileName, 20)))
					ver := lipgloss.NewStyle().Foreground(colorText).
						Render(ref.Version.String())
					s.WriteString(proj + " " + ver + "\n")
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
	limit := 12
	for i, v := range displayVersions {
		if i >= limit {
			s.WriteString(lipgloss.NewStyle().Foreground(colorMuted).
				Render(fmt.Sprintf("  … and %d more", len(displayVersions)-limit)) + "\n")
			break
		}

		marker := "  "
		vStyle := lipgloss.NewStyle().Foreground(colorSubtle)

		isCurrent := v.SemVer.String() == row.ref.Version.String()
		isCompat := row.latestCompatible != nil && v.SemVer.String() == row.latestCompatible.SemVer.String()
		isLatest := row.latestStable != nil && v.SemVer.String() == row.latestStable.SemVer.String()

		switch {
		case isCurrent:
			vStyle = vStyle.Foreground(colorAccent)
			marker = "▶ "
		case isCompat:
			vStyle = vStyle.Foreground(colorYellow)
			marker = "↑ "
		case isLatest:
			vStyle = vStyle.Foreground(colorPurple)
			marker = "⬆ "
		}

		extras := ""
		if v.SemVer.IsPreRelease() {
			extras += lipgloss.NewStyle().Foreground(colorMuted).Render(" pre")
		}
		if v.Downloads > 0 {
			extras += lipgloss.NewStyle().Foreground(colorMuted).
				Render(fmt.Sprintf(" (%s)", formatDownloads(v.Downloads)))
		}

		s.WriteString(vStyle.Render(marker+v.SemVer.String()) + extras + "\n")
	}

	// frameworks
	if row.latestCompatible != nil && len(row.latestCompatible.Frameworks) > 0 {
		s.WriteString("\n" + label("Frameworks") + "\n")
		for _, fw := range row.latestCompatible.Frameworks {
			s.WriteString(lipgloss.NewStyle().Foreground(colorSubtle).
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

func (m Model) renderSearchOverlay() string {
	w := 56
	innerW := w - 6 // border (2) + padding (2*2)

	var lines []string

	// Title row
	title := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Add Package")
	proj := m.selectedProject()
	projName := ""
	if proj != nil {
		projName = lipgloss.NewStyle().Foreground(colorSubtle).
			Render("  " + truncate(proj.FileName, innerW-15))
	}
	lines = append(lines, title+projName)

	// Text input
	lines = append(lines, m.search.input.View())

	// Divider
	lines = append(lines,
		lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", innerW)),
	)

	// Body
	maxVisible := 10
	switch {
	case m.search.fetchingVersion:
		lines = append(lines,
			m.spinner.View()+" "+
				lipgloss.NewStyle().Foreground(colorAccent).Render("Fetching package info…"))

	case m.search.loading:
		lines = append(lines,
			m.spinner.View()+" "+
				lipgloss.NewStyle().Foreground(colorSubtle).Render("Searching…"))

	case m.search.err != nil:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(colorRed).Render("✗ "+m.search.err.Error()))

	case len(m.search.results) == 0 && m.search.lastQuery != "":
		lines = append(lines,
			lipgloss.NewStyle().Foreground(colorMuted).Render("No results found"))

	case len(m.search.results) == 0:
		lines = append(lines,
			lipgloss.NewStyle().Foreground(colorMuted).Render("Type to search NuGet…"))

	default:
		// Build already-installed set for the current project
		alreadyHas := NewSet[string]()
		if proj != nil {
			for ref := range proj.Packages {
				alreadyHas.Add(strings.ToLower(ref.Name))
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
			idStyle := lipgloss.NewStyle().Foreground(colorText)
			if selected {
				prefix = lipgloss.NewStyle().Foreground(colorAccent).Render("▶ ")
				idStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
			}

			pkgID := padRight(idStyle.Render(truncate(r.ID, 34)), 35)
			ver := lipgloss.NewStyle().Foreground(colorSubtle).Render(truncate(r.Version, 12))

			suffix := ""
			if alreadyHas.Contains(strings.ToLower(r.ID)) {
				suffix = " " + lipgloss.NewStyle().Foreground(colorMuted).Render("(installed)")
			} else if r.Verified {
				suffix = " " + lipgloss.NewStyle().Foreground(colorGreen).Render("✓")
			}

			line := prefix + pkgID + ver + suffix
			if selected {
				line = lipgloss.NewStyle().
					Background(colorSurface).
					Width(innerW).
					Render(line)
			}
			lines = append(lines, line)
		}
	}

	// Help line
	lines = append(lines, "")
	lines = append(lines,
		lipgloss.NewStyle().Foreground(colorMuted).
			Render("↑/↓ navigate · enter select · esc cancel"),
	)

	box := lipgloss.NewStyle().
		Width(w).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Background(colorSurface).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) renderPickerOverlay() string {
	w := 52
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

	var lines []string
	lines = append(lines,
		lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render("Select version"),
	)
	lines = append(lines,
		lipgloss.NewStyle().Foreground(colorSubtle).
			Render(m.picker.pkgName),
	)
	lines = append(lines,
		lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", w-6)),
	)

	for i := start; i < end; i++ {
		v := versions[i]
		selected := i == m.picker.cursor
		compat := versionCompatible(v, m.picker.targets)
		isPre := v.SemVer.IsPreRelease()

		var style lipgloss.Style
		prefix := "  "
		if selected {
			style = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
			prefix = "▶ "
		} else {
			switch {
			case !compat:
				style = lipgloss.NewStyle().Foreground(colorRed)
			case isPre:
				style = lipgloss.NewStyle().Foreground(colorYellow)
			default:
				style = lipgloss.NewStyle().Foreground(colorGreen)
			}
		}

		extras := ""
		if isPre {
			extras += lipgloss.NewStyle().Foreground(colorMuted).Render(" pre")
		}
		if selected {
			if compat {
				extras += lipgloss.NewStyle().Foreground(colorGreen).Render(" ✓")
			} else {
				extras += lipgloss.NewStyle().Foreground(colorRed).Render(" ✗")
			}
		}

		lines = append(lines, style.Render(prefix+v.SemVer.String())+extras)
	}

	lines = append(lines, "")
	legend := lipgloss.NewStyle().Foreground(colorGreen).Render("■") + " compat  " +
		lipgloss.NewStyle().Foreground(colorYellow).Render("■") + " pre  " +
		lipgloss.NewStyle().Foreground(colorRed).Render("■") + " incompat"
	lines = append(lines, lipgloss.NewStyle().Foreground(colorMuted).Render(legend))
	lines = append(lines,
		lipgloss.NewStyle().Foreground(colorMuted).
			Render("↑/↓ navigate · enter select · esc cancel"),
	)

	box := lipgloss.NewStyle().
		Width(w).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Background(colorSurface).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
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
		return lipgloss.NewStyle().Foreground(colorMuted).Render(line)
	case strings.HasPrefix(line, "[DEBUG]"):
		return lipgloss.NewStyle().Foreground(colorCyan).Render(line)
	case strings.HasPrefix(line, "[INFO]"):
		return lipgloss.NewStyle().Foreground(colorGreen).Render(line)
	case strings.HasPrefix(line, "[WARN]"):
		return lipgloss.NewStyle().Foreground(colorYellow).Render(line)
	case strings.HasPrefix(line, "[ERROR]"), strings.HasPrefix(line, "[FATAL]"):
		return lipgloss.NewStyle().Foreground(colorRed).Render(line)
	default:
		return lipgloss.NewStyle().Foreground(colorText).Render(line)
	}
}

func (m Model) renderLogPanel() string {
	borderColor := colorBorder
	if m.focus == focusLog {
		borderColor = colorAccent
	}

	title := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("Logs")
	divider := lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", m.width-6))
	content := lipgloss.JoinVertical(lipgloss.Left, title, divider, m.logView.View())

	return lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Render(content)
}

func (m Model) renderFooter() string {
	type kv struct{ k, v string }
	keys := []kv{
		{"tab/↑↓", "focus/navigate"},
		{"u/U", "update compat/latest"},
		{"r", "pick version"},
		{"a", "update all"},
		{"R", "restore"},
		{"/", "add pkg"},
		{"d", "del pkg"},
		{"l", "logs"},
		{"q", "quit"},
	}

	var parts []string
	for _, pair := range keys {
		k := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(pair.k)
		v := lipgloss.NewStyle().Foreground(colorSubtle).Render(pair.v)
		parts = append(parts, k+" "+v)
	}

	if m.restoring {
		parts = append(parts, m.spinner.View()+lipgloss.NewStyle().Foreground(colorAccent).Render(" restoring..."))
	} else if m.statusLine != "" {
		c := colorGreen
		if m.statusIsErr {
			c = colorRed
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(c).Render(m.statusLine))
	}

	return lipgloss.NewStyle().
		Width(m.width).
		Background(colorSurface).
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderTopForeground(colorBorder).
		Padding(0, 2).
		Render(strings.Join(parts, "  ·  "))
}

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

// padRight pads a styled string to the given visible width.
// Uses lipgloss.Width to measure, ignoring ANSI escape codes.
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

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
