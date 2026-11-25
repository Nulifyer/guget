package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
)

// A save that fails, such as one MSBuild evaluation refuses, must not be
// started again on every press of the hold: the hold passes the row, updates
// the rest, and rests on the failed row with its reason.
func TestHeldUpdatePassesARowWhoseSaveFailed(t *testing.T) {
	m, messages, path, now := heldTestApp(t, heldTestProject)
	m.setPackageSort(sortByName)
	m.selectPackageByName("A")
	m.held = heldUpdate{key: "u", run: heldKeyRun, last: *now}
	if pressHeld(m, now, 'u', 1) == nil || !m.ctx.Saving || selectedActionPackage(m) != "A" {
		t.Fatalf("held press did not start A: selected=%s", selectedActionPackage(m))
	}
	m.HandleEvent(writeResultMsg{err: errors.New("evaluated ownership differs"), packageID: "A"})
	if m.ctx.Saving || !strings.Contains(m.ctx.StatusLine, "Save failed: evaluated ownership differs") {
		t.Fatalf("status=%q saving=%v", m.ctx.StatusLine, m.ctx.Saving)
	}
	for _, name := range []string{"B", "C"} {
		save := pressHeld(m, now, 'u', 1)
		if save == nil || !m.ctx.Saving || selectedActionPackage(m) != name {
			t.Fatalf("held press did not start %s after a failed save: selected=%s status=%q", name, selectedActionPackage(m), m.ctx.StatusLine)
		}
		executeActionCmd(t, m, save)
		finishActionReload(t, m, messages)
	}
	if pressHeld(m, now, 'u', 3) != nil || m.ctx.Saving || selectedActionPackage(m) != "A" ||
		!strings.Contains(m.ctx.StatusLine, "Save failed: evaluated ownership differs") || !m.ctx.StatusIsErr {
		t.Fatalf("selected=%s status=%q saving=%v", selectedActionPackage(m), m.ctx.StatusLine, m.ctx.Saving)
	}
	if versions := heldTestVersions(t, path); versions["A"] != "1.0.0" || versions["B"] != "2.0.0" || versions["C"] != "2.0.0" {
		t.Fatalf("versions=%v", versions)
	}
	// Releasing the key forgets the failure: a press afterwards tries again.
	*now = now.Add(time.Second)
	if pressHeld(m, now, 'u', 1) == nil || !m.ctx.Saving || selectedActionPackage(m) != "A" {
		t.Fatalf("a press after the hold did not retry A: status=%q", m.ctx.StatusLine)
	}
}

// A failed save from a single press is not remembered.
func TestSeparatePressesRetryAFailedSave(t *testing.T) {
	m, _, _, now := heldTestApp(t, heldTestProject)
	m.selectPackageByName("A")
	for range 2 {
		*now = now.Add(time.Second)
		if m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'})) == nil || !m.ctx.Saving {
			t.Fatalf("press did not start a save: %q", m.ctx.StatusLine)
		}
		m.HandleEvent(writeResultMsg{err: errors.New("boom"), packageID: "A"})
	}
	if len(m.held.failed) != 0 {
		t.Fatalf("separate presses recorded failures: %v", m.held.failed)
	}
}
