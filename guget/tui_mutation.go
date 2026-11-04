package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

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
	return !m.ctx.Saving && !m.ctx.Reloading && !m.workspaceStale
}

// mutationReconciledMsg carries a locally reparsed, revision-checked delta.
// It avoids rediscovering unrelated projects after each package write.
type mutationReconciledMsg struct {
	writeResultMsg
	generation int
	snapshot   *workspaceSnapshot
	revisions  map[string]watchedFileState
}

func (m *App) startMutation(verb, packageID, version string, targets []mutationTarget, options mutationOptions, selection *reloadSelection) tea.Cmd {
	if !m.canMutate() {
		return nil
	}
	m.ctx.Saving = true
	m.setStatus("Saving "+packageID, false)
	if options.ctx == nil {
		options.ctx = m.lifecycle
	}
	if options.ctx == nil {
		options.ctx = context.Background()
	}
	if m.watchInputs == nil {
		m.watchInputs = &workspaceWatchInputs{}
	}
	generation := m.workspaceGeneration
	previous := &workspaceSnapshot{
		ProjectDir:     m.projectDir,
		InputRevisions: m.sourceInputRevisions,
		ParsedProjects: append([]*ParsedProject(nil), m.ctx.ParsedProjects...),
		PropsProjects:  append([]*ParsedProject(nil), m.ctx.PropsProjects...),
		Sources:        m.ctx.Sources, SourceMapping: m.ctx.SourceMapping, NugetServices: m.ctx.NugetServices,
	}
	return func() tea.Msg {
		if err := options.ctx.Err(); err != nil {
			return writeResultMsg{err: err}
		}
		expected, err := mutationSnapshotInputs(previous, targets, packageID)
		if err == nil {
			err = validateMutationSnapshotInputs(expected)
		}
		if err != nil {
			return writeResultMsg{err: err, reload: true}
		}
		plan, _, skipped, err := planPackageMutation(verb, packageID, version, targets, options)
		if err != nil {
			return writeResultMsg{err: err}
		}
		if err := validateMutationSnapshotInputs(expected); err != nil {
			return writeResultMsg{err: err, reload: true}
		}
		result, err := plan.ApplyContext(options.ctx)
		if len(result.Unrecovered) > 0 {
			err = fmt.Errorf("%w; inspect unrecovered files before retrying: %s", err, strings.Join(result.Unrecovered, ", "))
		}
		completion := writeResultMsg{err: err, written: len(result.Changed), skipped: skipped, reload: len(result.Changed) > 0, selection: selection}
		if err != nil || len(result.Changed) == 0 {
			return completion
		}
		snapshot, revisions, reconcileErr := reconcileMutationSnapshot(previous, result.Changed, ParseCsproj)
		if reconcileErr != nil {
			logWarn("Saved package files; full reconciliation required: %v", reconcileErr)
			return completion
		}
		completion.reload = false
		return mutationReconciledMsg{writeResultMsg: completion, generation: generation, snapshot: snapshot, revisions: revisions}
	}
}

func (m *App) handleMutationReconciled(msg mutationReconciledMsg) tea.Cmd {
	if msg.generation != m.workspaceGeneration || m.ctx.Reloading {
		msg.writeResultMsg.reload = true
		_, cmd := m.Update(msg.writeResultMsg)
		return cmd
	}
	before := m.packageSelection()
	offset := m.detail.vp.YOffset()
	m.activeReload = reloadRequestedMsg{reason: "package files changed", selection: msg.selection}
	m.applyWorkspaceSnapshot(msg.snapshot)
	m.workspaceGeneration++
	next, missing := planPackageReload(msg.snapshot, m.ctx.Results, false)
	m.ctx.Results = next
	m.startPackageFetch(missing, false)
	m.rebuildPackageRows()
	m.refreshDetail()
	if samePackageIdentity(m.packageSelection(), before) {
		m.detail.vp.SetYOffset(offset)
	}
	m.watchInputs.acknowledge(msg.revisions)
	_, cmd := m.Update(msg.writeResultMsg)
	return cmd
}

