package main

import (
	"crypto/sha256"
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
	baselines    map[string]watchedFileState
	acknowledged map[string]watchedFileState
}

func (w *workspaceWatchInputs) setPaths(paths []string, revisions ...map[string]watchedFileState) {
	w.mu.Lock()
	w.paths = append([]string(nil), paths...)
	w.baselines = make(map[string]watchedFileState)
	if len(revisions) > 0 {
		for path, state := range revisions[0] {
			w.baselines[path] = state
		}
	}
	// A new authoritative snapshot replaces the revisions displayed previously.
	// Keeping old acknowledgements could hide a later external revert.
	w.acknowledged = nil
	w.mu.Unlock()
}

func (w *workspaceWatchInputs) changes(prev, next map[string]watchedFileState) []string {
	changed := diffWatchedWorkspaceFiles(prev, next)
	if w == nil {
		return changed
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	paths := changed[:0]
	for _, path := range changed {
		_, watched := prev[path]
		state, exists := next[path]
		baseline, loaded := w.baselines[path]
		// Loading can discover external inputs after the watcher starts. Their
		// already displayed revisions are not edits; later revisions still are.
		if !watched && exists && loaded && state == baseline {
			continue
		}
		paths = append(paths, path)
	}
	return paths
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

// isAcknowledged reports whether a disk state needs no reload: it is either
// the exact revision guget committed or the revision the displayed snapshot
// consumed. Any other content, including an external revert, still reloads.
func (w *workspaceWatchInputs) isAcknowledged(path string, state watchedFileState, exists bool) bool {
	if w == nil || !exists {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	// A metadata-only touch of the exact committed bytes needs no rescan.
	if prior, ok := w.acknowledged[path]; ok && prior.Hash == state.Hash && prior.Owner == state.Owner {
		return true
	}
	baseline, ok := w.baselines[path]
	return ok && baseline.Hash == state.Hash && baseline.Owner == state.Owner
}

// displayedRevisions returns a copy of the revisions the displayed snapshot
// consumed, so a local reconciliation can carry them into the next snapshot.
func (w *workspaceWatchInputs) displayedRevisions() map[string]watchedFileState {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	revisions := make(map[string]watchedFileState, len(w.baselines))
	for path, state := range w.baselines {
		revisions[path] = state
	}
	return revisions
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

// scanWatchedWorkspaceFiles walks rootDir and reads every watched file it
// finds plus extraPaths, such as imports outside the workspace.
func scanWatchedWorkspaceFiles(rootDir string, opts workspaceScanOptions, extraPaths ...string) (map[string]watchedFileState, error) {
	tree, err := walkWorkspace(rootDir, opts)
	if err != nil {
		return make(map[string]watchedFileState), err
	}
	return readWatchedFileStates(tree.WatchedFiles, extraPaths)
}

// readWatchedFileStates reads the given files without listing directories.
// Missing files are left out of the result.
func readWatchedFileStates(paths, extraPaths []string) (map[string]watchedFileState, error) {
	files := make(map[string]watchedFileState, len(paths)+len(extraPaths))
	for _, group := range [][]string{paths, extraPaths} {
		for _, path := range group {
			if _, seen := files[path]; seen {
				continue
			}
			state, exists, err := readWatchedFileState(path)
			if err != nil {
				return files, err
			}
			if exists {
				files[path] = state
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
func watchWorkspaceFiles(rootDir string, opts workspaceScanOptions, send func(ui.Event), registries ...*workspaceWatchInputs) func() {
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
		prev, err := scanWatchedWorkspaceFiles(rootDir, opts, inputs.inputPaths()...)
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
				next, err := scanWatchedWorkspaceFiles(rootDir, opts, inputs.inputPaths()...)
				if err != nil {
					if !os.IsNotExist(err) {
						logWarn("workspace watch scan failed: %v", err)
					}
					continue
				}
				changed := inputs.changes(prev, next)
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
