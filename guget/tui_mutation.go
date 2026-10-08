package main

import tea "charm.land/bubbletea/v2"

// packageSelection describes the view in which an action began. A completion
// must not move the cursor if the user has since navigated or changed sorting.
type packageSelection struct {
	projectPath string
	name        string
	cursor      int
	sortMode    packageSortMode
	sortDir     bool
}

type reloadSelection struct {
	origin  packageSelection
	keepRow bool
	name    string
}

func (m *App) packageSelection() packageSelection {
	selection := packageSelection{cursor: m.packages.cursor, sortMode: m.packages.sortMode, sortDir: m.packages.sortDir}
	if project := m.selectedProject(); project != nil {
		selection.projectPath = project.FilePath
	}
	if m.packages.cursor >= 0 && m.packages.cursor < len(m.packages.rows) {
		selection.name = m.packages.rows[m.packages.cursor].ref.Name
	}
	return selection
}

func (m *App) rowReloadSelection() *reloadSelection {
	return &reloadSelection{origin: m.packageSelection(), keepRow: true}
}

func (m *App) canMutate() bool {
	return !m.ctx.Saving && !m.ctx.Reloading && !m.ctx.Loading
}

func (m *App) startMutation(verb, packageID, version string, targets []mutationTarget, options mutationOptions, selection *reloadSelection) tea.Cmd {
	if !m.canMutate() {
		return nil
	}
	m.ctx.Saving = true
	m.setStatus("Saving "+packageID, false)
	return func() tea.Msg {
		plan, _, skipped, err := planPackageMutation(verb, packageID, version, targets, options)
		if err != nil {
			return writeResultMsg{err: err}
		}
		result, err := plan.Apply()
		return writeResultMsg{err: err, written: len(result.Changed), skipped: skipped, reload: len(result.Changed) > 0, selection: selection}
	}
}
