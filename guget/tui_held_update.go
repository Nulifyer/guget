package main

import (
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
)

const (
	// heldKeyWindow is the longest gap between two presses of one update key
	// that still counts as the key being held. It covers slow keyboard repeat
	// rates (about 11 per second) with room for delivery jitter.
	heldKeyWindow = 125 * time.Millisecond
	// heldKeyRun is how many presses in a row, each within heldKeyWindow of
	// the one before, mean the key is held. Two quick taps stay two taps.
	heldKeyRun = 3
)

// heldUpdate tracks an update key (u, U, a, or A) the user keeps pressed.
// A terminal reports a held key as repeated presses and never reports the
// release, so the hold is inferred from the time between presses.
//
// Each press of a held key updates one package, as a single press does, but
// it skips rows that cannot be updated, so the hold sweeps the whole list.
// Every update still starts from a press: nothing is queued, and releasing
// the key starts nothing more.
type heldUpdate struct {
	key  string
	last time.Time // when the latest press arrived
	run  int       // presses in a row no further apart than heldKeyWindow
}

// pressUpdateKey handles u, U, a, or A in the package panel.
func (m *App) pressUpdateKey(key string) ui.Effect {
	now := m.timeNow()
	if m.held.key == key && now.Sub(m.held.last) <= heldKeyWindow {
		m.held.run++
	} else {
		m.held = heldUpdate{key: key, run: 1}
	}
	m.held.last = now
	useStable, scope := false, scopeSelected
	switch key {
	case "U":
		scope = scopeAll
	case "a":
		useStable = true
	case "A":
		useStable, scope = true, scopeAll
	}
	if m.held.run < heldKeyRun {
		return m.updatePackage(useStable, scope)
	}
	if m.ctx.Saving || m.ctx.Reloading {
		// The next press after the save or reload continues the sweep. Leave
		// the status row alone so it keeps showing the last result.
		return nil
	}
	return m.sweepUpdate(useStable, scope)
}

// endHeldUpdate forgets the held key: another key or the mouse took over.
func (m *App) endHeldUpdate() { m.held = heldUpdate{} }

// sweepUpdate updates the first row at or after the cursor that the held key
// can update, wrapping to the top, and moves the cursor there. A row that
// needs a confirmation or a decision is never written by a sweep. When no row
// can be updated, the cursor moves to the first row that still has an update
// to decide about and the status row gives that row's reason.
func (m *App) sweepUpdate(useStable bool, scope actionScope) ui.Effect {
	if effect, refused := m.updateRefusedEverywhere(); refused {
		return effect
	}
	rows := m.packages.rows
	index := indexUpdateDependencyReferences(m.ctx.ParsedProjects)
	undecided, reason, reasonIsErr := -1, "", false
	note := func(at int, text string, isErr bool) {
		if undecided < 0 || at < undecided {
			undecided, reason, reasonIsErr = at, text, isErr
		}
	}
	var here quickUpdatePlan
	for i := range rows {
		at := (m.packages.cursor + i) % len(rows)
		plan := m.planQuickUpdate(rows[at], useStable, scope, index)
		if i == 0 {
			here = plan
		}
		switch {
		case plan.version == "":
			if !plan.settled {
				note(at, plan.refusal, plan.isErr)
			}
			continue
		case m.updateNeedsConfirmation(plan.name, plan.project):
			note(at, plan.name+" is pinned; release the key and press it again to confirm", false)
			continue
		}
		projects := m.ctx.ParsedProjects
		if plan.project != nil {
			projects = []*ParsedProject{plan.project}
		}
		if problem := m.sharedOwnerProblem(plan.name, plan.version, declaredPackageTargets(projects, plan.name)); problem != "" {
			note(at, "▲ Not updated: "+problem, true)
			continue
		}
		m.selectPackageIndex(at)
		return m.applyVersionWithSelection(plan.name, plan.version, plan.project, m.rowReloadSelection())
	}
	if undecided >= 0 {
		m.selectPackageIndex(undecided)
		return m.setStatus(reason, reasonIsErr)
	}
	return m.setStatus(here.refusal, here.isErr)
}
