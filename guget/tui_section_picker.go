package main

import (
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (s *versionPicker) FooterKeys() []kv {
	return []kv{{"↑↓", "nav"}, {"u/U", "update/all"}, {"esc", "close"}}
}

func (s *versionPicker) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		return nil
	case "]":
		s.Resize(4)
		return nil
	case "esc", "q":
		s.closeOverlay()
		s.addMode = false
		s.targetProject = nil
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(s.versions)-1 {
			s.cursor++
		}
	case "u":
		return s.applyPickerVersion(scopeSelected)
	case "U":
		return s.applyPickerVersion(scopeAll)
	case "enter":
		if v := s.selectedVersion(); v != nil {
			s.closeOverlay()
			if s.addMode && s.targetProject != nil {
				return s.app.openLocationPickerOrAdd(s.pkgName, v.SemVer.String(), s.targetProject)
			}
			return s.app.applyOrConfirmUpdate(s.pkgName, v.SemVer.String(), s.targetProject)
		}
	}
	return nil
}

func (s *versionPicker) applyPickerVersion(scope actionScope) bubble_tea.Cmd {
	v := s.selectedVersion()
	if v == nil {
		return nil
	}
	s.closeOverlay()
	if s.addMode && s.targetProject != nil {
		return s.app.openLocationPickerOrAdd(s.pkgName, v.SemVer.String(), s.targetProject)
	}
	var project *ParsedProject
	if scope == scopeSelected {
		project = s.app.selectedProject()
	}
	return s.app.applyOrConfirmUpdate(s.pkgName, v.SemVer.String(), project)
}

func newVersionPicker(m *App, pkgName string, versions []PackageVersion, targets Set[TargetFramework], project *ParsedProject, addMode bool) versionPicker {
	return versionPicker{
		sectionBase:   sectionBase{app: m, baseWidth: 50, minWidth: 40, maxMargin: 4, active: true},
		pkgName:       pkgName,
		versions:      versions,
		cursor:        defaultVersionCursor(versions, targets),
		targets:       targets,
		addMode:       addMode,
		targetProject: project,
	}
}

func (m *App) openVersionPicker() {
	if m.packages.cursor >= len(m.packages.rows) {
		return
	}
	row := m.packages.rows[m.packages.cursor]
	if row.info == nil {
		return
	}
	m.ctx.StatusLine = ""
	m.picker = newVersionPicker(m, row.ref.Name, row.info.Versions, row.project.TargetFrameworks, m.selectedProject(), false)
}

func (s *versionPicker) Render() string {
	w := s.Width()
	maxVisible := 16
	versions := s.versions

	start := 0
	if s.cursor > maxVisible-1 {
		start = s.cursor - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(versions) {
		end = len(versions)
	}

	// Look up package-level info for deprecation notice and source.
	var pkgInfo *PackageInfo
	var pkgSource string
	if res, ok := s.app.ctx.Results[s.pkgName]; ok {
		pkgInfo = res.pkg
		pkgSource = res.source
	}

	var lines []string
	lines = append(lines,
		styleAccentBold.Render("Select version"),
	)
	lines = append(lines,
		styleSubtle.Render(s.pkgName),
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
		selected := i == s.cursor
		compat := versionCompatible(v, s.targets)
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
			verURL := "https://www.nuget.org/packages/" + s.pkgName + "/" + v.SemVer.String()
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

	return s.centerOverlay(box)
}
