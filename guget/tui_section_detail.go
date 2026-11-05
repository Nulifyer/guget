package main

import (
	"fmt"
	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
	"path/filepath"
	"slices"
	"strings"

	style "github.com/nulifyer/guget/internal/ui/style"
)

// advisoryLabel extracts a short display label from an advisory URL.
func advisoryLabel(url string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 && i < len(url)-1 {
		return url[i+1:]
	}
	return url
}

func wordWrap(s string, width int) string {
	return strings.Join(text.Wrap(s, max(1, width)), "\n")
}

func (p *detailPanel) Draw(r ui.Rect) string {
	m := p.app
	w := r.W
	s := stylePanel
	if m.focus == focusDetail {
		s = s.BorderForeground(colorAccent)
	}

	title := styleSubtleBold.Render("Package Detail")
	divider := styleBorder.Render(strings.Repeat("─", w-4))

	content := style.JoinVertical(style.Left, title, divider, m.detail.vp.View())

	return renderToPanel(s, w, r.H, content)
}

func (m *App) renderDetail(row packageRow) string {
	if row.err != nil {
		return styleRed.Render(wordWrap("Error: "+row.err.Error(), max(1, m.detail.vp.Width())))
	}
	if row.loading {
		return m.ctx.Spinner.View() + " " + styleAccent.Render("Loading package data...")
	}
	if row.info == nil {
		return "No data"
	}

	w := m.detail.vp.Width() - 2
	if w < 10 {
		w = 10
	}

	var s strings.Builder
	s.WriteString(m.renderDetailHeader(row, w))
	s.WriteString(m.renderDetailVulnerabilities(row))
	s.WriteString(m.renderDetailDeprecation(row, w))
	s.WriteString(m.renderDetailSource(row))
	s.WriteString(m.renderDetailDefinedIn(row))
	s.WriteString(m.renderDetailProjectVersions(row))
	s.WriteString(m.renderDetailVersionList(row, w))
	s.WriteString(m.renderDetailFrameworks(row))
	return strings.Join(text.Wrap(s.String(), max(1, m.detail.vp.Width())), "\n")
}

func (m *App) renderDetailHeader(row packageRow, w int) string {
	var s strings.Builder

	// name — link to project URL, nuget.org URL, or constructed nuget.org link
	pkgLink := row.info.ProjectURL
	if pkgLink == "" {
		if row.info.NugetOrgURL != "" {
			pkgLink = row.info.NugetOrgURL
		} else if strings.EqualFold(row.source, "nuget.org") {
			pkgLink = "https://www.nuget.org/packages/" + row.info.ID
		}
	}
	name := hyperlink(pkgLink, styleAccentBold.Render(row.info.ID))
	s.WriteString(name + "\n\n")
	s.WriteString(m.renderDetailUpdateRequirements(row))

	// description
	if row.info.Description != "" {
		s.WriteString(styleSubtle.Render(wordWrap(row.info.Description, w)) + "\n\n")
	}

	// authors
	if row.info.Authors.Len() > 0 {
		var authors []string
		for a := range row.info.Authors {
			authors = append(authors, a)
		}
		slices.Sort(authors)
		s.WriteString(styleMuted.Render("Authors") + "\n")
		s.WriteString(styleText.Render(strings.Join(authors, ", ")) + "\n\n")
	}

	return s.String()
}

func (m *App) renderDetailUpdateRequirements(row packageRow) string {
	var s strings.Builder
	if m.selectedProject() != nil && row.ref.Locked {
		s.WriteString(styleYellow.Render("Version pinned") + "\n")
		s.WriteString(styleSubtle.Render("Updating this project asks for confirmation. Bulk updates skip it.") + "\n\n")
	}
	if len(row.updateRequirements) == 0 {
		return s.String()
	}
	s.WriteString(styleYellowBold.Render("Update requirements") + "\n")
	s.WriteString(styleText.Render("For "+row.latestCompatible.SemVer.String()+":") + "\n")
	lastProject := ""
	for _, requirement := range row.updateRequirements {
		if requirement.project.FilePath != lastProject {
			path := requirement.project.FilePath
			if relative, err := filepath.Rel(m.projectDir, path); err == nil {
				path = relative
			}
			s.WriteString(styleSubtle.Render(path) + "\n")
			lastProject = requirement.project.FilePath
		}
		s.WriteString(styleYellow.Render("  "+requirement.packageID+" "+requirement.requiredVersion()) + "\n")
		s.WriteString(styleMuted.Render("  requested "+requirement.requested) + "\n")
	}
	s.WriteString(styleSubtle.Render("Update these dependencies first, then press u again.") + "\n")
	s.WriteString(styleMuted.Render("v/a can override this check; restore and test afterward.") + "\n\n")
	return s.String()
}

