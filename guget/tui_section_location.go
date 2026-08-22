package main

import (
	"path/filepath"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	editplan "github.com/nulifyer/guget/internal/edit"
)

func (m *App) addPackageToProject(pkgName, version string, project *ParsedProject) bubble_tea.Cmd {
	m.focus = focusPackages
	filePath := project.FilePath
	return func() bubble_tea.Msg {
		logInfo("AddPackageReference: %s %s → %s", pkgName, version, filePath)
		change, err := PlanAddPackageReference(filePath, pkgName, version)
		if err != nil {
			return writeResultMsg{err: err}
		}
		plan, err := editplan.NewPlan(change)
		if err != nil {
			return writeResultMsg{err: err}
		}
		if _, err := plan.Apply(); err != nil {
			return writeResultMsg{err: err}
		}
		return writeResultMsg{written: plan.Len(), reload: true}
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
	m.focus = focusPackages

	projectFilePath := project.FilePath
	targetFilePath := target.FilePath
	targetKind := target.Kind

	return func() bubble_tea.Msg {
		var changes []editplan.Change
		switch targetKind {
		case AddTargetCPM:
			logInfo("AddPackageVersion: %s %s → %s", pkgName, version, targetFilePath)
			centralChange, err := PlanAddPackageVersion(targetFilePath, pkgName, version)
			if err != nil {
				return writeResultMsg{err: err}
			}
			logInfo("AddPackageReference (CPM): %s → %s", pkgName, projectFilePath)
			projectChange, err := PlanAddPackageReference(projectFilePath, pkgName, "")
			if err != nil {
				return writeResultMsg{err: err}
			}
			changes = append(changes, centralChange, projectChange)
		default:
			logInfo("AddPackageReference: %s %s → %s", pkgName, version, targetFilePath)
			change, err := PlanAddPackageReference(targetFilePath, pkgName, version)
			if err != nil {
				return writeResultMsg{err: err}
			}
			changes = append(changes, change)
		}
		plan, err := editplan.NewPlan(changes...)
		if err != nil {
			return writeResultMsg{err: err}
		}
		if _, err := plan.Apply(); err != nil {
			return writeResultMsg{err: err}
		}
		return writeResultMsg{written: plan.Len(), reload: true}
	}
}

func (s *locationPicker) Render() string {
	w := s.Width()
	innerW := w - 6
	regions := []mouseRegion{overlayCloseRegion(w, s.HandleKey)}

	lines := []string{
		overlayTitleLine("Add to which file?", innerW),
		styleSubtle.Render(s.pkgName + " " + s.version),
		"",
	}
	rowStart := len(lines)

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
		index := i
		regions = append(regions, mouseRegion{
			rect: mouseRect{x: overlayContentX, y: overlayContentY + rowStart + i, w: innerW, h: 1},
			click: func(bubble_tea.MouseMsg) bubble_tea.Cmd {
				s.cursor = index
				return nil
			},
		})
	}
	lines = append(lines, "")
	buttonLine, buttonRegions := renderOverlayButtons(innerW, len(lines), s.HandleKey,
		overlayButton{label: "Cancel", code: bubble_tea.KeyEscape},
		overlayButton{label: "Add", code: bubble_tea.KeyEnter},
	)
	lines = append(lines, buttonLine)
	regions = append(regions, buttonRegions...)

	box := styleOverlay.
		Width(w).
		Render(strings.Join(lines, "\n"))
	regions = append(regions, mouseRegion{
		rect:  mouseRect{x: 1, y: 1, w: w - 2, h: lipgloss.Height(box) - 2},
		wheel: verticalWheelHandler(s.HandleKey),
	})
	return s.centerOverlayInteractive(box, regions)
}
