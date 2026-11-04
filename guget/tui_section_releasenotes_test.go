package main

import (
	"context"
	"testing"
)

func TestReleaseNotesIgnoreStaleNuSpecResponse(t *testing.T) {
	app := &App{ctx: &AppContext{}, lifecycle: context.Background()}
	app.releaseNotes.active = true
	app.releaseNotes.nsVersions = []string{"2.0.0", "1.0.0"}
	app.releaseNotes.nsCursor = 0
	app.releaseNotes.nsLoading = true
	app.releaseNotes.nsNotes = "current"

	app.HandleEvent(nuspecVersionNotesReadyMsg{version: "1.0.0", notes: "stale"})
	if app.releaseNotes.nsNotes != "current" || !app.releaseNotes.nsLoading {
		t.Fatalf("stale response changed view: notes=%q loading=%v", app.releaseNotes.nsNotes, app.releaseNotes.nsLoading)
	}
	if got := app.releaseNotes.nsNotesCache["1.0.0"]; got != "stale" {
		t.Fatalf("stale response was not cached: %q", got)
	}

	app.HandleEvent(nuspecVersionNotesReadyMsg{version: "2.0.0", notes: "selected"})
	if app.releaseNotes.nsNotes != "selected" || app.releaseNotes.nsLoading {
		t.Fatalf("selected response was not applied: notes=%q loading=%v", app.releaseNotes.nsNotes, app.releaseNotes.nsLoading)
	}
}

func TestReleaseNotesIgnoreStaleGitHubResponse(t *testing.T) {
	app := &App{ctx: &AppContext{}, lifecycle: context.Background()}
	app.releaseNotes.active = true
	app.releaseNotes.ghReleases = []GitHubRelease{{TagName: "v2"}, {TagName: "v1"}}
	app.releaseNotes.ghCursor = 0
	app.releaseNotes.ghLoading = true
	app.releaseNotes.ghNotes = "current"

	app.HandleEvent(releaseNotesReadyMsg{tag: "v1", body: "stale"})
	if app.releaseNotes.ghNotes != "current" || !app.releaseNotes.ghLoading {
		t.Fatalf("stale response changed view: notes=%q loading=%v", app.releaseNotes.ghNotes, app.releaseNotes.ghLoading)
	}

	app.HandleEvent(releaseNotesReadyMsg{tag: "v2", body: "selected"})
	if app.releaseNotes.ghNotes != "selected" || app.releaseNotes.ghLoading {
		t.Fatalf("selected response was not applied: notes=%q loading=%v", app.releaseNotes.ghNotes, app.releaseNotes.ghLoading)
	}
}

func TestReleaseNotesFirstOpenAndDisconnect(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	// The previous overlay is zero-valued on the first opening.
	m.openReleaseNotes()
	if !m.releaseNotes.active || m.releaseNotes.ctx.Err() != nil {
		t.Fatal("first opening did not create a live notes session")
	}
	ctx := m.releaseNotes.ctx
	m.Disconnect()
	if ctx.Err() == nil {
		t.Fatal("disconnect left release-note work running")
	}
}