func (m *App) renderDetailVulnerabilities(row packageRow) string {
	if !row.vulnerable {
		return ""
	}
	type installedAdvisory struct {
		PackageVulnerability
		version string
	}
	var vulns []installedAdvisory
	for _, v := range row.info.Versions {
		if row.installedVersions.Contains(v.SemVer.String()) || v.SemVer.String() == row.ref.Version.String() {
			for _, advisory := range v.Vulnerabilities {
				vulns = append(vulns, installedAdvisory{advisory, v.SemVer.String()})
			}
		}
	}
	if len(vulns) == 0 {
		return ""
	}

	var s strings.Builder
	s.WriteString(styleRedBold.Render("Vulnerabilities") + "\n")
	for _, vuln := range vulns {
		sev := vuln.SeverityLabel()
		var sevStyle style.Style
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
		s.WriteString("  " + sevStr + "  " + styleSubtle.Render(vuln.version) + "  " + label + "\n")
	}
	s.WriteString("\n")
	return s.String()
}

func (m *App) renderDetailDeprecation(row packageRow, w int) string {
	if !row.info.Deprecated {
		return ""
	}
	var s strings.Builder
	s.WriteString(styleYellowBold.Render("Deprecated") + "\n")
	if row.info.DeprecationMessage != "" {
		s.WriteString(styleText.Render(wordWrap(row.info.DeprecationMessage, w)) + "\n")
	}
	if row.info.AlternatePackageID != "" {
		s.WriteString(styleMuted.Render("Use instead: ") + styleText.Render(row.info.AlternatePackageID) + "\n")
	}
	s.WriteString("\n")
	return s.String()
}

func (m *App) renderDetailSource(row packageRow) string {
	var s strings.Builder

	sourceURL := ""
	for _, svc := range m.ctx.NugetServices {
		if strings.EqualFold(svc.SourceName(), row.source) {
			sourceURL = svc.PackageURL(row.info.ID, row.ref.Version.String(), row.info.ProjectURL)
			break
		}
	}
	s.WriteString(styleMuted.Render("Source") + "\n")
	s.WriteString(hyperlink(sourceURL, styleSubtle.Render(row.source)) + "\n")
	if row.info.NugetOrgURL != "" && !strings.EqualFold(row.source, "nuget.org") {
		s.WriteString(hyperlink(row.info.NugetOrgURL, styleMuted.Render("nuget.org")) + "\n")
	}
	s.WriteString("\n")
	return s.String()
}

func (m *App) renderDetailDefinedIn(row packageRow) string {
	sel := m.selectedProject()
	if sel == nil {
		return ""
	}
	sourceFile := sel.SourceFileForPackage(row.ref.Name)
	if sourceFile == sel.FilePath {
		return ""
	}
	return styleMuted.Render("Defined in") + "\n" +
		styleCyan.Render(filepath.Base(sourceFile)) + "\n\n"
}

func (m *App) renderDetailProjectVersions(row packageRow) string {
	if !row.diverged && m.selectedProject() != nil {
		return ""
	}

	var s strings.Builder
	s.WriteString(styleMuted.Render("Project versions") + "\n")
	for _, p := range m.ctx.ParsedProjects {
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, row.ref.Name) {
				requested := ref.Requested
				if requested == "" {
					requested = ref.Version.String()
				}
				ver := styleText.Render(requested)
				versions := ver
				if ref.Locked {
					versions += styleYellow.Render(" pinned")
				}
				for _, requirement := range row.updateRequirements {
					if requirement.project.FilePath == p.FilePath {
						versions += styleYellow.Render(" [deps]")
						break
					}
				}
				nameWidth := min(20, m.detail.vp.Width()-style.Width(versions)-3)
				line := ""
				if nameWidth >= 8 {
					line = "  " + styleSubtle.Render(padRight(truncate(p.FileName, nameWidth), nameWidth)) + " " + versions
				} else {
					line = styleSubtle.Render(p.FileName) + "\n  " + versions
				}
				sourceFile := p.SourceFileForPackage(ref.Name)
				if sourceFile != p.FilePath {
					line += " " + styleCyan.Render("("+filepath.Base(sourceFile)+")")
				}
				s.WriteString(line + "\n")
			}
		}
	}
	s.WriteString("\n")
	return s.String()
}

