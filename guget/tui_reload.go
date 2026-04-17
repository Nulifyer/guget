package main

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// SetSender wires the tea.Program's Send function into the App. It must be
// called exactly once, before startInitialLoad or any requestReload, and
// before the file watcher starts. After that, m.send is treated as read-only
// by background goroutines; tea.Program.Send is itself goroutine-safe, so no
// further locking is required.
func (m *App) SetSender(send func(tea.Msg)) {
	m.send = send
}

func (m *App) startInitialLoad() {
	m.workspaceGeneration++
	names := distinctPackageNames(m.ctx.ParsedProjects, m.ctx.PropsProjects)
	if m.ctx.Results == nil {
		m.ctx.Results = make(map[string]nugetResult, len(names))
	}
	m.startPackageFetch(names, true)
	m.rebuildPackageRows()
	m.refreshDetail()
}

func (m *App) requestReload(req reloadRequestedMsg) {
	if m.send == nil {
		return
	}

	if m.ctx.Loading || m.ctx.Reloading {
		m.pendingReload = req
		m.hasPendingReload = true
		if !m.ctx.Loading {
			m.setStatus("Reload queued", false)
		}
		return
	}

	m.ctx.Reloading = true
	m.activeReload = req
	m.closeReloadUnsafeOverlays()

	m.workspaceGeneration++
	generation := m.workspaceGeneration

	if req.automatic && len(req.paths) > 0 {
		logInfo("Workspace change detected: %s", formatReloadPaths(m.projectDir, req.paths))
	} else {
		logInfo("Reload requested: %s", reloadStatusText(req))
	}

	go func() {
		snapshot, err := loadWorkspace(m.projectDir)
		m.send(workspaceReloadedMsg{
			generation: generation,
			snapshot:   snapshot,
			request:    req,
			err:        err,
		})
	}()
}

func (m *App) handleWorkspaceReloaded(msg workspaceReloadedMsg) {
	if msg.generation != m.workspaceGeneration {
		return
	}
	if msg.err != nil {
		logWarn("Reload failed: %v", msg.err)
		m.ctx.Reloading = false
		m.setStatus("▲ Reload failed: "+msg.err.Error(), true)
		m.maybeStartQueuedReload()
		return
	}

	currentSourceSig := m.sourceSignature
	nextSourceSig := workspaceSourceSignature(msg.snapshot.Sources, msg.snapshot.SourceMapping)
	invalidateAll := currentSourceSig != "" && currentSourceSig != nextSourceSig
	if invalidateAll {
		logInfo("NuGet source configuration changed; refreshing all package metadata")
	}

	m.applyWorkspaceSnapshot(msg.snapshot)
	m.sourceSignature = nextSourceSig

	nextResults, toFetch := planPackageReload(msg.snapshot, m.ctx.Results, invalidateAll)
	m.ctx.Results = nextResults
	m.startPackageFetch(toFetch, false)
	m.rebuildPackageRows()
	m.refreshDetail()

	if len(toFetch) == 0 {
		m.finishReloadSuccess()
	}
}

func (m *App) applyWorkspaceSnapshot(snapshot *workspaceSnapshot) {
	selectedProjectPath := ""
	if sel := m.selectedProject(); sel != nil {
		selectedProjectPath = sel.FilePath
	}

	selectedPackage := ""
	if m.packages.cursor >= 0 && m.packages.cursor < len(m.packages.rows) {
		selectedPackage = m.packages.rows[m.packages.cursor].ref.Name
	}

	m.ctx.ParsedProjects = snapshot.ParsedProjects
	m.ctx.PropsProjects = snapshot.PropsProjects
	m.ctx.NugetServices = snapshot.NugetServices
	m.ctx.Sources = snapshot.Sources
	m.ctx.SourceMapping = snapshot.SourceMapping
	m.projects.items = buildProjectItems(snapshot.ParsedProjects, snapshot.PropsProjects)
	m.selectProjectByPath(selectedProjectPath)

	m.rebuildPackageRows()
	m.selectPackageByName(selectedPackage)
}

