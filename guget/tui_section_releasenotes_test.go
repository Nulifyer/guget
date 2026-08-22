package main

import (
	"context"
	"testing"
)

func TestReleaseNotesIgnoreStaleNuSpecResponse(t *testing.T) {
	app := &App{ctx: &AppContext{}, lifecycle: context.Background()}
	app.releaseNotes.nsVersions = []string{"2.0.0", "1.0.0"}
	app.releaseNotes.nsCursor = 0
	app.releaseNotes.nsLoading = true
	app.releaseNotes.nsNotes = "current"

	app.Update(nuspecVersionNotesReadyMsg{version: "1.0.0", notes: "stale"})
	if app.releaseNotes.nsNotes != "current" || !app.releaseNotes.nsLoading {
		t.Fatalf("stale response changed view: notes=%q loading=%v", app.releaseNotes.nsNotes, app.releaseNotes.nsLoading)
	}
	if got := app.releaseNotes.nsNotesCache["1.0.0"]; got != "stale" {
		t.Fatalf("stale response was not cached: %q", got)
	}

	app.Update(nuspecVersionNotesReadyMsg{version: "2.0.0", notes: "selected"})
	if app.releaseNotes.nsNotes != "selected" || app.releaseNotes.nsLoading {
		t.Fatalf("selected response was not applied: notes=%q loading=%v", app.releaseNotes.nsNotes, app.releaseNotes.nsLoading)
	}
}

func TestReleaseNotesIgnoreStaleGitHubResponse(t *testing.T) {
	app := &App{ctx: &AppContext{}, lifecycle: context.Background()}
	app.releaseNotes.ghReleases = []GitHubRelease{{TagName: "v2"}, {TagName: "v1"}}
	app.releaseNotes.ghCursor = 0
	app.releaseNotes.ghLoading = true
	app.releaseNotes.ghNotes = "current"

	app.Update(releaseNotesReadyMsg{tag: "v1", body: "stale"})
	if app.releaseNotes.ghNotes != "current" || !app.releaseNotes.ghLoading {
		t.Fatalf("stale response changed view: notes=%q loading=%v", app.releaseNotes.ghNotes, app.releaseNotes.ghLoading)
	}

	app.Update(releaseNotesReadyMsg{tag: "v2", body: "selected"})
	if app.releaseNotes.ghNotes != "selected" || app.releaseNotes.ghLoading {
		t.Fatalf("selected response was not applied: notes=%q loading=%v", app.releaseNotes.ghNotes, app.releaseNotes.ghLoading)
	}
}
