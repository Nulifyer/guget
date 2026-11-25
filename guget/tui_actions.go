package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	ui "github.com/nulifyer/guget/internal/ui"
)

func (m *App) updatePackage(useStable bool, scope actionScope) ui.Effect {
	if effect, refused := m.updateRefusedEverywhere(); refused {
		return effect
	}
	plan := m.planQuickUpdate(m.packages.rows[m.packages.cursor], useStable, scope, nil)
	if plan.version == "" {
		return m.setStatus(plan.refusal, plan.isErr)
	}
	return m.applyOrConfirmUpdateWithSelection(plan.name, plan.version, plan.project, m.rowReloadSelection())
}

// updateRefusedEverywhere reports the reasons no row can be updated now: a
// save or reload is running, the workspace is stale, or nothing is selected.
func (m *App) updateRefusedEverywhere() (ui.Effect, bool) {
	switch {
	case m.ctx.Saving:
		return m.setStatus("Saving package changes; wait before updating again", false), true
	case m.ctx.Reloading:
		return m.setStatus("Loading projects; wait before updating", false), true
	case m.workspaceStale:
		return m.setStatus("Workspace is out of date; fix files and Ctrl+R before updating", true), true
	case m.packages.cursor < 0 || m.packages.cursor >= len(m.packages.rows):
		return m.setStatus("Select a package to update", false), true
	}
	return nil, false
}

// quickUpdatePlan is what u, U, a, or A would do to one row. It is decided
// from loaded state alone; the write still runs every mutation check.
type quickUpdatePlan struct {
	name    string
	version string // the version to write; empty when the row is refused
	project *ParsedProject
	refusal string // status text for a refused row
	isErr   bool
	// settled marks a refusal that leaves nothing to do: the row is at the
	// target or beyond it. Other refusals leave an update the user must
	// decide about.
	settled bool
}

// planQuickUpdate decides the quick update for row without changing any
// state. index may be nil; a caller planning many rows passes one index so
// the dependency check does not rebuild it per row.
func (m *App) planQuickUpdate(row packageRow, useStable bool, scope actionScope, index updateDependencyReferences) quickUpdatePlan {
	plan := quickUpdatePlan{name: row.ref.Name}
	refuse := func(text string, isErr bool) quickUpdatePlan {
		plan.refusal, plan.isErr = text, isErr
		return plan
	}
	if row.loading {
		return refuse("Loading metadata for "+row.ref.Name+"; press u again when ready", false)
	}
	if errors.Is(row.err, errNoCommonVersion) {
		// A retry cannot help: the projects' configurations disagree.
		return refuse("Cannot update "+row.ref.Name+": its projects' NuGet sources share no version", true)
	}
	if row.err != nil {
		return refuse("Cannot update "+row.ref.Name+": "+row.err.Error()+"; Ctrl+R to retry", true)
	}
	var project *ParsedProject
	if scope == scopeSelected {
		project = m.selectedProject()
	}
	// u and U aim for the update-target policy; a and A for the stable
	// policy, which is what dotnet package update chooses. Bulk updates skip
	// pins, so a pin does not hold the other references back.
	policy := m.updateTarget
	if useStable {
		policy = UpdateTargetStable
	}
	var refs []PackageReference
	for _, ref := range m.scopeReferences(row.ref.Name, scope) {
		if project == nil && ref.Locked {
			continue
		}
		refs = append(refs, ref)
	}
	assessment := assessPackage(row.info, refs, m.packageTargets(row.ref.Name, scope), policy)
	target := assessment.Candidate
	if target == nil {
		// Name what the policy in effect looked for, not the default's.
		kind := "compatible stable"
		switch policy {
		case UpdateTargetStable:
			kind = "stable"
		case UpdateTargetCompatible:
			kind = "compatible"
		}
		return refuse("No "+kind+" version for "+row.ref.Name+"; v to choose a version", true)
	}
	if assessment.Target == nil {
		plan.settled = true
		for _, installed := range assessment.Installed {
			if installed.IsNewerThan(target.SemVer) {
				return refuse(row.ref.Name+" "+installed.String()+" is newer than "+target.SemVer.String()+"; v to choose a version", false)
			}
		}
		return refuse(row.ref.Name+" is already at "+target.SemVer.String(), false)
	}
	version := target.SemVer.String()
	projects := m.ctx.ParsedProjects
	if project != nil {
		projects = []*ParsedProject{project}
	}
	needsUpdate := false
	skippedLocked := 0
	for _, p := range projects {
		for ref := range p.Packages {
			requested := ref.Requested
			if requested == "" {
				requested = ref.Version.String()
			}
			if !strings.EqualFold(ref.Name, row.ref.Name) {
				continue
			}
			if project == nil && ref.Locked {
				skippedLocked++
				continue
			}
			if !requestNamesVersion(requested, target.SemVer) {
				needsUpdate = true
			}
		}
	}
	if !needsUpdate {
		if skippedLocked > 0 {
			return refuse(fmt.Sprintf("%d skipped (pinned)", skippedLocked), false)
		}
		plan.settled = true
		return refuse(row.ref.Name+" is already at "+version, false)
	}
	if !useStable {
		if index == nil {
			index = indexUpdateDependencyReferences(m.ctx.ParsedProjects)
		}
		if requirements := m.indexedUpdateDependencyRequirements(row.ref.Name, target, scope, index); len(requirements) > 0 {
			return refuse(updateDependencyConflict(requirements[0]), true)
		}
	}
	plan.version, plan.project = version, project
	return plan
}

