package main

import (
	"fmt"
	"path/filepath"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

func (m Model) renderDetailPanel(w int) string {
	s := stylePanel
	if m.focus == focusDetail {
		s = s.BorderForeground(colorAccent)
	}

	title := styleSubtleBold.Render("Package Detail")
	divider := styleBorder.Render(strings.Repeat("─", w-4))

	content := lipgloss.JoinVertical(lipgloss.Left, title, divider, m.detailView.View())

	return renderToPanel(s, w, m.bodyOuterHeight(), content)
}

func (m Model) renderDetail(row packageRow) string {
	if row.err != nil {
		return styleRed.Render("Error: " + row.err.Error())
	}
	if row.info == nil {
		return "No data"
	}

	w := m.detailView.Width() - 2
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
	s.WriteString(name + "\n\n")

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

	// source — link to the package page on the source
	sourceURL := ""
	for _, svc := range m.ctx.NugetServices {
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
		for _, p := range m.ctx.ParsedProjects {
			for ref := range p.Packages {
				if ref.Name == row.ref.Name {
					proj := styleSubtle.
						Render(fmt.Sprintf("  %-20s", truncate(p.FileName, 20)))
					ver := styleText.Render(ref.Version.String())
					if ref.Locked {
						ver = styleYellow.Render("[") + ver + styleYellow.Render("]")
					}
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
	oldestStr := ""
	if row.diverged {
		oldestStr = row.oldest.String()
	}
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
			marker = "⬆ "
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
			if ago := timeAgo(v.Published); ago != "" {
				agoRendered := vStyle.Render(ago)
				leftW := lipgloss.Width(line)
				agoW := lipgloss.Width(agoRendered)
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
