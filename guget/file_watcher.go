package main

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
)

const (
	workspaceWatchInterval = 1500 * time.Millisecond
	workspaceWatchDebounce = 500 * time.Millisecond
	workspaceWatchMaxDelay = 5 * time.Second
)

type watchedFileState struct {
	Size    int64
	ModTime int64
	Hash    [sha256.Size]byte
	Owner   string
}

// workspaceWatchInputs shares immutable input lists and acknowledged revisions
// between the event loop and watcher. The watcher never reads App state.
type workspaceWatchInputs struct {
	mu           sync.RWMutex
	paths        []string
	acknowledged map[string]watchedFileState
}

func (w *workspaceWatchInputs) setPaths(paths []string) {
	w.mu.Lock()
	w.paths = append([]string(nil), paths...)
	// A new authoritative snapshot replaces the revisions displayed previously.
	// Keeping old acknowledgements could hide a later external revert.
	w.acknowledged = nil
	w.mu.Unlock()
}

func (w *workspaceWatchInputs) inputPaths() []string {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return append([]string(nil), w.paths...)
}

func (w *workspaceWatchInputs) acknowledge(states map[string]watchedFileState) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.acknowledged == nil {
		w.acknowledged = make(map[string]watchedFileState)
	}
	for path, state := range states {
		w.acknowledged[path] = state
	}
}

func (w *workspaceWatchInputs) isAcknowledged(path string, state watchedFileState, exists bool) bool {
	if w == nil || !exists {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	prior, ok := w.acknowledged[path]
	// A metadata-only touch of the exact committed bytes needs no rescan.
	return ok && prior.Hash == state.Hash && prior.Owner == state.Owner
}

type watchedWorkspaceChangeMsg struct {
	paths  []string
	states map[string]watchedFileState
}

func isWatchedWorkspaceFile(path string) bool {
	name := filepath.Base(path)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csproj", ".fsproj", ".vbproj", ".props", ".targets":
		return true
	}
	return strings.EqualFold(name, "nuget.config")
}

func readWatchedFileState(path string) (watchedFileState, bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return watchedFileState{}, false, nil
	}
	if err != nil {
		return watchedFileState{}, false, err
	}
	if !info.Mode().IsRegular() {
		return watchedFileState{}, false, nil
	}
	owner, err := filepath.EvalSymlinks(path)
	if err != nil {
		return watchedFileState{}, false, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return watchedFileState{}, false, nil
	}
	if err != nil {
		return watchedFileState{}, false, err
	}
	return watchedFileState{Size: int64(len(data)), ModTime: info.ModTime().UnixNano(), Hash: sha256.Sum256(data), Owner: owner}, true, nil
}

func scanWatchedWorkspaceFiles(rootDir string, extraPaths ...string) (map[string]watchedFileState, error) {
	files := make(map[string]watchedFileState)
	add := func(path string) error {
		state, exists, err := readWatchedFileState(path)
		if err == nil && exists {
			files[path] = state
		}
		return err
	}
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
		return add(path)
	})
	if err != nil {
		return files, err
	}
	for _, path := range extraPaths {
		if _, exists := files[path]; !exists {
			if err := add(path); err != nil {
				return files, err
			}
		}
	}
	return files, nil
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

// workspaceWatchBatch is the debounce state. A maximum delay ensures continuous
// editor saves cannot indefinitely postpone synchronization.
type workspaceWatchBatch struct {
	pending   map[string]struct{}
	quietAt   time.Time
	startedAt time.Time
}

func (b *workspaceWatchBatch) update(now time.Time, changed []string) []string {
	if len(changed) > 0 {
		if len(b.pending) == 0 {
			b.pending = make(map[string]struct{})
			b.startedAt = now
		}
		for _, path := range changed {
			b.pending[path] = struct{}{}
		}
		b.quietAt = now.Add(workspaceWatchDebounce)
	}
	if len(b.pending) == 0 || (now.Before(b.quietAt) && now.Before(b.startedAt.Add(workspaceWatchMaxDelay))) {
		return nil
	}
	paths := make([]string, 0, len(b.pending))
	for path := range b.pending {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	b.pending = nil
	return paths
}

// watchWorkspaceFiles polls workspace and explicitly loaded external inputs.
// Acknowledgements identify exact committed bytes, never suppression windows.
func watchWorkspaceFiles(rootDir string, send func(ui.Event), registries ...*workspaceWatchInputs) func() {
	if send == nil {
		return func() {}
	}
	var inputs *workspaceWatchInputs
	if len(registries) > 0 {
		inputs = registries[0]
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		prev, err := scanWatchedWorkspaceFiles(rootDir, inputs.inputPaths()...)
		if err != nil {
			logWarn("workspace watch init failed: %v", err)
			prev = make(map[string]watchedFileState)
		}
		ticker := time.NewTicker(workspaceWatchInterval)
		defer ticker.Stop()
		var batch workspaceWatchBatch
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				next, err := scanWatchedWorkspaceFiles(rootDir, inputs.inputPaths()...)
				if err != nil {
					if !os.IsNotExist(err) {
						logWarn("workspace watch scan failed: %v", err)
					}
					continue
				}
				changed := diffWatchedWorkspaceFiles(prev, next)
				prev = next
				paths := batch.update(now, changed)
				if len(paths) > 0 {
					send(watchedWorkspaceChangeMsg{paths: paths, states: next})
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