func (m *App) renderDetailVersionList(row packageRow, w int) string {
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

	var s strings.Builder
	s.WriteString(styleMuted.Render("Versions") + "\n")
	const limit = 12

	installedStr := row.ref.Version.String()
	oldestStr := ""
	if row.diverged {
		oldestStr = row.oldest.String()
	}
	curMajor, curMinor := row.ref.Version.Major, row.ref.Version.Minor
	latestPatchStr := ""
	for _, v := range displayVersions {
		if v.SemVer.Major == curMajor && v.SemVer.Minor == curMinor && !v.SemVer.IsPreRelease() {
			latestPatchStr = v.SemVer.String()
			break
		}
	}

	pinnedSeen := NewSet[string]()
	var pinnedAfter []PackageVersion
	for i, v := range displayVersions {
		if i < limit {
			continue
		}
		vs := v.SemVer.String()
		isPinned := vs == installedStr || (oldestStr != "" && vs == oldestStr) || (latestPatchStr != "" && vs == latestPatchStr)
		if isPinned && !pinnedSeen.Contains(vs) {
			pinnedSeen.Add(vs)
			pinnedAfter = append(pinnedAfter, v)
		}
	}

	renderVRow := func(v PackageVersion) {
		marker := "  "
		vStyle := styleMuted

		isCurrent := v.SemVer.String() == installedStr || (oldestStr != "" && v.SemVer.String() == oldestStr)
		isCompat := row.latestCompatible != nil && v.SemVer.String() == row.latestCompatible.SemVer.String()
		isLatest := row.latestStable != nil && v.SemVer.String() == row.latestStable.SemVer.String()
		isHighlighted := isCurrent || isCompat || isLatest

		switch {
		case isCurrent:
			vStyle = styleAccent
			marker = "▶ "
		case isCompat:
			vStyle = styleYellow
			marker = "↑ "
		case isLatest:
			vStyle = stylePurple
			marker = "⇑ "
		}

		extras := ""
		if len(v.Vulnerabilities) > 0 {
			extras += styleRed.Render(" ▲")
		}
		if v.SemVer.IsPreRelease() {
			extras += styleMuted.Render(" pre")
		}
		verText := vStyle.Render(v.SemVer.String())
		if strings.EqualFold(row.source, "nuget.org") || row.info.NugetOrgURL != "" {
			verURL := "https://www.nuget.org/packages/" + row.info.ID + "/" + v.SemVer.String()
			verText = hyperlink(verURL, verText)
		}
		line := vStyle.Render(marker) + verText + extras
		if isHighlighted {
			if ago := versionAge(v); ago != "" {
				agoRendered := vStyle.Render(ago)
				leftW := style.Width(line)
				agoW := style.Width(agoRendered)
				if gap := w - leftW - agoW; gap > 0 {
					line += strings.Repeat(" ", gap) + agoRendered
				}
			}
		}
		s.WriteString(line + "\n")
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
			s.WriteString(styleMuted.Render(fmt.Sprintf("  ... and %d more", hidden)) + "\n")
		}
		for _, pv := range pinnedAfter {
			renderVRow(pv)
		}
	}

	return s.String()
}

func (m *App) renderDetailFrameworks(row packageRow) string {
	if row.latestCompatible == nil || len(row.latestCompatible.Frameworks) == 0 {
		return ""
	}
	var s strings.Builder
	s.WriteString("\n" + styleMuted.Render("Frameworks") + "\n")
	for _, fw := range row.latestCompatible.Frameworks {
		s.WriteString(styleSubtle.Render("  "+fw.String()) + "\n")
	}
	return s.String()
}
