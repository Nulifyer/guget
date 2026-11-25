package main

import (
	"cmp"
	"github.com/nulifyer/guget/internal/ui"
	"slices"
	"strings"
	"time"

	style "github.com/nulifyer/guget/internal/ui/style"
)

func currentVersionText(row packageRow) string {
	if row.diverged {
		return row.oldest.String() + "–" + row.ref.Version.String()
	}
	if row.ref.Requested != "" {
		if row.ref.Locked {
			return row.ref.Requested + " pin"
		}
		return row.ref.Requested
	}
	if row.ref.Locked {
		return row.ref.Version.String() + " pin"
	}
	return row.ref.Version.String()
}

// availableVersionText returns the plain text for the merged available column.
func availableVersionText(row packageRow) string {
	if row.latestCompatible == nil {
		// No version qualifies as the update target; the a key's choice still
		// follows in parentheses.
		if row.latestStable != nil {
			return "- (" + row.latestStable.SemVer.String() + ")"
		}
		return "-"
	}
	compat := row.latestCompatible.SemVer.String()
	if row.latestStable != nil && row.latestStable.SemVer.String() != compat {
		return compat + availableDependencyMarker(row) + " (" + row.latestStable.SemVer.String() + ")"
	}
	return compat + availableDependencyMarker(row)
}

func availableDependencyMarker(row packageRow) string {
	if len(row.updateRequirements) > 0 {
		return " [deps]"
	}
	return ""
}

// availableVersionParts returns the styled update target and, when the stable
// policy chooses differently, that choice in parentheses (empty otherwise).
func availableVersionParts(row packageRow) (target, stable string) {
	stableStyle := func() style.Style {
		switch {
		case row.latestStable.SemVer.IsNewerThan(row.ref.Version):
			return stylePurple
		case row.ref.Version.IsNewerThan(row.latestStable.SemVer):
			return styleMuted
		default:
			return styleGreen
		}
	}
	parenthesized := func() string {
		return styleMuted.Render("(") + stableStyle().Render(row.latestStable.SemVer.String()) + styleMuted.Render(")")
	}
	if row.latestCompatible == nil {
		if row.latestStable != nil {
			return styleSubtle.Render("-"), parenthesized()
		}
		return styleSubtle.Render("-"), ""
	}
	compat := row.latestCompatible.SemVer.String()
	var compStyle style.Style
	switch {
	case row.latestCompatible.SemVer.IsNewerThan(row.ref.Version):
		compStyle = styleYellow
	case row.ref.Version.IsNewerThan(row.latestCompatible.SemVer):
		compStyle = styleMuted
	default:
		compStyle = styleGreen
	}
	target = compStyle.Render(compat) + styleYellow.Render(availableDependencyMarker(row))
	if row.latestStable != nil && row.latestStable.SemVer.String() != compat {
		stable = parenthesized()
	}
	return target, stable
}

// renderAvailableVersion returns the styled string for the merged available column.
func renderAvailableVersion(row packageRow) string {
	target, stable := availableVersionParts(row)
	if stable != "" {
		return target + " " + stable
	}
	return target
}

// renderAvailableCell fits the Available value into a column of width cells,
// keeping the one-cell gutter the Requested column has. When the pair does
// not fit, the stable choice collapses to "(...)" before the update target
// itself is cut, so a narrow column never shows half a version as a whole one.
func renderAvailableCell(row packageRow, width int) string {
	limit := max(1, width-1)
	target, stable := availableVersionParts(row)
	if stable == "" {
		if style.Width(target) <= limit {
			return target
		}
	} else if style.Width(target)+1+style.Width(stable) <= limit {
		return target + " " + stable
	} else if style.Width(target)+len(" (...)") <= limit {
		return target + " " + styleMuted.Render("(...)")
	}
	if limit <= 3 {
		return truncateStyled(target, limit)
	}
	return truncateStyled(target, limit-3) + styleMuted.Render("...")
}

func renderCurrentVersion(row packageRow, width int) string {
	if row.diverged {
		low := styleSubtle.Render(row.oldest.String())
		sep := styleMuted.Render("–")
		high := styleYellow.Render(row.ref.Version.String())
		return padRight(truncate(low+sep+high, max(1, width-1)), width)
	}
	return padRight(styleSubtle.Render(truncate(currentVersionText(row), max(1, width-1))), width)
}

