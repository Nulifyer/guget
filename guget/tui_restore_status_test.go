package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
)

func TestRestoreSummarySaysHowManyProjectsRanAndWhy(t *testing.T) {
	failure := errors.New("exit status 1")
	for _, test := range []struct {
		msg   restoreResultMsg
		want  string
		isErr bool
	}{
		{restoreResultMsg{}, "✓ Restore complete", false},
		{restoreResultMsg{projects: 1, only: "Web.csproj"}, "✓ Restored Web.csproj", false},
		{restoreResultMsg{projects: 3, only: "Web.csproj"}, "✓ Restored 3 projects", false},
		{restoreResultMsg{projects: 3, affected: 2}, "✓ Restored 3 projects (2 affected by package edits)", false},
		{restoreResultMsg{projects: 2, affected: 1, regenerated: 1}, "✓ Restored 2 projects (1 affected by package edits, 1 lock file regenerated)", false},
		{restoreResultMsg{projects: 1, only: "Web.csproj", regenerated: 1}, "✓ Restored Web.csproj (1 lock file regenerated)", false},
		{restoreResultMsg{err: failure}, "✗ Restore failed; l for logs", true},
		{restoreResultMsg{err: failure, projects: 1, failed: 1, only: "Web.csproj"}, "✗ Restore failed for Web.csproj; l for logs", true},
		{restoreResultMsg{err: failure, projects: 3, failed: 2}, "✗ Restore failed for 2 of 3 projects; l for logs", true},
	} {
		if got, isErr := restoreSummary(test.msg); got != test.want || isErr != test.isErr {
			t.Errorf("restoreSummary(%+v) = %q, %v; want %q, %v", test.msg, got, isErr, test.want, test.isErr)
		}
	}
	if restoringStatus(1) != " restoring..." || restoringStatus(3) != " restoring 3 projects..." {
		t.Fatalf("restoring status: %q, %q", restoringStatus(1), restoringStatus(3))
	}

	m, _, _ := newActionTestApp(t, "")
	m.ctx.Restoring, m.restoringProjects = true, 3
	if footer := text.Strip(m.renderFooter()); !strings.Contains(footer, "restoring 3 projects...") {
		t.Fatalf("footer lacks the restore size:\n%s", footer)
	}
	m.HandleEvent(restoreResultMsg{projects: 3, affected: 2})
	if m.ctx.Restoring || m.restoringProjects != 0 || m.ctx.StatusLine != "✓ Restored 3 projects (2 affected by package edits)" || m.ctx.StatusIsErr {
		t.Fatalf("status %q", m.ctx.StatusLine)
	}
}

// A refusal explains what to do instead. The status row cuts it short, so it
// has to point at the log panel, and the log panel has to show all of it.
func TestRefusedSaveStaysReadable(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, path := newActionTestApp(t, "")
	owner := filepath.Join(filepath.Dir(path), "Directory.Packages.props")
	refusal := fmt.Errorf("package %q is a GlobalPackageReference in %s and applies to every project that imports that file; remove it from that file for all of them", "A", owner)

	m.HandleEvent(ui.ResizeEvent{Width: 100, Height: 30})
	m.HandleEvent(writeResultMsg{err: refusal})
	status := text.Strip(m.ctx.StatusLine)
	if !m.ctx.StatusIsErr || !strings.HasPrefix(status, "▲ Save failed (l for logs): ") || strings.Contains(status, filepath.Dir(path)) || !strings.Contains(status, "GlobalPackageReference in Directory.Packages.p") {
		t.Fatalf("status %q", status)
	}
	if text.StringWidth(status) > 100 {
		t.Fatalf("status is %d cells", text.StringWidth(status))
	}

	// A short failure needs no pointer to the logs.
	m.HandleEvent(writeResultMsg{err: errors.New("disk full")})
	if m.ctx.StatusLine != "▲ Save failed: disk full" {
		t.Fatalf("short status %q", m.ctx.StatusLine)
	}

	m.ctx.LogLines = []string{"[INFO] short entry", "[WARN] Save failed: " + refusal.Error()}
	m.HandleEvent(ui.KeyPress(ui.Key{Code: 'l'}))
	width := m.log.vp.Width()
	if width <= 0 {
		t.Fatal("log panel has no width")
	}
	logged := strings.Split(text.Strip(m.log.vp.GetContent()), "\n")
	if len(logged) < 3 {
		t.Fatalf("long entry was not wrapped: %q", logged)
	}
	for _, line := range logged {
		if text.StringWidth(line) > width {
			t.Fatalf("log line is %d cells in a %d-cell panel: %q", text.StringWidth(line), width, line)
		}
	}
	if joined := strings.Join(strings.Fields(strings.Join(logged, "")), ""); !strings.Contains(joined, "removeitfromthatfileforallofthem") {
		t.Fatalf("the end of the refusal is not in the log panel: %q", logged)
	}

	// Entries rewrap when the terminal narrows.
	m.HandleEvent(ui.ResizeEvent{Width: 80, Height: 30})
	m.relayout()
	for _, line := range strings.Split(text.Strip(m.log.vp.GetContent()), "\n") {
		if text.StringWidth(line) > m.log.vp.Width() {
			t.Fatalf("log line kept its old width after resize: %q", line)
		}
	}
}
