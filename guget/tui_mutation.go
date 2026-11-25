package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
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

// addSourceProblem explains why a search add must not write: the chosen
// version comes from a source that the NuGet configuration of a target
// project, or of a project importing a shared add location, does not allow
// for the package. Search in All Projects offers what any project may use,
// so the add re-checks each target. It returns "" when the add may proceed
// or when the version did not come from search.
func (m *App) addSourceProblem(pkgName, version string, targets []mutationTarget) string {
	info := m.search.fetchedInfo
	if info == nil {
		return ""
	}
	source := m.search.fetchedSource
	want := ParseSemVer(version)
	found := false
	for _, v := range info.Versions {
		if v.SemVer.Equal(want) {
			found = true
			if v.Source != "" {
				source = v.Source
			}
			break
		}
	}
	if !found || source == "" {
		return fmt.Sprintf("%s %s was not found on a known source; search again before adding", pkgName, version)
	}
	allowedFor := func(path string) bool {
		for _, service := range m.ctx.NugetServices {
			if strings.EqualFold(service.SourceName(), source) && m.ctx.SourceMapping.AllowsFor(path, service.SourceName(), service.SourceURL(), pkgName) {
				return true
			}
		}
		return false
	}
	for _, target := range targets {
		if !allowedFor(target.project.FilePath) {
			return fmt.Sprintf("%s may not use source %s for %s %s; see its NuGet configuration", target.project.FileName, source, pkgName, version)
		}
		if location := target.addTarget; location != nil && (location.Kind == AddTargetBuildProps || location.Kind == AddTargetImportedProps) && !allowedFor(location.FilePath) {
			return fmt.Sprintf("a project importing %s may not use source %s for %s %s", filepath.Base(location.FilePath), source, pkgName, version)
		}
	}
	return ""
}

// refusalStatus logs a refused action in full and shows it on the status
// row, pointing at the log panel when the row cannot hold the reason.
func (m *App) refusalStatus(action, problem string) ui.Effect {
	logWarn("%s: %s", action, problem)
	text := "▲ " + action + ": " + problem
	if style.Width(text) > m.layoutWidth()-6 {
		text = "▲ " + action + " (l for logs): " + problem
	}
	return m.setStatus(text, true)
}

// sharedOwnerProblem explains why an update must not write version: a file
// it writes holds the package's version for projects with different NuGet
// source scopes, and version is not listed in the package's metadata, which
// holds only versions every referencing project can use. It returns "" when
// no written file is shared across scopes, as in a single-scope workspace.
func (m *App) sharedOwnerProblem(packageID, version string, targets []mutationTarget) string {
	projects := make([]*ParsedProject, len(targets))
	for i, target := range targets {
		projects[i] = target.project
	}
	result := m.ctx.Results[packageID]
	for _, owner := range sharedOwnersFor(m.ctx.SourceMapping, m.ctx.ParsedProjects, projects, packageID) {
		name := filepath.Base(owner.path)
		switch {
		case result.pkg == nil:
			return fmt.Sprintf("%s %s cannot be checked: its metadata is not loaded for the projects sharing %s (%s)", packageID, version, name, owner.consumerNames())
		case !versionListed(result.pkg, version):
			return fmt.Sprintf("%s %s is not available from the NuGet sources of every project sharing %s (%s)", packageID, version, name, owner.consumerNames())
		}
	}
	return ""
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

// savingNoticeDelay is how long a save runs before the footer says so. A
// shorter save shows only its result, so the status row does not alternate
// between "saving..." and the result when updates follow one another.
const savingNoticeDelay = 80 * time.Millisecond

func (m *App) timeNow() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
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

func (m *App) startMutation(verb, packageID, version string, targets []mutationTarget, options mutationOptions, selection *reloadSelection) ui.Effect {
	if !m.canMutate() {
		return nil
	}
	if verb == "update" {
		if problem := m.sharedOwnerProblem(packageID, version, targets); problem != "" {
			return m.refusalStatus("Not updated", problem)
		}
	}
	// The status row keeps its text: the footer announces a save only once
	// it has run for savingNoticeDelay, and the result replaces it.
	m.ctx.Saving = true
	m.savingSince = m.timeNow()
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
		FileRevisions:  m.watchInputs.displayedRevisions(),
		InputRevisions: m.sourceInputRevisions,
		ParsedProjects: append([]*ParsedProject(nil), m.ctx.ParsedProjects...),
		PropsProjects:  append([]*ParsedProject(nil), m.ctx.PropsProjects...),
		Sources:        m.ctx.Sources, SourceMapping: m.ctx.SourceMapping, NugetServices: m.ctx.NugetServices,
	}
	return func() ui.Event {
		if err := options.ctx.Err(); err != nil {
			return writeResultMsg{err: err, packageID: packageID}
		}
		expected, err := mutationSnapshotInputs(previous, targets, packageID)
		if err == nil {
			err = validateMutationSnapshotInputs(expected)
		}
		if err != nil {
			return writeResultMsg{err: err, packageID: packageID, reload: true}
		}
		plan, _, skipped, err := planPackageMutation(verb, packageID, version, targets, options)
		if err != nil {
			return writeResultMsg{err: err, packageID: packageID}
		}
		if err := validateMutationSnapshotInputs(expected); err != nil {
			return writeResultMsg{err: err, packageID: packageID, reload: true}
		}
		// The TUI saves while keys repeat, so it does not wait for each file to
		// be flushed to disk; the CLI does.
		result, err := plan.ApplyContextBuffered(options.ctx)
		if len(result.Unrecovered) > 0 {
			err = fmt.Errorf("%w; inspect unrecovered files before retrying: %s", err, strings.Join(result.Unrecovered, ", "))
		}
		completion := writeResultMsg{err: err, packageID: packageID, summary: mutationSummary(verb, packageID, version, len(result.Changed), skipped), changed: result.Changed, written: len(result.Changed), skipped: skipped, reload: len(result.Changed) > 0, selection: selection}
		if err != nil || len(result.Changed) == 0 {
			return completion
		}
		snapshot, revisions, reconcileErr := reconcileMutationSnapshot(previous, result.Changed, newProjectParser())
		if reconcileErr != nil {
			logWarn("Saved package files; full reconciliation required: %v", reconcileErr)
			return completion
		}
		completion.reload = false
		return mutationReconciledMsg{writeResultMsg: completion, generation: generation, snapshot: snapshot, revisions: revisions}
	}
}