type packageColumnLayout struct {
	innerW     int
	nameW      int
	currentW   int
	availableW int
	sourceW    int
	showAvail  bool
	showSource bool
}

func (m *App) packageColumns(w int) packageColumnLayout {
	const (
		colPrefix     = 4
		versionColPad = 3
		sourceColPad  = 2
	)
	header, _ := packageSortHeader(m.packages.sortMode, m.packages.sortDir)
	minNameW := max(20, style.Width(header))
	innerW := w - 4
	currentW := len("Requested")
	availableW := len("Available")
	sourceW := len("Source")
	for _, row := range m.packages.rows {
		if n := style.Width(currentVersionText(row)); n > currentW {
			currentW = n
		}
		if n := style.Width(availableVersionText(row)); n > availableW {
			availableW = n
		}
		if n := style.Width(row.source); n > sourceW {
			sourceW = n
		}
	}
	currentW += versionColPad
	availableW += versionColPad
	// A long range or prerelease request must not push the Available column
	// off the panel for every row. When both columns can have at least their
	// header's width, Requested gives way first and its cells show an
	// ellipsis; the detail panel has the request in full.
	requestedFloor := len("Requested") + versionColPad
	if innerW-colPrefix-minNameW-(len("Available")+versionColPad) >= requestedFloor {
		if room := innerW - colPrefix - minNameW - availableW; currentW > room {
			currentW = max(room, min(currentW, requestedFloor))
		}
	}
	// Reserve the package column before expanding requested versions. A long
	// range or preview version must show an ellipsis, never disappear at the
	// right panel edge after the entire row is clipped.
	currentW = min(currentW, max(1, innerW-colPrefix-minNameW))
	sourceW += sourceColPad

	budget := innerW - colPrefix - currentW
	showSource := budget >= minNameW+availableW+sourceW
	if showSource {
		budget -= sourceW
	}
	// A long prerelease or a "target (stable)" pair must not hide the column
	// for every row: narrow it to what the package column can spare, down to
	// the header's width, and let the cells show an ellipsis.
	if spare := budget - minNameW; spare < availableW && spare >= len("Available")+versionColPad {
		availableW = spare
	}
	showAvail := budget >= minNameW+availableW
	if showAvail {
		budget -= availableW
	}
	nameW := budget
	if nameW < minNameW {
		nameW = minNameW
	}
	return packageColumnLayout{
		innerW: innerW, nameW: nameW, currentW: currentW,
		availableW: availableW, sourceW: sourceW,
		showAvail: showAvail, showSource: showSource,
	}
}

func (m *App) setPackageSort(mode packageSortMode) {
	if m.packages.sortMode == mode {
		m.packages.sortDir = !m.packages.sortDir
	} else {
		m.packages.sortMode = mode
		m.packages.sortDir = mode.defaultDir()
	}
	m.refreshPackageSort()
}

func (m *App) cyclePackageSort() {
	m.packages.sortMode = m.packages.sortMode.next()
	m.packages.sortDir = m.packages.sortMode.defaultDir()
	m.refreshPackageSort()
}

func (m *App) reversePackageSort() {
	m.packages.sortDir = !m.packages.sortDir
	m.refreshPackageSort()
}

func (m *App) refreshPackageSort() {
	m.packages.cursor = 0
	m.packages.scroll = 0
	m.rebuildPackageRows()
	m.refreshDetail()
}

func packageSortHeader(mode packageSortMode, ascending bool) (text string, directionOffset int) {
	arrow := "▼"
	if ascending {
		arrow = "▲"
	}
	prefix := "Package [" + mode.label() + "] "
	return prefix + arrow, style.Width(prefix)
}

