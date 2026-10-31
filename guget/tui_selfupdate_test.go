package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/nulifyer/guget/internal/selfupdate"
)

func noticeTestApp(t *testing.T) *App {
	t.Helper()
	oldFooter := styleFooterBar
	styleFooterBar = lipgloss.NewStyle().BorderTop(true).BorderStyle(lipgloss.NormalBorder()).Padding(0, 2)
	t.Cleanup(func() { styleFooterBar = oldFooter })
	m := NewApp(".", &workspaceSnapshot{}, nil, BuiltFlags{})
	m.ctx.Width, m.ctx.Height = 120, 30
	m.updateCachePath = filepath.Join(t.TempDir(), "update.json")
	return m
}

func TestTUIUpdateNoticeIsAsynchronousCachedAndDismissible(t *testing.T) {
	old := version
	version = "1.2.0"
	t.Cleanup(func() { version = old })
	t.Setenv("GUGET_NO_UPDATE_CHECK", "")
	m := noticeTestApp(t)
	cache := map[string]any{"checked": time.Now(), "version": "1.3.0"}
	data, _ := json.Marshal(cache)
	if err := os.WriteFile(m.updateCachePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	before := m.footerLines()
	cmd := m.checkForUpdate()
	if cmd == nil || m.updateVersion != "" {
		t.Fatal("check did not defer work to a command")
	}
	m.Update(cmd())
	footer := m.renderFooter()
	for _, text := range []string{"guget 1.3.0 available", "guget self-update", "ctrl+x dismiss"} {
		if !strings.Contains(footer, text) {
			t.Fatalf("missing %q in %q", text, footer)
		}
	}
	if m.footerLines() != before {
		t.Fatal("notice changed footer height")
	}
	_, dismiss := m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	if m.updateVersion != "" {
		t.Fatal("dismiss did not immediately hide notice")
	}
	executeActionCmd(t, m, dismiss)
	if got := selfupdate.New("1.2.0", nil).Notice(m.lifecycle, m.updateCachePath); got != "" {
		t.Fatalf("dismissal did not persist: %q", got)
	}
}

func TestTUIUpdateNoticeYieldsToOperationsAndFitsFooter(t *testing.T) {
	m := noticeTestApp(t)
	m.Update(selfUpdateNoticeMsg{version: "1.3.0"})
	for _, state := range []string{"saving", "restoring", "reloading", "loading", "status"} {
		m.ctx.Saving = state == "saving"
		m.ctx.Restoring = state == "restoring"
		m.ctx.Reloading = state == "reloading"
		m.ctx.LoadingTotal = 0
		if state == "loading" {
			m.ctx.LoadingTotal = 1
		}
		m.ctx.StatusLine = ""
		if state == "status" {
			m.ctx.StatusLine = "operation failed"
			m.ctx.StatusIsErr = true
		}
		footer := m.renderFooter()
		if strings.Contains(footer, "1.3.0 available") || !strings.Contains(footer, map[string]string{"saving": "saving", "restoring": "restoring", "reloading": "reloading", "loading": "loading packages", "status": "operation failed"}[state]) {
			t.Fatalf("state=%s footer=%q", state, footer)
		}
	}
	m.ctx.StatusLine = ""
	m.ctx.LoadingTotal = 0
	for _, width := range []int{80, 100, 160} {
		m.ctx.Width = width
		if got := lipgloss.Height(m.renderFooter()); got != m.footerLines()+1 {
			t.Fatalf("width=%d rendered=%d calculated=%d", width, got, m.footerLines()+1)
		}
	}
	m.Update(selfUpdateNoticeMsg{})
	if strings.Contains(m.renderFooter(), "available") {
		t.Fatal("empty notice displayed")
	}
}

func TestTUIUpdateChecksCanBeDisabled(t *testing.T) {
	m := noticeTestApp(t)
	m.updateChecksDisabled = true
	if m.checkForUpdate() != nil {
		t.Fatal("flag did not disable checks")
	}
	m.updateChecksDisabled = false
	t.Setenv("GUGET_NO_UPDATE_CHECK", "1")
	if m.checkForUpdate() != nil {
		t.Fatal("environment did not disable checks")
	}
	flags, _ := parseRegisteredCLIForTest(t, "--no-update-check")
	if !flags.NoUpdateCheck {
		t.Fatal("flag was not parsed")
	}
}

func TestTUIUpdateNoticeSurvivesStartupSuccessStatus(t *testing.T) {
	m := noticeTestApp(t)
	m.activeReload = reloadRequestedMsg{reason: "initial load"}
	m.finishReloadSuccess()
	m.Update(selfUpdateNoticeMsg{version: "1.3.0"})
	for _, width := range []int{80, 120} {
		m.ctx.Width = width
		footer := m.renderFooter()
		for _, text := range []string{"reloaded", "guget 1.3.0 available", "guget self-update"} {
			if !strings.Contains(footer, text) {
				t.Fatalf("width=%d missing %q in %q", width, text, footer)
			}
		}
		if lipgloss.Height(footer) != m.footerLines()+1 || lipgloss.Width(footer) > width {
			t.Fatalf("notice overflow at width %d", width)
		}
	}
}