func mutationSummary(verb, packageID, version string, written, skipped int) string {
	if written == 0 {
		if skipped > 0 {
			return fmt.Sprintf("%s unchanged (%d pinned skipped)", packageID, skipped)
		}
		return packageID + " unchanged"
	}
	var summary string
	switch verb {
	case "update":
		summary = fmt.Sprintf("Updated %s to %s", packageID, version)
	case "add":
		summary = fmt.Sprintf("Added %s %s", packageID, version)
	case "remove":
		summary = "Removed " + packageID
	}
	files := "files"
	if written == 1 {
		files = "file"
	}
	summary = fmt.Sprintf("✓ %s (%d %s", summary, written, files)
	if skipped > 0 {
		summary += fmt.Sprintf(", %d pinned skipped", skipped)
	}
	return summary + ")"
}

func (m *App) handleMutationReconciled(msg mutationReconciledMsg) ui.Effect {
	if msg.generation != m.workspaceGeneration || m.ctx.Reloading {
		msg.writeResultMsg.reload = true
		cmd := m.HandleEvent(msg.writeResultMsg)
		return cmd
	}
	before := m.packageSelection()
	offset := m.detail.vp.YOffset()
	m.activeReload = reloadRequestedMsg{reason: "package files changed", selection: msg.selection}
	previousScopes := m.ctx.SourceMapping.packageScopesFor(m.ctx.ParsedProjects, m.ctx.PropsProjects)
	m.applyWorkspaceSnapshot(msg.snapshot)
	m.workspaceGeneration++
	next, missing := planPackageReload(msg.snapshot, m.ctx.Results, false, previousScopes)
	m.ctx.Results = next
	m.startPackageFetch(missing, false)
	m.rebuildPackageRows()
	m.refreshDetail()
	if samePackageIdentity(m.packageSelection(), before) {
		m.detail.vp.SetYOffset(offset)
	}
	m.watchInputs.acknowledge(msg.revisions)
	cmd := m.HandleEvent(msg.writeResultMsg)
	return cmd
}

// Reparse affected consumers only, preserving unrelated immutable project state.
// Hash both sides of parsing so an overlapping editor save triggers a full reload
// rather than publishing a mixed local result. The parser seam controls this race
// in tests without adding sleeps or changing production filesystem semantics.
func reconcileMutationSnapshot(previous *workspaceSnapshot, changed []string, parse func(string) (*ParsedProject, error)) (*workspaceSnapshot, map[string]watchedFileState, error) {
	// Every project's inputs are compared with the changed owners; resolve
	// each path's symlinks once per reconciliation.
	inputKey := memoizedMutationInputKey()
	owners := make(map[string]bool, len(changed))
	for _, path := range changed {
		owners[inputKey(path)] = true
	}
	affected := make(map[string]bool)
	inputs := make(map[string]struct{})
	for _, project := range previous.ParsedProjects {
		for _, path := range projectInputPaths(project) {
			if owners[inputKey(path)] {
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
		if owners[inputKey(path)] {
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
		if !sameRestoreProperties(project, updated) {
			return nil, nil, fmt.Errorf("source configuration changed during local reconciliation: %s", updated.FilePath)
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
		props[inputKey(project.FilePath)] = project
	}
	paths := make(map[string]string)
	for _, project := range next.ParsedProjects {
		for _, source := range project.PackageSources {
			if strings.EqualFold(filepath.Ext(source), ".props") {
				paths[inputKey(source)] = source
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
		// A package edit touched a NuGet configuration owner. Advance its
		// consumed revision only after proving the source view is unchanged,
		// rather than treating our own edit as external later.
		detected, err := resolveWorkspaceSources(next.ParsedProjects)
		if err != nil {
			return nil, nil, fmt.Errorf("source configuration changed during local reconciliation: %v", err)
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
	// The new snapshot displays the post-write revisions it read; record them
	// so the watcher compares disk changes against what is shown, not against
	// revisions an earlier save already replaced.
	revisions := make(map[string]watchedFileState, len(previous.FileRevisions)+len(after))
	for path, state := range previous.FileRevisions {
		revisions[path] = state
	}
	for path, state := range after {
		revisions[path] = state
	}
	next.FileRevisions = revisions
	return &next, after, nil
}

func memoizedMutationInputKey() func(string) string {
	keys := make(map[string]string)
	return func(path string) string {
		key, known := keys[path]
		if !known {
			key = mutationInputKey(path)
			keys[path] = key
		}
		return key
	}
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
