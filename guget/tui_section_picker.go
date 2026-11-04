package main

import (
	"strings"

	ui "github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
)

func (s *versionPicker) FooterKeys() []kv {
	if s.addMode {
		return []kv{{"↑↓", "nav"}, {"enter", "add"}, {"esc", "close"}}
	}
	return []kv{{"↑↓", "nav"}, {"enter", "select"}, {"u/U", "update/all"}, {"esc", "close"}}
}

func (s *versionPicker) HandleKey(msg ui.KeyEvent) ui.Effect {
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
		if !s.app.canMutate() {
			return s.app.setStatus("Wait for the save or workspace reload to finish", false)
		}
		if v := s.selectedVersion(); v != nil {
			s.closeOverlay()
			if s.addMode {
				return s.routeAddVersion(v.SemVer.String())
			}
			return s.app.applyOrConfirmUpdate(s.pkgName, v.SemVer.String(), s.targetProject)
		}
	}
	return nil
}

// routeAddVersion handles the add-mode flow after a version is selected:
// single project goes to location picker, "All Projects" goes to project picker.
func (s *versionPicker) routeAddVersion(version string) ui.Effect {
	if s.targetProject != nil {
		return s.app.openLocationPickerOrAdd(s.pkgName, version, s.targetProject)
	}
	s.app.openProjectPicker(s.pkgName, version)
	return nil
}

func (s *versionPicker) applyPickerVersion(scope actionScope) ui.Effect {
	if !s.app.canMutate() {
		return s.app.setStatus("Wait for the save or workspace reload to finish", false)
	}
	v := s.selectedVersion()
	if v == nil {
		return nil
	}
	s.closeOverlay()
	if s.addMode {
		return s.routeAddVersion(v.SemVer.String())
	}
	var project *ParsedProject
	if scope == scopeSelected {
		project = s.app.selectedProject()
	}
	return s.app.applyOrConfirmUpdate(s.pkgName, v.SemVer.String(), project)
}

func newVersionPicker(m *App, pkgName string, versions []PackageVersion, targets Set[TargetFramework], project *ParsedProject, addMode bool) versionPicker {
	return versionPicker{
		sectionBase:   sectionBase{app: m, baseWidth: 50, minWidth: 40, maxMargin: 4, active: true, requestID: m.nextRequestID()},
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
	m.picker = newVersionPicker(m, row.ref.Name, row.info.Versions, m.packageTargets(row.ref.Name, scopeSelected), m.selectedProject(), false)
}

func (s *versionPicker) Render() string {
	w := s.Width()
	innerW := w - 6
	regions := []mouseRegion{overlayCloseRegion(w, s.HandleKey)}
	versions := s.versions

	// Look up package-level info for deprecation notice and source.
	var pkgInfo *PackageInfo
	var pkgSource string
	if res, ok := s.app.ctx.Results[s.pkgName]; ok {
		pkgInfo = res.pkg
		pkgSource = res.source
	}

	var lines []string
	lines = append(lines,
		overlayTitleLine("Select version", innerW),
	)
	lines = append(lines,
		styleSubtle.Render(truncate(s.pkgName, innerW)),
	)
	// Deprecation notice directly under the name.
	if pkgInfo != nil && pkgInfo.Deprecated {
		notice := styleYellow.Render("~ deprecated")
		if pkgInfo.AlternatePackageID != "" {
			notice += styleMuted.
				Render("  use: " + pkgInfo.AlternatePackageID)
		}
		lines = append(lines, truncateStyled(notice, innerW))
	}
	if s.app.overlayHeight() >= len(lines)+7 {
		lines = append(lines, styleBorder.Render(strings.Repeat("─", innerW)))
	}
	rowStart := len(lines)
	compact := s.app.overlayHeight() < 14
	trailingLines := 4 // blank, legend, blank, buttons
	if compact {
		trailingLines = 1
	}
	maxVisible := max(1, min(16, s.app.overlayHeight()-rowStart-trailingLines-4))
	start := max(0, s.cursor-maxVisible+1)
	end := min(len(versions), start+maxVisible)

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

		var rowStyle style.Style
		prefix := "  "
		if selected {
			rowStyle = styleAccentBold
			prefix = "▶ "
		} else {
			switch {
			case isVulnerable:
				rowStyle = vulnStyle
			case !compat:
				rowStyle = styleRed
			case isPre:
				rowStyle = styleYellow
			default:
				rowStyle = styleGreen
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

		verStr := rowStyle.Render(v.SemVer.String())
		if strings.EqualFold(pkgSource, "nuget.org") || (pkgInfo != nil && pkgInfo.NugetOrgURL != "") {
			verURL := "https://www.nuget.org/packages/" + s.pkgName + "/" + v.SemVer.String()
			verStr += " " + hyperlinkBare(verURL, styleSubtle.Render("↗"))
		}
		verText := rowStyle.Render(prefix) + verStr + extras

		ago := versionAge(v)
		if ago != "" {
			agoRendered := styleSubtle.Render(ago)
			leftW := style.Width(verText)
			agoW := style.Width(agoRendered)
			innerW := w - 6
			gap := innerW - leftW - agoW
			if gap > 0 {
				verText += strings.Repeat(" ", gap) + agoRendered
			}
		}
		lines = append(lines, verText)
		index := i
		regions = append(regions, mouseRegion{
			rect: mouseRect{x: overlayContentX, y: overlayContentY + rowStart + i - start, w: innerW, h: 1},
			click: func(ui.MouseEvent) ui.Effect {
				s.cursor = index
				return nil
			},
		})
	}

	if !compact {
		lines = append(lines, "")
		legend := styleGreen.Render("■") + " compat  " +
			styleYellow.Render("■") + " pre  " +
			styleRed.Render("■") + " incompat  " +
			styleRed.Render("▲") + " vuln"
		lines = append(lines, styleMuted.Render(legend))
		lines = append(lines, "")
	}
	actionLabel := "Apply"
	if s.addMode {
		actionLabel = "Add"
	}
	buttonLine, buttonRegions := renderOverlayButtons(innerW, len(lines), s.HandleKey,
		overlayButton{label: "Cancel", code: ui.KeyEscape},
		overlayButton{label: actionLabel, code: ui.KeyEnter},
	)
	lines = append(lines, buttonLine)
	regions = append(regions, buttonRegions...)

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))
	regions = append(regions, mouseRegion{
		rect:  mouseRect{x: 1, y: 1, w: w - 2, h: style.Height(box) - 2},
		wheel: verticalWheelHandler(s.HandleKey),
	})

	return s.centerOverlayInteractive(box, regions)
}
