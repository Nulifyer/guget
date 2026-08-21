package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
	editplan "github.com/nulifyer/guget/internal/edit"
)

func (m *App) updatePackage(useStable bool, scope actionScope) bubble_tea.Cmd {
	if m.packages.cursor >= len(m.packages.rows) {
		return nil
	}
	row := m.packages.rows[m.packages.cursor]
	if row.err != nil {
		return nil
	}
	var target *PackageVersion
	if useStable {
		target = row.latestStable
	} else {
		target = row.latestCompatible
	}
	if target == nil {
		return nil
	}
	var project *ParsedProject
	if scope == scopeSelected {
		project = m.selectedProject()
	}
	return m.applyOrConfirmUpdate(row.ref.Name, target.SemVer.String(), project)
}

func (m *App) isPropsProject(p *ParsedProject) bool {
	for _, pp := range m.ctx.PropsProjects {
		if pp == p {
			return true
		}
	}
	return false
}

// allProjects returns every project (parsed + props) for propagation purposes.
func (m *App) allProjects() []*ParsedProject {
	all := make([]*ParsedProject, 0, len(m.ctx.ParsedProjects)+len(m.ctx.PropsProjects))
	all = append(all, m.ctx.ParsedProjects...)
	all = append(all, m.ctx.PropsProjects...)
	return all
}

func (m *App) applyVersion(pkgName, version string, targetProject *ParsedProject) bubble_tea.Cmd {
	projects := m.ctx.ParsedProjects
	if targetProject != nil {
		projects = []*ParsedProject{targetProject}
	}
	var toWrite []string
	// Determine the on-disk source file so we know which .props (if any) to propagate.
	skippedLocked := 0
	for _, p := range projects {
		for ref := range p.Packages {
			if ref.Name == pkgName {
				if targetProject == nil && ref.Locked {
					// scope=all: skip locked versions, track count for status warning
					skippedLocked++
				} else {
					sourceFile := p.SourceFileForPackage(pkgName)
					if sourceFile != "" {
						toWrite = append(toWrite, sourceFile)
					}
				}
			}
		}
	}

	if skippedLocked > 0 {
		logWarn("applyVersion: %s → %s (%d locked project(s) skipped)", pkgName, version, skippedLocked)
	}

	logInfo("applyVersion: %s → %s (%d file(s) to write, %d locked skipped)", pkgName, version, len(toWrite), skippedLocked)
	if len(toWrite) == 0 {
		if skippedLocked > 0 {
			m.setStatus(fmt.Sprintf("🔒 %d skipped (version locked)", skippedLocked), false)
		}
		return nil
	}
	return func() bubble_tea.Msg {
		seen := make(map[string]bool)
		var changes []editplan.Change
		for _, fp := range toWrite {
			if seen[fp] {
				continue
			}
			seen[fp] = true
			change, err := PlanUpdatePackageVersion(fp, pkgName, version)
			if err != nil {
				logWarn("write failed for %s: %v", fp, err)
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
		return writeResultMsg{written: plan.Len(), skipped: skippedLocked, reload: true}
	}
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
	targetProject := m.selectedProject() // nil = all projects
	var toWrite []string

	projects := m.ctx.ParsedProjects
	if targetProject != nil {
		projects = []*ParsedProject{targetProject}
	}

	for _, p := range projects {
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, pkgName) {
				if targetProject != nil {
					local, err := fileHasPackageReference(p.FilePath, pkgName)
					if err != nil || !local {
						return m.setStatus(fmt.Sprintf("read-only: %s is inherited from %s", pkgName, p.SourceFileForPackage(pkgName)), true)
					}
				}
				// The project owns its PackageReference even when a central props
				// file owns the version. For a workspace-wide removal, also plan
				// against an imported props source; the editor only removes actual
				// PackageReference nodes and preserves PackageVersion nodes.
				toWrite = append(toWrite, p.FilePath)
				if targetProject == nil {
					sourceFile := p.SourceFileForPackage(pkgName)
					if strings.HasSuffix(strings.ToLower(sourceFile), ".props") {
						toWrite = append(toWrite, sourceFile)
					}
				}
				break
			}
		}
	}

	logInfo("removePackage: %s (%d file(s) to write)", pkgName, len(toWrite))
	if len(toWrite) == 0 {
		return nil
	}
	return func() bubble_tea.Msg {
		seen := make(map[string]bool)
		var changes []editplan.Change
		for _, fp := range toWrite {
			if seen[fp] {
				continue
			}
			seen[fp] = true
			change, err := PlanRemovePackageReference(fp, pkgName)
			if err != nil {
				logWarn("remove failed for %s: %v", fp, err)
				return writeResultMsg{err: err}
			}
			changes = append(changes, change)
		}
		plan, err := editplan.NewPlan(changes...)
		if err != nil {
			return writeResultMsg{err: err}
		}
		if plan.Len() == 0 {
			return writeResultMsg{err: fmt.Errorf("package %q is inherited and cannot be removed from the selected project safely", pkgName)}
		}
		if _, err := plan.Apply(); err != nil {
			return writeResultMsg{err: err}
		}
		return writeResultMsg{written: plan.Len(), reload: true}
	}
}