func (p *packagePanel) Draw(r ui.Rect) string {
	m := p.app
	w := r.W
	focused := m.focus == focusPackages

	visibleH := m.packageListHeight()
	var lines []string

	columns := m.packageColumns(w)
	innerW := columns.innerW
	nameW := columns.nameW
	colCurrent := columns.currentW
	colAvail := columns.availableW
	showAvail := columns.showAvail
	showSource := columns.showSource

	// Header
	hStyle := styleSubtleBold
	pkgHeader, _ := packageSortHeader(m.packages.sortMode, m.packages.sortDir)
	header := "  " + padRight(hStyle.Render(pkgHeader), nameW) +
		padRight(hStyle.Render("Requested"), colCurrent)
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
	if len(m.packages.rows) == 0 {
		lines = append(lines, "")
		lines = append(lines, styleSubtle.Render("  No packages found"))
		lines = append(lines, styleSubtle.Render("  Press / to search NuGet"))
	}

	end := m.packages.scroll + visibleH
	if end > len(m.packages.rows) {
		end = len(m.packages.rows)
	}

	for i := m.packages.scroll; i < end; i++ {
		row := m.packages.rows[i]
		selected := i == m.packages.cursor

		// icon
		icon := row.statusStyle().Render(row.statusIcon())

		// name
		rawName := truncate(row.ref.Name, nameW-1)
		nameStyle := styleText
		if selected {
			nameStyle = styleAccentBold
		}
		name := padRight(nameStyle.Render(rawName), nameW)

		current := renderCurrentVersion(row, colCurrent)

		line := ""
		prefix := "  "
		if selected && focused {
			prefix = styleAccent.Render("▶ ")
		}
		line += prefix + icon + " " + name + current

		// available version (merged compatible + latest)
		if showAvail {
			line += padRight(renderAvailableCell(row, colAvail), colAvail)
		}

		if showSource {
			line += styleSubtle.Render(row.source)
		}

		lines = append(lines, line)
	}

	for i, line := range lines {
		if style.Width(line) > innerW {
			lines[i] = truncateStyled(line, innerW)
		}
	}
	content := strings.Join(lines, "\n")

	s := stylePanel
	if focused {
		s = s.BorderForeground(colorAccent)
	}
	return renderToPanel(s, w, r.H, content)
}

// versionCompatible returns true when v is usable by all of the project's
// target frameworks, directly or through AssetTargetFallback. It uses the same
// decision as assessPackage, so the picker, its default cursor and the
// Available column agree.
func versionCompatible(v PackageVersion, targets Set[TargetFramework]) bool {
	return v.compatibilityFor(targets) != FrameworkIncompatible
}

// defaultVersionCursor returns the index, in a newest-first sorted slice, of
// the version the update-target policy chooses for refs (nil when adding) and
// targets — the natural default selection. Falls back to 0 if nothing
// qualifies.
func defaultVersionCursor(versions []PackageVersion, targets Set[TargetFramework], refs []PackageReference, policy UpdateTarget) int {
	candidate := assessPackage(&PackageInfo{Versions: versions}, refs, targets, policy).Candidate
	if candidate == nil {
		return 0
	}
	for i := range versions {
		if &versions[i] == candidate {
			return i
		}
	}
	return 0
}

// assessRow fills a row's version, vulnerability, and deprecation fields from
// the assessment of refs under the app's policy. latestStable is what the
// stable policy (and the a key) would choose.
func (m *App) assessRow(row *packageRow, refs []PackageReference, targets Set[TargetFramework]) {
	if row.info == nil {
		return
	}
	floors := floorsOf(row.info, refs) // shared by both policies
	assessment := assessFloors(row.info, floors, targets, m.updateTarget)
	stable := assessment
	if m.updateTarget != UpdateTargetStable {
		stable = assessFloors(row.info, floors, targets, UpdateTargetStable)
	}
	row.latestCompatible = assessment.Candidate
	row.latestStable = stable.Candidate
	row.updateTarget = assessment.Target
	row.vulnerable = assessment.Vulnerable()
	row.deprecated = assessment.Deprecated()
	row.deprecation = nil
	if row.deprecated {
		row.deprecation = &assessment.Deprecations[0]
	}
}

// installedUnusable reports whether the feed lists version without support
// for one of targets. A version the feed does not list, or lists without
// frameworks, is never reported: nothing is known against it.
func installedUnusable(info *PackageInfo, version SemVer, targets Set[TargetFramework]) bool {
	if info == nil || !version.IsValid() || targets.Len() == 0 {
		return false
	}
	for i := range info.Versions {
		if info.Versions[i].SemVer.Equal(version) {
			return !versionCompatible(info.Versions[i], targets)
		}
	}
	return false
}

