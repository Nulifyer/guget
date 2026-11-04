package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
)

func (m *App) updatePackage(useStable bool, scope actionScope) bubble_tea.Cmd {
	if !m.canMutate() || m.packages.cursor < 0 || m.packages.cursor >= len(m.packages.rows) {
		return nil
	}
	row := m.packages.rows[m.packages.cursor]
	if row.err != nil || row.loading {
		return nil
	}
	var target *PackageVersion
	if useStable {
		target = row.latestStable
	} else {
		if row.info != nil {
			target = row.info.LatestStableForFramework(m.packageTargets(row.ref.Name, scope))
		}
	}
	if target == nil {
		return nil
	}
	var project *ParsedProject
	if scope == scopeSelected {
		project = m.selectedProject()
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
			if requested != version && requested != "["+version+"]" {
				needsUpdate = true
			}
		}
	}
	if !needsUpdate {
		if skippedLocked > 0 {
			return m.setStatus(fmt.Sprintf("%d skipped (version locked)", skippedLocked), false)
		}
		return nil
	}
	return m.applyOrConfirmUpdateWithSelection(row.ref.Name, version, project, m.rowReloadSelection())
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

func (m *App) applyVersion(pkgName, version string, targetProject *ParsedProject) bubble_tea.Cmd {
	return m.applyVersionWithSelection(pkgName, version, targetProject, nil)
}

func (m *App) applyVersionWithSelection(pkgName, version string, targetProject *ParsedProject, selection *reloadSelection) bubble_tea.Cmd {
	projects := m.ctx.ParsedProjects
	if targetProject != nil {
		projects = []*ParsedProject{targetProject}
	}
	targets := make([]mutationTarget, 0, len(projects))
	for _, project := range projects {
		targets = append(targets, mutationTarget{project: project})
	}
	return m.startMutation("update", pkgName, version, targets, mutationOptions{skipLocked: targetProject == nil}, selection)
}

func (m *App) restore(scope actionScope) bubble_tea.Cmd {
	m.ctx.Restoring = true
	if scope == scopeSelected {
		sel := m.selectedProject()
		if sel != nil && !m.isPropsProject(sel) {
			return runDotnetRestore(m.lifecycle, []*ParsedProject{sel})
		}
	}
	// scopeAll, or "All Projects" selected, or .props file — restore all actual project files.
	return runDotnetRestore(m.lifecycle, m.ctx.ParsedProjects)
}

func runDotnetRestore(ctx context.Context, projects []*ParsedProject) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		var lastErr error
		for _, p := range projects {
			if p.FilePath == "" {
				continue
			}
			logDebug("dotnet restore: %s", p.FilePath)
			cmd := exec.CommandContext(ctx, "dotnet", "restore", p.FilePath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				logWarn("restore failed for %s: %v\n%s", p.FilePath, err, strings.TrimSpace(string(out)))
				lastErr = fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(out)))
			} else {
				logInfo("restore succeeded for %s", p.FileName)
			}
		}
		return restoreResultMsg{err: lastErr}
	}
}

func (m *App) removePackage(pkgName string) bubble_tea.Cmd {
	selected := m.selectedProject()
	projects := m.ctx.ParsedProjects
	if selected != nil {
		projects = []*ParsedProject{selected}
	}
	targets := make([]mutationTarget, 0, len(projects))
	for _, project := range projects {
		targets = append(targets, mutationTarget{project: project})
	}
	return m.startMutation("remove", pkgName, "", targets, mutationOptions{removeInherited: selected == nil}, m.rowReloadSelection())
}