// Reparse affected consumers only, preserving unrelated immutable project state.
// Hash both sides of parsing so an overlapping editor save triggers a full reload
// rather than publishing a mixed local result. The parser seam controls this race
// in tests without adding sleeps or changing production filesystem semantics.
func reconcileMutationSnapshot(previous *workspaceSnapshot, changed []string, parse func(string) (*ParsedProject, error)) (*workspaceSnapshot, map[string]watchedFileState, error) {
	owners := make(map[string]bool, len(changed))
	for _, path := range changed {
		owners[mutationInputKey(path)] = true
	}
	affected := make(map[string]bool)
	inputs := make(map[string]struct{})
	for _, project := range previous.ParsedProjects {
		for _, path := range projectInputPaths(project) {
			if owners[mutationInputKey(path)] {
				affected[project.FilePath] = true
				break
			}
		}
		if affected[project.FilePath] {
			for _, path := range projectInputPaths(project) {
				inputs[path] = struct{}{}
			}
		}
	}
	for _, path := range changed {
		inputs[path] = struct{}{}
	}
	refreshSources := false
	for path := range previous.InputRevisions {
		if owners[mutationInputKey(path)] {
			refreshSources = true
			break
		}
	}
	if refreshSources {
		for path := range previous.InputRevisions {
			inputs[path] = struct{}{}
		}
	}
	if len(affected) == 0 {
		return nil, nil, fmt.Errorf("changed owner has no known project consumer")
	}
	before, err := mutationInputStates(inputs)
	if err != nil {
		return nil, nil, err
	}
	next := *previous
	next.ParsedProjects = append([]*ParsedProject(nil), previous.ParsedProjects...)
	for i, project := range next.ParsedProjects {
		if !affected[project.FilePath] {
			continue
		}
		updated, err := parse(project.FilePath)
		if err != nil {
			return nil, nil, err
		}
		if err := validateProjectInputRevisions(updated, before); err != nil {
			return nil, nil, err
		}
		if len(updated.DiscoveryErrors) > 0 {
			return nil, nil, fmt.Errorf("incomplete project discovery in %s: %v", updated.FilePath, updated.DiscoveryErrors[0])
		}
		next.ParsedProjects[i] = updated
		for _, path := range projectInputPaths(updated) {
			if _, known := inputs[path]; !known {
				return nil, nil, fmt.Errorf("project inputs changed during local reconciliation: %s", path)
			}
		}
	}
	// Preserve untouched props views, but update shared owners and newly exposed
	// package owners together with all their project consumers.
	props := make(map[string]*ParsedProject)
	for _, project := range previous.PropsProjects {
		props[mutationInputKey(project.FilePath)] = project
	}
	paths := make(map[string]string)
	for _, project := range next.ParsedProjects {
		for _, source := range project.PackageSources {
			if strings.EqualFold(filepath.Ext(source), ".props") {
				paths[mutationInputKey(source)] = source
			}
		}
	}
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	next.PropsProjects = nil
	for _, key := range keys {
		project := props[key]
		if project == nil || owners[key] {
			project, err = ParsePropsAsProject(paths[key])
			if err != nil {
				return nil, nil, err
			}
		}
		if err := validateProjectInputRevisions(project, before); err != nil {
			return nil, nil, err
		}
		if project.Packages.Len() > 0 {
			next.PropsProjects = append(next.PropsProjects, project)
		}
	}
	if refreshSources {
		// Directory.Build.props can define both package versions and sources.
		// Advance its consumed revision only after proving the source view is
		// unchanged, rather than treating our own package edit as external later.
		detected := DetectSources(previous.ProjectDir)
		if len(detected.Errors) > 0 {
			return nil, nil, fmt.Errorf("source configuration changed during local reconciliation: %v", detected.Errors[0])
		}
		if !slices.Equal(previous.Sources, detected.Sources) || workspaceSourceSignature(previous.Sources, previous.SourceMapping) != workspaceSourceSignature(detected.Sources, detected.Mapping) {
			return nil, nil, fmt.Errorf("source configuration changed during local reconciliation")
		}
		for path, digest := range detected.InputRevisions {
			state, known := before[path]
			if !known || state.Hash != digest {
				return nil, nil, fmt.Errorf("source input changed during local reconciliation: %s", path)
			}
		}
		next.InputRevisions = detected.InputRevisions
	}
	after, err := mutationInputStates(inputs)
	if err != nil {
		return nil, nil, err
	}
	for path, state := range before {
		if current, exists := after[path]; !exists || current.Hash != state.Hash || current.Owner != state.Owner {
			return nil, nil, fmt.Errorf("project input changed during local reconciliation: %s", path)
		}
	}
	if len(before) != len(after) {
		return nil, nil, fmt.Errorf("project inputs appeared during local reconciliation")
	}
	return &next, after, nil
}

func mutationInputKey(path string) string {
	if canonical, err := canonicalMutationPath(path); err == nil {
		return canonical
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func mutationInputStates(paths map[string]struct{}) (map[string]watchedFileState, error) {
	states := make(map[string]watchedFileState, len(paths))
	for path := range paths {
		state, exists, err := readWatchedFileState(path)
		if err != nil {
			return nil, err
		}
		if exists {
			states[path] = state
		}
	}
	return states, nil
}

func samePackageIdentity(a, b packageSelection) bool {
	return a.projectPath == b.projectPath && strings.EqualFold(a.name, b.name)
}

// Guard recommendation inputs before and after planning. The transaction protects
// the edits it planned; this also rejects stale UI recommendations computed before
// an editor changed a dependency, framework, import, or source configuration.
func mutationSnapshotInputs(snapshot *workspaceSnapshot, targets []mutationTarget, packageID string) (map[string][32]byte, error) {
	selected := make([]*ParsedProject, 0, len(targets))
	for _, target := range targets {
		selected = append(selected, target.project)
	}
	consumers := append(append([]*ParsedProject(nil), selected...), snapshotAffectedProjects(snapshot.ParsedProjects, selected, packageID)...)
	expected := make(map[string][32]byte)
	merge := func(path string, digest [32]byte) error {
		if prior, known := expected[path]; known && prior != digest {
			return fmt.Errorf("workspace inputs changed between loaded project views: %s; reload before retrying", path)
		}
		expected[path] = digest
		return nil
	}
	for _, project := range consumers {
		for path, digest := range project.InputRevisions {
			if err := merge(path, digest); err != nil {
				return nil, err
			}
		}
	}
	for path, digest := range snapshot.InputRevisions {
		if err := merge(path, digest); err != nil {
			return nil, err
		}
	}
	return expected, nil
}

func validateMutationSnapshotInputs(expected map[string][32]byte) error {
	for path, digest := range expected {
		state, exists, err := readWatchedFileState(path)
		if err != nil {
			return fmt.Errorf("could not validate loaded workspace input %s: %w", path, err)
		}
		if !exists || state.Hash != digest {
			return fmt.Errorf("workspace input changed since it was loaded: %s; reloading before retry", path)
		}
	}
	return nil
}