func buildProjectItems(parsedProjects []*ParsedProject, propsProjects []*ParsedProject) []projectItem {
	items := []projectItem{{name: "All Projects", project: nil}}
	for _, p := range parsedProjects {
		items = append(items, projectItem{name: p.FileName, project: p})
	}
	for _, p := range propsProjects {
		items = append(items, projectItem{name: p.FileName, project: p})
	}
	return items
}

func (m *App) selectProjectByPath(path string) {
	if path == "" {
		m.projects.cursor = 0
		m.clampProjectOffset()
		return
	}
	for i, item := range m.projects.items {
		if item.project != nil && item.project.FilePath == path {
			m.projects.cursor = i
			m.clampProjectOffset()
			return
		}
	}
	m.projects.cursor = 0
	m.clampProjectOffset()
}

func (m *App) selectPackageByName(name string) {
	if name == "" {
		if m.packages.cursor >= len(m.packages.rows) {
			m.packages.cursor = imax(0, len(m.packages.rows)-1)
		}
		m.clampOffset()
		return
	}
	for i, row := range m.packages.rows {
		if strings.EqualFold(row.ref.Name, name) {
			m.packages.cursor = i
			m.clampOffset()
			return
		}
	}
	m.packages.cursor = imax(0, len(m.packages.rows)-1)
	m.clampOffset()
}

func (m *App) startPackageFetch(names []string, initial bool) {
	m.ctx.LoadingDone = 0
	m.ctx.LoadingTotal = len(names)
	m.ctx.PendingPackages = NewSet[string]()
	for _, name := range names {
		m.ctx.PendingPackages.Add(name)
	}

	if initial {
		m.ctx.Loading = len(names) > 0
	} else {
		m.ctx.Loading = false
	}

	if len(names) == 0 {
		m.ctx.Loading = false
		return
	}

	fetchPackageMetadataAsync(m.send, m.workspaceGeneration, m.ctx.NugetServices, m.ctx.SourceMapping, names)
}

func (m *App) finishReloadSuccess() {
	m.ctx.Reloading = false
	m.setStatus("✓ "+reloadStatusText(m.activeReload), false)
	m.maybeStartQueuedReload()
}

func (m *App) maybeStartQueuedReload() {
	if !m.hasPendingReload {
		return
	}
	req := m.pendingReload
	m.pendingReload = reloadRequestedMsg{}
	m.hasPendingReload = false
	m.requestReload(req)
}

// closeReloadUnsafeOverlays closes only the overlays that mutate project state
// or hold references to specific projects/packages that may disappear on reload.
// Read-only overlays (depTree, releaseNotes, help, sources) stay open so an
// auto-reload triggered by a disk change doesn't yank the user out of what
// they're reading.
func (m *App) closeReloadUnsafeOverlays() {
	if m.picker.app != nil {
		m.picker.closeOverlay()
	}
	m.picker.addMode = false
	m.picker.targetProject = nil

	if m.confirmRemove.app != nil {
		m.confirmRemove.closeOverlay()
	}
	if m.confirmUpdate.app != nil {
		m.confirmUpdate.closeOverlay()
	}
	m.confirmUpdate.project = nil

	if m.locationPick.app != nil {
		m.locationPick.closeOverlay()
	}
	m.locationPick.targetProject = nil
	m.locationPick.targets = nil

	if m.projectPick.app != nil {
		m.projectPick.closeOverlay()
	}
	m.projectPick.items = nil
}

func reloadStatusText(req reloadRequestedMsg) string {
	if req.automatic {
		if n := len(req.paths); n > 0 {
			if n == 1 {
				return "reloaded after 1 disk change"
			}
			return fmt.Sprintf("reloaded after %d disk changes", n)
		}
		return "reloaded after disk changes"
	}
	return "reloaded from disk"
}

func formatReloadPaths(rootDir string, paths []string) string {
	if len(paths) == 0 {
		return "workspace files changed"
	}

	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		if rel, err := filepath.Rel(rootDir, path); err == nil {
			parts = append(parts, rel)
		} else {
			parts = append(parts, path)
		}
	}

	if len(parts) > 4 {
		return strings.Join(parts[:4], ", ") + fmt.Sprintf(" (+%d more)", len(parts)-4)
	}
	return strings.Join(parts, ", ")
}
