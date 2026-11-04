package main

import (
	"context"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
)

func TestDependencyViewCancelsOnlyItsOwnWork(t *testing.T) {
	for _, action := range []string{"escape", "replace", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			m, _, _ := newActionTestApp(t, "")
			m.openTransitiveDepTree()
			prior := m.depTree.ctx
			if prior == nil || prior.Err() != nil {
				t.Fatal("dependency request has no live context")
			}
			switch action {
			case "escape":
				m.depTree.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEscape}))
			case "replace":
				m.openTransitiveDepTree()
			case "disconnect":
				m.Disconnect()
			}
			if prior.Err() != context.Canceled {
				t.Fatal("abandoned dependency request remains active")
			}
			if m.lifecycle.Err() != nil {
				t.Fatal("dependency cancellation canceled the application")
			}
			if action != "replace" {
				m.openTransitiveDepTree()
			}
			if m.depTree.ctx.Err() != nil || !m.depTree.active {
				t.Fatal("a new dependency request cannot start after cancellation")
			}
			m.depTree.closeOverlay()
		})
	}
}
