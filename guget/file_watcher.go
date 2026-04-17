package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	workspaceWatchInterval = 1500 * time.Millisecond
	workspaceWatchDebounce = 500 * time.Millisecond
)

type watchedFileState struct {
	Size    int64
	ModTime int64
}

func isWatchedWorkspaceFile(path string) bool {
	name := filepath.Base(path)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csproj", ".fsproj", ".vbproj", ".props":
		return true
	}
	return strings.EqualFold(name, "nuget.config")
}

func scanWatchedWorkspaceFiles(rootDir string) (map[string]watchedFileState, error) {
	files := make(map[string]watchedFileState)
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if shouldSkipProjectDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isWatchedWorkspaceFile(path) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		files[path] = watchedFileState{
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
		}
		return nil
	})
	return files, err
}

func diffWatchedWorkspaceFiles(prev, next map[string]watchedFileState) []string {
	changed := make([]string, 0)
	for path, state := range next {
		if prior, ok := prev[path]; !ok || prior != state {
			changed = append(changed, path)
		}
	}
	for path := range prev {
		if _, ok := next[path]; !ok {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

// watchWorkspaceFiles polls rootDir and emits reloadRequestedMsg when watched
// files change. Returns a stop func that terminates the watcher goroutine.
// Changes are debounced: bursts within workspaceWatchDebounce coalesce into
// a single reload so editors that rewrite files rapidly don't thrash.
func watchWorkspaceFiles(rootDir string, send func(tea.Msg)) func() {
	if send == nil {
		return func() {}
	}

	stop := make(chan struct{})

	go func() {
		prev, err := scanWatchedWorkspaceFiles(rootDir)
		if err != nil {
			logWarn("workspace watch init failed: %v", err)
			prev = make(map[string]watchedFileState)
		}

		ticker := time.NewTicker(workspaceWatchInterval)
		defer ticker.Stop()

		pending := make(map[string]struct{})
		var quietAt time.Time

		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				next, err := scanWatchedWorkspaceFiles(rootDir)
				if err != nil {
					if !os.IsNotExist(err) {
						logWarn("workspace watch scan failed: %v", err)
					}
					continue
				}

				changed := diffWatchedWorkspaceFiles(prev, next)
				prev = next

				if len(changed) > 0 {
					for _, path := range changed {
						pending[path] = struct{}{}
					}
					quietAt = now.Add(workspaceWatchDebounce)
					continue
				}

				if len(pending) == 0 || now.Before(quietAt) {
					continue
				}

				paths := make([]string, 0, len(pending))
				for path := range pending {
					paths = append(paths, path)
				}
				sort.Strings(paths)
				pending = make(map[string]struct{})

				send(reloadRequestedMsg{
					reason:    "disk changes detected",
					paths:     paths,
					automatic: true,
				})
			}
		}
	}()

	return func() { close(stop) }
}
