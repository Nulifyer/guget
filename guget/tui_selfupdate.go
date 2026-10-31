package main

import (
	"context"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/nulifyer/guget/internal/selfupdate"
)

type selfUpdateNoticeMsg struct{ version string }

func (m *App) checkForUpdate() tea.Cmd {
	if m.updateChecksDisabled || os.Getenv("GUGET_NO_UPDATE_CHECK") == "1" {
		return nil
	}
	ctx := m.lifecycle
	if ctx == nil {
		ctx = context.Background()
	}
	if m.updateCachePath == "" {
		m.updateCachePath = selfupdate.CachePath()
	}
	path := m.updateCachePath
	updater := selfupdate.New(version, nil)
	return func() tea.Msg { return selfUpdateNoticeMsg{version: updater.Notice(ctx, path)} }
}

func (m *App) dismissUpdateNotice() tea.Cmd {
	if m.updateVersion == "" {
		return nil
	}
	release, path := m.updateVersion, m.updateCachePath
	m.updateVersion = ""
	return func() tea.Msg {
		_ = selfupdate.Dismiss(path, release)
		return nil
	}
}