// rowVersion is the version a row treats as current: the reference's known
// version or, for floating and open-ended requests such as "1.*" or
// "(1.0,)", the version restore selects from the package's versions. It stays
// unknown (invalid) when nothing on the feed satisfies the request.
func rowVersion(ref PackageReference, pkg *PackageInfo) SemVer {
	if ref.Version.IsValid() || pkg == nil {
		return ref.Version
	}
	expression := ref.Requested
	if expression == "" {
		expression = ref.Version.Raw
	}
	if resolved, ok := resolveRequestedVersion(expression, pkg.Versions); ok {
		return resolved
	}
	return ref.Version
}

// sameCurrentVersion compares row versions: NuGet equality for known
// versions, identical expressions for unknown ones.
func sameCurrentVersion(a, b SemVer) bool {
	return a.Equal(b) || (!a.IsValid() && !b.IsValid() && a.Raw == b.Raw)
}

func (m *App) rebuildPackageRows() {
	if m.ctx.Results == nil {
		return
	}

	var rows []packageRow
	sel := m.selectedProject()
	frameworks := packageFrameworksForView(m.ctx.ParsedProjects, sel)

	if sel == nil {
		// All Projects — merge by package name
		type group struct {
			refs     []PackageReference
			projects []*ParsedProject // the project of each ref
			project  *ParsedProject
		}
		grouped := make(map[string]*group)

		for _, p := range m.ctx.ParsedProjects {
			for ref := range p.Packages {
				g, ok := grouped[ref.Name]
				if !ok {
					g = &group{project: p}
					grouped[ref.Name] = g
				}
				g.refs = append(g.refs, ref)
				g.projects = append(g.projects, p)
			}
		}

		for name, g := range grouped {
			res := m.ctx.Results[name]

			versions := make([]SemVer, len(g.refs))
			for i, ref := range g.refs {
				versions[i] = rowVersion(ref, res.pkg)
			}
			newest, oldest := versions[0], versions[0]
			diverged := false
			for _, version := range versions[1:] {
				if version.IsNewerThan(newest) || (!newest.IsValid() && version.IsValid()) {
					newest = version
				}
				if oldest.IsNewerThan(version) || (!oldest.IsValid() && version.IsValid()) {
					oldest = version
				}
				diverged = diverged || !sameCurrentVersion(version, versions[0])
			}

			installed := NewSet[string]()
			for _, version := range versions {
				installed.Add(version.versionKey())
			}
			row := packageRow{
				installedVersions: installed,
				ref:               PackageReference{Name: name, Version: newest},
				project:           g.project,
				info:              res.pkg,
				source:            res.source,
				err:               res.err,
				loading:           m.ctx.PendingPackages.Contains(name),
				diverged:          diverged,
				oldest:            oldest,
			}
			m.assessRow(&row, g.refs, frameworks[strings.ToLower(name)])
			for i, version := range versions {
				if installedUnusable(res.pkg, version, g.projects[i].TargetFrameworks) {
					row.unusable = append(row.unusable, unusableInstall{version: version.String(), project: g.projects[i].FileName})
				}
			}
			slices.SortFunc(row.unusable, func(a, b unusableInstall) int { return strings.Compare(a.project, b.project) })
			rows = append(rows, row)
		}
	} else {
		for ref := range sel.Packages {
			res := m.ctx.Results[ref.Name]
			row := packageRow{
				ref:     ref,
				project: sel,
				info:    res.pkg,
				source:  res.source,
				err:     res.err,
				loading: m.ctx.PendingPackages.Contains(ref.Name),
			}
			row.ref.Version = rowVersion(ref, res.pkg)
			m.assessRow(&row, []PackageReference{ref}, frameworks[strings.ToLower(ref.Name)])
			// The same frameworks as the Available column: the selected
			// project's and those of projects sharing the version's owner.
			if installedUnusable(res.pkg, row.ref.Version, frameworks[strings.ToLower(ref.Name)]) {
				row.unusable = []unusableInstall{{version: row.ref.Version.String()}}
			}
			rows = append(rows, row)
		}
	}

	var dependencyReferences updateDependencyReferences
	for i := range rows {
		row := &rows[i]
		if !row.loading && row.err == nil && row.latestCompatible != nil && len(row.latestCompatible.DependencyGroups) > 0 {
			if dependencyReferences == nil {
				dependencyReferences = indexUpdateDependencyReferences(m.ctx.ParsedProjects)
			}
			row.updateRequirements = m.indexedUpdateDependencyRequirements(row.ref.Name, row.latestCompatible, scopeSelected, dependencyReferences)
		}
	}

	switch m.packages.sortMode {
	case sortByName:
		sortPackageRowsByName(rows)
	case sortBySource:
		sortPackageRowsByName(rows)
		sortPackageRowsBySource(rows)
	case sortByCurrent:
		sortPackageRowsByName(rows)
		sortPackageRowsByCurrent(rows)
	case sortByAvailable:
		sortPackageRowsByName(rows)
		sortPackageRowsByAvailable(rows)
	default: // sortByStatus
		sortPackageRowsByName(rows)
		sortPackageRowsByStatus(rows)
	}

	if !m.packages.sortDir {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}

	m.packages.rows = rows
	if m.packages.cursor >= len(rows) {
		m.packages.cursor = imax(0, len(rows)-1)
	}
	m.clampOffset()
}

