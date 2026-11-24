package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	ui "github.com/nulifyer/guget/internal/ui"
)

func (m *App) updatePackage(useStable bool, scope actionScope) ui.Effect {
	if m.ctx.Saving {
		return m.setStatus("Saving package changes; wait before updating again", false)
	}
	if m.ctx.Reloading {
		return m.setStatus("Loading projects; wait before updating", false)
	}
	if m.workspaceStale {
		return m.setStatus("Workspace is out of date; fix files and Ctrl+R before updating", true)
	}
	if m.packages.cursor < 0 || m.packages.cursor >= len(m.packages.rows) {
		return m.setStatus("Select a package to update", false)
	}
	row := m.packages.rows[m.packages.cursor]
	if row.loading {
		return m.setStatus("Loading metadata for "+row.ref.Name+"; press u again when ready", false)
	}
	if row.err != nil {
		return m.setStatus("Cannot update "+row.ref.Name+": "+row.err.Error()+"; Ctrl+R to retry", true)
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
		if useStable {
			return m.setStatus("No stable version for "+row.ref.Name+"; v to choose a version", true)
		}
		return m.setStatus("No compatible stable version for "+row.ref.Name+"; v to choose a version", true)
	}
	if assessment.Target == nil {
		for _, installed := range assessment.Installed {
			if installed.IsNewerThan(target.SemVer) {
				return m.setStatus(row.ref.Name+" "+installed.String()+" is newer than "+target.SemVer.String()+"; v to choose a version", false)
			}
		}
		return m.setStatus(row.ref.Name+" is already at "+target.SemVer.String(), false)
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
			return m.setStatus(fmt.Sprintf("%d skipped (version locked)", skippedLocked), false)
		}
		return m.setStatus(row.ref.Name+" is already at "+version, false)
	}
	if !useStable {
		if conflict := m.quickUpdateDependencyConflict(row.ref.Name, target, scope); conflict != "" {
			return m.setStatus(conflict, true)
		}
	}
	return m.applyOrConfirmUpdateWithSelection(row.ref.Name, version, project, m.rowReloadSelection())
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
	return m.startMutation("update", pkgName, version, targets, mutationOptions{skipLocked: targetProject == nil, allowDeclaredEdit: true}, selection)
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

func (m *App) removePackage(pkgName string) ui.Effect {
	selected := m.selectedProject()
	projects := m.ctx.ParsedProjects
	if selected != nil {
		projects = []*ParsedProject{selected}
	}
	targets := declaredPackageTargets(projects, pkgName)
	return m.startMutation("remove", pkgName, "", targets, mutationOptions{removeInherited: selected == nil, allowDeclaredEdit: true}, m.rowReloadSelection())
}