// scopeReferences returns the references to packageID that an action in
// scope changes: the selected project's, or every project's.
func (m *App) scopeReferences(packageID string, scope actionScope) []PackageReference {
	projects := m.ctx.ParsedProjects
	if scope == scopeSelected && m.selectedProject() != nil {
		projects = []*ParsedProject{m.selectedProject()}
	}
	var refs []PackageReference
	for _, project := range projects {
		for ref := range project.Packages {
			if strings.EqualFold(ref.Name, packageID) {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// packageTargets includes all consumers of each version owner the action can change.
// A central version selected in one project may also affect other target frameworks.
func (m *App) packageTargets(packageID string, scope actionScope) Set[TargetFramework] {
	selected := m.ctx.ParsedProjects
	if scope == scopeSelected && m.selectedProject() != nil {
		selected = []*ParsedProject{m.selectedProject()}
	}
	var references []*ParsedProject
	for _, project := range selected {
		for ref := range project.Packages {
			if strings.EqualFold(ref.Name, packageID) {
				references = append(references, project)
				break
			}
		}
	}
	targets := NewSet[TargetFramework]()
	consumers := append(references, snapshotAffectedProjects(m.ctx.ParsedProjects, references, packageID)...)
	for _, project := range consumers {
		for target := range project.TargetFrameworks {
			targets.Add(target)
		}
	}
	return targets
}

func (m *App) isPropsProject(p *ParsedProject) bool {
	for _, pp := range m.ctx.PropsProjects {
		if pp == p {
			return true
		}
	}
	return false
}

func (m *App) applyVersion(pkgName, version string, targetProject *ParsedProject) ui.Effect {
	return m.applyVersionWithSelection(pkgName, version, targetProject, nil)
}

func (m *App) applyVersionWithSelection(pkgName, version string, targetProject *ParsedProject, selection *reloadSelection) ui.Effect {
	projects := m.ctx.ParsedProjects
	if targetProject != nil {
		projects = []*ParsedProject{targetProject}
	}
	targets := declaredPackageTargets(projects, pkgName)
	return m.startMutation("update", pkgName, version, targets, mutationOptions{skipLocked: targetProject == nil, staticOwnership: true}, selection)
}

// TUI actions operate on the package references shown in the loaded view.
// Unrelated projects must not trigger MSBuild evaluation or prevent an edit.
// Actual consumers still receive the normal ownership and revision checks.
func declaredPackageTargets(projects []*ParsedProject, packageID string) []mutationTarget {
	targets := make([]mutationTarget, 0, len(projects))
	for _, project := range projects {
		for ref := range project.Packages {
			if strings.EqualFold(ref.Name, packageID) {
				targets = append(targets, mutationTarget{project: project})
				break
			}
		}
	}
	return targets
}

func (m *App) restore(scope actionScope) ui.Effect {
	projects := m.ctx.ParsedProjects
	if scope == scopeSelected {
		if selected := m.selectedProject(); selected != nil && !m.isPropsProject(selected) {
			projects = []*ParsedProject{selected}
		}
	}
	if m.ctx.Saving {
		// Keep the requested scope while the user continues navigating. Start SDK
		// restore only after the write and its local reconciliation have completed.
		m.queuedRestore = append([]*ParsedProject(nil), projects...)
		return m.setStatus("Restore queued after saving package changes", false)
	}
	return m.startRestore(projects)
}

// startRestore restores the requested projects through the shared restore
// planner, which adds projects affected by unrestored package edits.
func (m *App) startRestore(projects []*ParsedProject) ui.Effect {
	covered := append([]string(nil), m.pendingRestoreChanges...)
	steps := planRestore(m.ctx.ParsedProjects, projects, covered)
	m.ctx.Restoring = true
	m.restoringProjects = len(steps)
	return runDotnetRestore(m.lifecycle, steps, covered)
}

// restoreSummary is the status line for a finished restore. The planner can
// restore more projects than the user selected, so the line says how many ran
// and why, and names the project when there was only one.
func restoreSummary(msg restoreResultMsg) (string, bool) {
	if msg.err != nil {
		if msg.projects <= 1 {
			return "✗ Restore failed" + restoreProjectSuffix(msg.only) + "; l for logs", true
		}
		return fmt.Sprintf("✗ Restore failed for %d of %d projects; l for logs", msg.failed, msg.projects), true
	}
	if msg.projects == 0 {
		return "✓ Restore complete", false
	}
	summary := fmt.Sprintf("✓ Restored %d projects", msg.projects)
	if msg.projects == 1 {
		summary = "✓ Restored " + msg.only
	}
	var notes []string
	if msg.affected > 0 {
		notes = append(notes, fmt.Sprintf("%d affected by package edits", msg.affected))
	}
	if msg.regenerated == 1 {
		notes = append(notes, "1 lock file regenerated")
	} else if msg.regenerated > 1 {
		notes = append(notes, fmt.Sprintf("%d lock files regenerated", msg.regenerated))
	}
	if len(notes) > 0 {
		summary += " (" + strings.Join(notes, ", ") + ")"
	}
	return summary, false
}

// restoringStatus is the footer text while a restore of count projects runs.
func restoringStatus(count int) string {
	if count > 1 {
		return fmt.Sprintf(" restoring %d projects...", count)
	}
	return " restoring..."
}

func restoreProjectSuffix(name string) string {
	if name == "" {
		return ""
	}
	return " for " + name
}

// runDotnetRestore runs every step off the event loop and continues past
// failures, reporting the last one.
func runDotnetRestore(ctx context.Context, steps []restoreStep, covered []string) ui.Effect {
	return func() ui.Event {
		var lastErr error
		result := restoreResultMsg{covered: covered}
		for _, step := range steps {
			p := step.Project
			if p.FilePath == "" {
				continue
			}
			result.projects++
			result.only = p.FileName
			if !step.Requested {
				result.affected++
			}
			if step.ForceEvaluate {
				result.regenerated++
			}
			if !step.Requested && step.Reason != "" {
				logInfo("restoring %s: %s", p.FileName, step.Reason)
			}
			if step.ForceEvaluate {
				logInfo("restoring %s with --force-evaluate: its locked-mode lock file no longer matches the edited packages", p.FileName)
			}
			logDebug("dotnet %s", strings.Join(step.Args, " "))
			cmd := exec.CommandContext(ctx, "dotnet", step.Args...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				logWarn("restore failed for %s: %v\n%s", p.FilePath, err, strings.TrimSpace(string(out)))
				lastErr = fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
				result.failed++
			} else {
				logInfo("restore succeeded for %s", p.FileName)
			}
		}
		result.err = lastErr
		return result
	}
}

// removeProblem explains, before the confirmation opens, why d cannot remove
// the row of a selected shared props file: the file holds the package as a
// global reference, which is removed from All Projects, or only as a version
// that no project takes its reference from. Without it the removal would
// find nothing to write and answer only "unchanged". It reads discovery
// state alone and returns "" when the removal may proceed.
func (m *App) removeProblem(row packageRow) string {
	selected := m.selectedProject()
	if selected == nil || !m.isPropsProject(selected) {
		return ""
	}
	if row.ref.Global {
		return row.ref.Name + " is a global reference; remove it from All Projects"
	}
	identity := func(project *ParsedProject, path string) string {
		if resolved := project.PackageOwnerIdentities[path]; resolved != "" {
			return snapshotOwnerKey(resolved)
		}
		return snapshotOwnerKey(filepath.Clean(path))
	}
	file := identity(selected, selected.FilePath)
	for _, project := range m.ctx.ParsedProjects {
		for ref := range project.Packages {
			if strings.EqualFold(ref.Name, row.ref.Name) && identity(project, project.ReferenceFileForPackage(ref.Name)) == file {
				return ""
			}
		}
	}
	return selected.FileName + " only sets " + row.ref.Name + "'s version; remove its reference from a project"
}

func (m *App) removePackage(pkgName string) ui.Effect {
	selected := m.selectedProject()
	projects := m.ctx.ParsedProjects
	if selected != nil {
		projects = []*ParsedProject{selected}
	}
	targets := declaredPackageTargets(projects, pkgName)
	return m.startMutation("remove", pkgName, "", targets, mutationOptions{removeInherited: selected == nil, staticOwnership: true}, m.rowReloadSelection())
}