func sortPackageRowsByName(rows []packageRow) {
	slices.SortStableFunc(rows, func(a, b packageRow) int {
		return strings.Compare(a.ref.Name, b.ref.Name)
	})
}

func sortPackageRowsByStatus(rows []packageRow) {
	priority := func(r packageRow) int {
		if r.err != nil {
			return 0
		}
		if r.vulnerable {
			return 1
		}
		if r.deprecated {
			return 2
		}
		if r.updateTarget != nil {
			return 3
		}
		return 4
	}
	slices.SortStableFunc(rows, func(a, b packageRow) int {
		return cmp.Compare(priority(a), priority(b))
	})
}

func sortPackageRowsBySource(rows []packageRow) {
	slices.SortStableFunc(rows, func(a, b packageRow) int {
		return strings.Compare(a.source, b.source)
	})
}

// sortPackageRowsByCurrent sorts by the published date of the currently
// installed version (newest first).
func sortPackageRowsByCurrent(rows []packageRow) {
	type versionKey struct {
		info    *PackageInfo
		version string
	}
	dates := make(map[versionKey]time.Time, len(rows))
	for _, r := range rows {
		key := versionKey{r.info, r.effectiveVersion().versionKey()}
		if _, ok := dates[key]; ok {
			continue
		}
		dates[key] = time.Time{}
		if r.info != nil {
			for _, v := range r.info.Versions {
				if v.SemVer.versionKey() == key.version {
					dates[key] = v.Published
					break
				}
			}
		}
	}
	published := func(r packageRow) time.Time {
		return dates[versionKey{r.info, r.effectiveVersion().versionKey()}]
	}
	slices.SortStableFunc(rows, func(a, b packageRow) int {
		return published(a).Compare(published(b))
	})
}

// sortPackageRowsByAvailable sorts by the published date of the best available
// upgrade version: latestCompatible → latestStable → latest overall (newest first).
func sortPackageRowsByAvailable(rows []packageRow) {
	published := func(r packageRow) time.Time {
		if v := r.latestCompatible; v != nil {
			return v.Published
		}
		if v := r.latestStable; v != nil {
			return v.Published
		}
		if r.info != nil && len(r.info.Versions) > 0 {
			return r.info.Versions[0].Published
		}
		return time.Time{}
	}
	slices.SortStableFunc(rows, func(a, b packageRow) int {
		return published(a).Compare(published(b))
	})
}

func (m *App) refreshDetail() {
	if m.packages.cursor >= len(m.packages.rows) {
		m.detail.vp.SetContent("")
		return
	}
	m.detail.vp.SetContent(m.renderDetail(m.packages.rows[m.packages.cursor]))
	m.detail.vp.GotoTop()
}

func (m *App) clampOffset() {
	clampListScroll(m.packages.cursor, &m.packages.scroll, m.packageListHeight(), len(m.packages.rows), 1)
}
