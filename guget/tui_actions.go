package main

import (
	"fmt"
	"os/exec"
	"strings"

	bubble_tea "charm.land/bubbletea/v2"
)

func (m *Model) updatePackage(useStable bool, scope actionScope) bubble_tea.Cmd {
	if m.packageCursor >= len(m.packageRows) {
		return nil
	}
	row := m.packageRows[m.packageCursor]
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

func (m *Model) isPropsProject(p *ParsedProject) bool {
	for _, pp := range m.ctx.PropsProjects {
		if pp == p {
			return true
		}
	}
	return false
}

// allProjects returns every project (parsed + props) for propagation purposes.
func (m *Model) allProjects() []*ParsedProject {
	all := make([]*ParsedProject, 0, len(m.ctx.ParsedProjects)+len(m.ctx.PropsProjects))
	all = append(all, m.ctx.ParsedProjects...)
	all = append(all, m.ctx.PropsProjects...)
	return all
}

func (m *Model) applyVersion(pkgName, version string, targetProject *ParsedProject) bubble_tea.Cmd {
	projects := m.ctx.ParsedProjects
	if targetProject != nil {
		projects = []*ParsedProject{targetProject}
	}
	type writeTarget struct {
		filePath string
	}
	var toWrite []writeTarget
	// Determine the on-disk source file so we know which .props (if any) to propagate.
	var propsSource string
	skippedLocked := 0
	for _, p := range projects {
		updated := NewSet[PackageReference]()
		changed := false
		for ref := range p.Packages {
			if ref.Name == pkgName {
				if targetProject == nil && ref.Locked {
					// scope=all: skip locked versions, track count for status warning
					skippedLocked++
				} else {
					ref.Version = ParseSemVer(version)
					changed = true
				}
			}
			updated.Add(ref)
		}
		p.Packages = updated
		if changed {
			sourceFile := p.SourceFileForPackage(pkgName)
			if sourceFile != "" {
				toWrite = append(toWrite, writeTarget{filePath: sourceFile})
				if strings.HasSuffix(strings.ToLower(sourceFile), ".props") {
					propsSource = sourceFile
				}
			}
		}
	}
	// When the package lives in a .props file, propagate the version change
	// to every other project that inherits from the same file.
	if propsSource != "" {
		for _, p := range m.allProjects() {
			if p.SourceFileForPackage(pkgName) != propsSource {
				continue
			}
			updated := NewSet[PackageReference]()
			for ref := range p.Packages {
				if ref.Name == pkgName {
					ref.Version = ParseSemVer(version)
				}
				updated.Add(ref)
			}
			p.Packages = updated
		}
	}
	m.rebuildPackageRows()
	m.refreshDetail()

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
	written := len(toWrite)
	return func() bubble_tea.Msg {
		seen := make(map[string]bool)
		for _, wt := range toWrite {
			if seen[wt.filePath] {
				continue
			}
			seen[wt.filePath] = true
			logDebug("writing %s to %s", pkgName, wt.filePath)
			if err := UpdatePackageVersion(wt.filePath, pkgName, version); err != nil {
				logWarn("write failed for %s: %v", wt.filePath, err)
				return writeResultMsg{err: err}
			}
		}
		return writeResultMsg{err: nil, written: written, skipped: skippedLocked}
	}
}

func (m *Model) restore(scope actionScope) bubble_tea.Cmd {
	m.ctx.Restoring = true
	if scope == scopeSelected {
		sel := m.selectedProject()
		if sel != nil && !m.isPropsProject(sel) {
			return runDotnetRestore([]*ParsedProject{sel})
		}
	}
	// scopeAll, or "All Projects" selected, or .props file — restore all actual project files.
	return runDotnetRestore(m.ctx.ParsedProjects)
}

func runDotnetRestore(projects []*ParsedProject) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		var lastErr error
		for _, p := range projects {
			if p.FilePath == "" {
				continue
			}
			logDebug("dotnet restore: %s", p.FilePath)
			cmd := exec.Command("dotnet", "restore", p.FilePath)
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

func (m *Model) removePackage(pkgName string) bubble_tea.Cmd {
	targetProject := m.selectedProject() // nil = all projects
	type writeTarget struct {
		filePath string
	}
	var toWrite []writeTarget
	var propsSource string

	// Determine which projects to operate on.
	projects := m.ctx.ParsedProjects
	if targetProject != nil {
		if m.isPropsProject(targetProject) {
			projects = []*ParsedProject{targetProject}
		} else {
			projects = []*ParsedProject{targetProject}
		}
	}

	for _, p := range projects {
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, pkgName) {
				sourceFile := p.SourceFileForPackage(pkgName)
				p.Packages.Remove(ref)
				if sourceFile != "" {
					toWrite = append(toWrite, writeTarget{filePath: sourceFile})
					if strings.HasSuffix(strings.ToLower(sourceFile), ".props") {
						propsSource = sourceFile
					}
				}
				delete(p.PackageSources, strings.ToLower(pkgName))
				break
			}
		}
	}

	// When the package lived in a .props file, propagate the removal to
	// every other project that inherited it from the same file.
	if propsSource != "" {
		for _, p := range m.allProjects() {
			if p.SourceFileForPackage(pkgName) != propsSource {
				continue
			}
			for ref := range p.Packages {
				if strings.EqualFold(ref.Name, pkgName) {
					p.Packages.Remove(ref)
					delete(p.PackageSources, strings.ToLower(pkgName))
					break
				}
			}
		}
	}

	// Clean up results cache if the package is gone from every project.
	stillExists := false
	for _, p := range m.allProjects() {
		for ref := range p.Packages {
			if strings.EqualFold(ref.Name, pkgName) {
				stillExists = true
				break
			}
		}
		if stillExists {
			break
		}
	}
	if !stillExists {
		delete(m.ctx.Results, pkgName)
	}

	m.rebuildPackageRows()
	if m.packageCursor >= len(m.packageRows) && len(m.packageRows) > 0 {
		m.packageCursor = len(m.packageRows) - 1
	}
	m.clampOffset()
	m.refreshDetail()

	logInfo("removePackage: %s (%d file(s) to write)", pkgName, len(toWrite))
	if len(toWrite) == 0 {
		return nil
	}
	return func() bubble_tea.Msg {
		seen := make(map[string]bool)
		for _, wt := range toWrite {
			if seen[wt.filePath] {
				continue
			}
			seen[wt.filePath] = true
			logDebug("RemovePackageReference: %s from %s", pkgName, wt.filePath)
			if err := RemovePackageReference(wt.filePath, pkgName); err != nil {
				logWarn("remove failed for %s: %v", wt.filePath, err)
				return writeResultMsg{err: err}
			}
		}
		return writeResultMsg{err: nil}
	}
}
