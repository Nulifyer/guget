package main

import (
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"
)

func currentVersionText(row packageRow) string {
	if row.diverged {
		return row.oldest.String() + "–" + row.ref.Version.String()
	}
	if row.ref.Locked {
		return "[" + row.ref.Version.String() + "]"
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
	var compStyle lipgloss.Style
	switch {
	case row.latestCompatible.SemVer.IsNewerThan(row.ref.Version):
		compStyle = styleYellow
	case row.ref.Version.IsNewerThan(row.latestCompatible.SemVer):
		compStyle = styleMuted
	default:
		compStyle = styleGreen
	}
	if row.latestStable != nil && row.latestStable.SemVer.String() != compat {
		latest := row.latestStable.SemVer.String()
		var latestStyle lipgloss.Style
		switch {
		case row.latestStable.SemVer.IsNewerThan(row.ref.Version):
			latestStyle = stylePurple
		case row.ref.Version.IsNewerThan(row.latestStable.SemVer):
			latestStyle = styleMuted
		default:
			latestStyle = styleGreen
		}
		return compStyle.Render(compat) + " " + styleMuted.Render("(") + latestStyle.Render(latest) + styleMuted.Render(")")
	}
	return compStyle.Render(compat)
}

func (m *App) renderPackagePanel(w int) string {
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
	for _, row := range m.packages.rows {
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
	if m.packages.sortDir {
		sortArrow = "▲"
	}
	pkgHeader := "Package (by " + m.packages.sortMode.label() + " " + sortArrow + ")"
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
	if len(m.packages.rows) == 0 {
		lines = append(lines, "")
		lines = append(lines, styleMuted.Render("  No packages found"))
		lines = append(lines, styleMuted.Render("  Press / to search NuGet"))
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

		var current string
		if row.diverged {
			low := styleSubtle.Render(row.oldest.String())
			sep := styleMuted.Render("–")
			high := styleYellow.Render(row.ref.Version.String())
			current = padRight(low+sep+high, colCurrent)
		} else if row.ref.Locked {
			verText := styleYellow.Render("[") + styleSubtle.Render(row.ref.Version.String()) + styleYellow.Render("]")
			current = padRight(verText, colCurrent)
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
	return renderToPanel(s, w, m.bodyOuterHeight(), content)
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

func (m *App) rebuildPackageRows() {
	if m.ctx.Results == nil {
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

		for _, p := range m.ctx.ParsedProjects {
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
			res := m.ctx.Results[name]

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
				loading:  m.ctx.PendingPackages.Contains(name),
				diverged: oldest != newest,
				oldest:   oldest,
			}
			if res.pkg != nil {
				row.latestCompatible = res.pkg.LatestStableForFramework(g.project.TargetFrameworks)
				row.latestStable = res.pkg.LatestStable()
				row.deprecated = res.pkg.Deprecated
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
			res := m.ctx.Results[ref.Name]
			row := packageRow{
				ref:     ref,
				project: sel,
				info:    res.pkg,
				source:  res.source,
				err:     res.err,
				loading: m.ctx.PendingPackages.Contains(ref.Name),
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
		if r.deprecated {
			return 2
		}
		ver := r.effectiveVersion()
		check := r.latestCompatible
		if check == nil {
			check = r.latestStable
		}
		if check != nil && check.SemVer.IsNewerThan(ver) {
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

// sortPackageRowsByCurrent sorts by the published date of the currently
// installed version (newest first).
func sortPackageRowsByCurrent(rows []packageRow) {
	published := func(r packageRow) time.Time {
		if r.info == nil {
			return time.Time{}
		}
		ver := r.effectiveVersion()
		for i := range r.info.Versions {
			if r.info.Versions[i].SemVer.Raw == ver.Raw {
				return r.info.Versions[i].Published
			}
		}
		return time.Time{}
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && published(rows[j]).Before(published(rows[j-1])); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
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
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && published(rows[j]).Before(published(rows[j-1])); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
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
