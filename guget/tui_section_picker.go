package main

import (
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (m *Model) handlePickerKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		adjustOffset(&m.overlayWidthOffset, -4, m.ctx.Width, 30, m.ctx.Width-4)
		return nil
	case "]":
		adjustOffset(&m.overlayWidthOffset, 4, m.ctx.Width, 30, m.ctx.Width-4)
		return nil
	case "esc", "q":
		m.overlayWidthOffset = 0
		m.picker.active = false
		m.picker.addMode = false
		m.picker.targetProject = nil
		m.ctx.StatusLine = ""
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
				return m.openLocationPickerOrAdd(m.picker.pkgName, v.SemVer.String(), m.picker.targetProject)
			}
			return m.applyOrConfirmUpdate(m.picker.pkgName, v.SemVer.String(), m.picker.targetProject)
		}
	}
	return nil
}

func (m *Model) applyPickerVersion(scope actionScope) bubble_tea.Cmd {
	v := m.picker.selectedVersion()
	if v == nil {
		return nil
	}
	m.overlayWidthOffset = 0
	m.picker.active = false
	if m.picker.addMode && m.picker.targetProject != nil {
		return m.openLocationPickerOrAdd(m.picker.pkgName, v.SemVer.String(), m.picker.targetProject)
	}
	var project *ParsedProject
	if scope == scopeSelected {
		project = m.selectedProject()
	}
	return m.applyOrConfirmUpdate(m.picker.pkgName, v.SemVer.String(), project)
}

func (m *Model) openVersionPicker() {
	if m.packageCursor >= len(m.packageRows) {
		return
	}
	row := m.packageRows[m.packageCursor]
	if row.info == nil {
		return
	}
	m.ctx.StatusLine = ""
	m.picker = versionPicker{
		active:        true,
		pkgName:       row.ref.Name,
		versions:      row.info.Versions,
		cursor:        defaultVersionCursor(row.info.Versions, row.project.TargetFrameworks),
		targets:       row.project.TargetFrameworks, // used for compatibility display only
		targetProject: m.selectedProject(),          // nil = all projects, specific = scoped
	}
}

func (m Model) renderPickerOverlay() string {
	w := clampW(50+m.overlayWidthOffset, 40, m.ctx.Width-4)
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
	if res, ok := m.ctx.Results[m.picker.pkgName]; ok {
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

	return lipgloss.Place(m.ctx.Width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}
