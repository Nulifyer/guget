package edit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Editing the resolved owner also preserves a symlink instead of replacing
	// the link itself. Both workspace roots and symlink aliases share one lock.
	return filepath.EvalSymlinks(abs)
}

func ownerKey(path string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}

// lockOwners locks stable path identities as well as current file IDs because
// atomic replacement changes the file ID. Lock files are retained: unlinking one can
// let two processes lock different inodes for the same owner. The OS releases
// held locks when a process exits, including abrupt termination.
func lockOwners(ctx context.Context, paths []string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return func() {}, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("locate owner locks: %w", err)
	}
	dir := filepath.Join(cache, "guget", "owner-locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create owner lock directory: %w", err)
	}
	keys := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		key := ownerKey(path)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
		// Also serialize aliases made with hard links while they still refer
		// to the same file. The path lock remains necessary across replacement.
		identity, err := ownerFileIdentity(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // Keep the path lock; preflight reports the removed revision.
		}
		if err != nil {
			return nil, fmt.Errorf("identify owner %s: %w", path, err)
		}
		if !seen[identity] {
			seen[identity] = true
			keys = append(keys, identity)
		}
	}
	sort.Strings(keys)
	var files []*os.File
	unlock := func() {
		for i := len(files) - 1; i >= 0; i-- {
			_ = unlockOwner(files[i])
			_ = files[i].Close()
		}
	}
	for _, key := range keys {
		name := fmt.Sprintf("%x.lock", sha256.Sum256([]byte(key)))
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			unlock()
			return nil, fmt.Errorf("open owner lock for %s: %w", key, err)
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				unlock()
				return nil, err
			}
			locked, err := tryLockOwner(file)
			if err != nil {
				_ = file.Close()
				unlock()
				return nil, fmt.Errorf("lock owner %s: %w", key, err)
			}
			if locked {
				files = append(files, file)
				break
			}
			timer := time.NewTimer(20 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	return unlock, nil
}
