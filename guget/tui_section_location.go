package main

import (
	"path/filepath"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func (m *Model) addPackageToProject(pkgName, version string, project *ParsedProject) bubble_tea.Cmd {
	project.Packages.Add(PackageReference{Name: pkgName, Version: ParseSemVer(version)})
	project.PackageSources[strings.ToLower(pkgName)] = project.FilePath
	if m.ctx.Results == nil {
		m.ctx.Results = make(map[string]nugetResult)
	}
	if m.search.fetchedInfo != nil {
		m.ctx.Results[pkgName] = nugetResult{pkg: m.search.fetchedInfo, source: m.search.fetchedSource}
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

// openLocationPickerOrAdd shows the location picker if the project has multiple
// AddTargets (e.g. Directory.Build.props, CPM, imported props). If the project
// is a .props file or has only one target, it adds directly.
func (m *Model) openLocationPickerOrAdd(pkgName, version string, project *ParsedProject) bubble_tea.Cmd {
	// Props files: add directly, no picker needed.
	if strings.HasSuffix(strings.ToLower(project.FilePath), ".props") {
		return m.addPackageToProject(pkgName, version, project)
	}
	// Only one target (the project itself): add directly.
	if len(project.AddTargets) <= 1 {
		return m.addPackageToProject(pkgName, version, project)
	}
	// Multiple targets: open the location picker.
	m.locationPick = locationPicker{
		active:        true,
		pkgName:       pkgName,
		version:       version,
		targets:       project.AddTargets,
		cursor:        0,
		targetProject: project,
	}
	return nil
}

func (m *Model) handleLocationPickKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		adjustOffset(&m.overlayWidthOffset, -4, m.ctx.Width, 30, m.ctx.Width-4)
		return nil
	case "]":
		adjustOffset(&m.overlayWidthOffset, 4, m.ctx.Width, 30, m.ctx.Width-4)
		return nil
	case "esc", "q":
		m.overlayWidthOffset = 0
		m.locationPick.active = false
		m.ctx.StatusLine = ""
	case "up", "k":
		if m.locationPick.cursor > 0 {
			m.locationPick.cursor--
		}
	case "down", "j":
		if m.locationPick.cursor < len(m.locationPick.targets)-1 {
			m.locationPick.cursor++
		}
	case "enter":
		m.overlayWidthOffset = 0
		m.locationPick.active = false
		selected := m.locationPick.targets[m.locationPick.cursor]
		return m.addPackageToLocation(
			m.locationPick.pkgName,
			m.locationPick.version,
			m.locationPick.targetProject,
			selected,
		)
	}
	return nil
}

// addPackageToLocation adds a package to the specified target location.
// For CPM targets, it performs a dual write: PackageVersion to the CPM file
// and a version-less PackageReference to the project file.
func (m *Model) addPackageToLocation(pkgName, version string, project *ParsedProject, target AddTarget) bubble_tea.Cmd {
	project.Packages.Add(PackageReference{Name: pkgName, Version: ParseSemVer(version)})
	project.PackageSources[strings.ToLower(pkgName)] = target.FilePath

	if m.ctx.Results == nil {
		m.ctx.Results = make(map[string]nugetResult)
	}
	if m.search.fetchedInfo != nil {
		m.ctx.Results[pkgName] = nugetResult{pkg: m.search.fetchedInfo, source: m.search.fetchedSource}
		m.search.fetchedInfo = nil
		m.search.fetchedSource = ""
	}

	// If adding to a shared file, propagate to all projects that also reference it.
	if target.Kind != AddTargetProject {
		for _, p := range m.allProjects() {
			if p == project {
				continue
			}
			for _, at := range p.AddTargets {
				if at.FilePath == target.FilePath {
					p.Packages.Add(PackageReference{Name: pkgName, Version: ParseSemVer(version)})
					p.PackageSources[strings.ToLower(pkgName)] = target.FilePath
					break
				}
			}
		}
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

	projectFilePath := project.FilePath
	targetFilePath := target.FilePath
	targetKind := target.Kind

	return func() bubble_tea.Msg {
		switch targetKind {
		case AddTargetCPM:
			logInfo("AddPackageVersion: %s %s → %s", pkgName, version, targetFilePath)
			if err := AddPackageVersion(targetFilePath, pkgName, version); err != nil {
				return writeResultMsg{err: err}
			}
			logInfo("AddPackageReference (CPM): %s → %s", pkgName, projectFilePath)
			if err := AddPackageReference(projectFilePath, pkgName, ""); err != nil {
				return writeResultMsg{err: err}
			}
		default:
			logInfo("AddPackageReference: %s %s → %s", pkgName, version, targetFilePath)
			if err := AddPackageReference(targetFilePath, pkgName, version); err != nil {
				return writeResultMsg{err: err}
			}
		}
		return writeResultMsg{err: nil}
	}
}

func (m Model) renderLocationPickOverlay() string {
	w := clampW(80+m.overlayWidthOffset, 60, m.ctx.Width-4)

	lines := []string{
		styleAccentBold.Render("Add to which file?"),
		styleSubtle.Render(m.locationPick.pkgName + " " + m.locationPick.version),
		"",
	}

	type row struct {
		fileName  string
		kindLabel string
		desc      string
	}
	rows := make([]row, len(m.locationPick.targets))
	maxName := 0
	maxKind := 0
	for i, target := range m.locationPick.targets {
		var kind string
		switch target.Kind {
		case AddTargetProject:
			kind = "project"
		case AddTargetBuildProps:
			kind = "build props"
		case AddTargetCPM:
			kind = "CPM"
		case AddTargetImportedProps:
			kind = "imported props"
		}
		rows[i] = row{filepath.Base(target.FilePath), kind, target.Description}
		if len(rows[i].fileName) > maxName {
			maxName = len(rows[i].fileName)
		}
		if len(kind) > maxKind {
			maxKind = len(kind)
		}
	}

	for i, r := range rows {
		prefix := "  "
		nameStyle := styleMuted
		if i == m.locationPick.cursor {
			prefix = "▶ "
			nameStyle = styleAccentBold
		}
		line := prefix +
			padRight(nameStyle.Render(r.fileName), maxName+1) +
			padRight(styleMuted.Render("["+r.kindLabel+"]"), maxKind+3) +
			styleSubtle.Render(r.desc)
		lines = append(lines, line)
	}

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.ctx.Width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}
