package main

import "github.com/nulifyer/guget/internal/ui"

func (m *App) focusedComponent() ui.Element {
	switch m.focus {
	case focusProjects:
		m.projects.app = m
		return &m.projects
	case focusDetail:
		m.detail.app = m
		return &m.detail
	case focusLog:
		m.log.app = m
		return &m.log
	default:
		m.packages.app = m
		return &m.packages
	}
}
func (p *projectPanel) HandleKey(k ui.KeyEvent) ui.Effect {
	switch mainKey(k.String()) {
	case "up":
		p.app.moveProjectCursor(-1)
	case "down":
		p.app.moveProjectCursor(1)
	case "enter":
		p.app.focus = focusPackages
	case "u", "U", "a", "A":
		return p.app.setStatus("Press Enter to focus packages, then select a package to update", false)
	}
	return nil
}
func (p *packagePanel) HandleKey(k ui.KeyEvent) ui.Effect {
	m := p.app
	switch mainKey(k.String()) {
	case "up":
		m.movePackageCursor(-1)
	case "down":
		m.movePackageCursor(1)
	case "u", "U", "a", "A":
		return m.pressUpdateKey(mainKey(k.String()))
	case "v":
		m.openVersionPicker()
	case "n":
		return m.openReleaseNotes()
	case "t":
		return m.openDepTree()
	case "o":
		m.cyclePackageSort()
	case "O":
		m.reversePackageSort()
	case "d":
		if m.canMutate() && m.packages.cursor < len(m.packages.rows) {
			if problem := m.removeProblem(m.packages.rows[m.packages.cursor]); problem != "" {
				return m.refusalStatus("Not removed", problem)
			}
			m.confirmRemove = newConfirmRemove(m, m.packages.rows[m.packages.cursor].ref.Name)
			m.ctx.StatusLine = ""
		}
	}
	return nil
}
func (p *detailPanel) HandleKey(k ui.KeyEvent) ui.Effect {
	switch k.String() {
	case "v":
		p.app.openVersionPicker()
		return nil
	case "n":
		return p.app.openReleaseNotes()
	}
	var effect ui.Effect
	p.vp, effect = p.vp.Update(k)
	return effect
}
func (p *logPanel) HandleKey(k ui.KeyEvent) ui.Effect {
	var effect ui.Effect
	p.vp, effect = p.vp.Update(k)
	return effect
}
