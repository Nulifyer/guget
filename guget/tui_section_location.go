package main

import (
	"path/filepath"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
)

func (m *App) addPackageToProject(pkgName, version string, project *ParsedProject) bubble_tea.Cmd {
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
	for i, row := range m.packages.rows {
		if strings.EqualFold(row.ref.Name, pkgName) {
			m.packages.cursor = i
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
func (m *App) openLocationPickerOrAdd(pkgName, version string, project *ParsedProject) bubble_tea.Cmd {
	// Props files: add directly, no picker needed.
	if strings.HasSuffix(strings.ToLower(project.FilePath), ".props") {
		return m.addPackageToProject(pkgName, version, project)
	}
	// Only one target (the project itself): add directly.
	if len(project.AddTargets) <= 1 {
		return m.addPackageToProject(pkgName, version, project)
	}
	// Multiple targets: open the location picker.
	m.locationPick = newLocationPicker(m, pkgName, version, project)
	return nil
}

func newLocationPicker(m *App, pkgName, version string, project *ParsedProject) locationPicker {
	return locationPicker{
		sectionBase:   sectionBase{app: m, baseWidth: 80, minWidth: 60, maxMargin: 4, active: true},
		pkgName:       pkgName,
		version:       version,
		targets:       project.AddTargets,
		targetProject: project,
	}
}

func (s *locationPicker) FooterKeys() []kv {
	return []kv{{"↑↓", "nav"}, {"enter", "select"}, {"esc", "cancel"}}
}

func (s *locationPicker) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		return nil
	case "]":
		s.Resize(4)
		return nil
	case "esc", "q":
		s.closeOverlay()
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(s.targets)-1 {
			s.cursor++
		}
	case "enter":
		s.closeOverlay()
		selected := s.targets[s.cursor]
		return s.app.addPackageToLocation(
			s.pkgName,
			s.version,
			s.targetProject,
			selected,
		)
	}
	return nil
}

// addPackageToLocation adds a package to the specified target location.
// For CPM targets, it performs a dual write: PackageVersion to the CPM file
// and a version-less PackageReference to the project file.
func (m *App) addPackageToLocation(pkgName, version string, project *ParsedProject, target AddTarget) bubble_tea.Cmd {
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
			matched := false
			// Props projects parsed via ParsePropsAsProject have no AddTargets
			// but their FilePath is the props file itself.
			if p.FilePath == target.FilePath {
				matched = true
			} else {
				for _, at := range p.AddTargets {
					if at.FilePath == target.FilePath {
						matched = true
						break
					}
				}
			}
			if matched {
				p.Packages.Add(PackageReference{Name: pkgName, Version: ParseSemVer(version)})
				p.PackageSources[strings.ToLower(pkgName)] = target.FilePath
			}
		}
	}

	m.rebuildPackageRows()
	for i, row := range m.packages.rows {
		if strings.EqualFold(row.ref.Name, pkgName) {
			m.packages.cursor = i
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

func (s *locationPicker) Render() string {
	w := s.Width()

	lines := []string{
		styleAccentBold.Render("Add to which file?"),
		styleSubtle.Render(s.pkgName + " " + s.version),
		"",
	}

	type row struct {
		fileName  string
		kindLabel string
		desc      string
	}
	rows := make([]row, len(s.targets))
	maxName := 0
	maxKind := 0
	for i, target := range s.targets {
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
		if i == s.cursor {
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
	return s.centerOverlay(box)
}
