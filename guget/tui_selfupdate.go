package main

import (
	"context"
	"os"

	"github.com/nulifyer/guget/internal/selfupdate"
	ui "github.com/nulifyer/guget/internal/ui"
)

type selfUpdateNoticeMsg struct{ version string }

func (m *App) checkForUpdate() ui.Effect {
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
	return func() ui.Event { return selfUpdateNoticeMsg{version: updater.Notice(ctx, path)} }
}

func (m *App) dismissUpdateNotice() ui.Effect {
	if m.updateVersion == "" {
		return nil
	}
	release, path := m.updateVersion, m.updateCachePath
	m.updateVersion = ""
	return func() ui.Event {
		_ = selfupdate.Dismiss(path, release)
		return nil
	}
}
