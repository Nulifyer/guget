package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrAfterReplace marks an error reported after the destination already
// contains the requested data. Multi-file callers must roll that path back.
var ErrAfterReplace = errors.New("destination replaced before durability check failed")

// Durability says whether a write waits for its data to reach the disk.
// Either way the destination is replaced by renaming a complete
// same-directory temporary file, so a crashed or killed process leaves the
// old or the new contents, never a partial file.
type Durability int

const (
	// Flushed writes the temporary file to disk before the rename and makes
	// the rename durable, so a power loss or system crash after the call
	// keeps the new contents. Each write costs a disk flush.
	Flushed Durability = iota
	// Buffered returns once the operating system has the new contents, as
	// dotnet and MSBuild do when they save a project. A power loss or system
	// crash before the operating system writes the data back (up to about
	// 30 s) can lose the change or, on some filesystems, leave an empty or
	// zero-filled file in place of the old one.
	Buffered
)

// WriteFile replaces path only after data has been written and synced to a
// same-directory temporary file. Existing permissions are preserved.
func WriteFile(path string, data []byte, fallbackMode os.FileMode) error {
	return WriteFileChecked(path, data, fallbackMode, nil)
}

// WriteFileChecked calls check immediately before every replacement attempt,
// after preparing the temporary file. A failed check leaves the destination
// unchanged. The check and replacement are not an atomic compare-and-swap
// against writers that do not cooperate with the caller's locking protocol.
func WriteFileChecked(path string, data []byte, fallbackMode os.FileMode, check func() error) error {
	return WriteFileCheckedWith(path, data, fallbackMode, check, Flushed)
}

// WriteFileCheckedWith is WriteFileChecked with a choice of durability.
func WriteFileCheckedWith(path string, data []byte, fallbackMode os.FileMode, check func() error, durability Durability) error {
	replace := func(source, destination string) error { return replaceFile(source, destination, durability) }
	return writeFileWith(path, data, fallbackMode, check, replace, durability)
}

func writeFile(path string, data []byte, fallbackMode os.FileMode, check func() error, replace func(string, string) error) error {
	return writeFileWith(path, data, fallbackMode, check, replace, Flushed)
}

// Flushes are variables so tests can check that Buffered never flushes.
var (
	syncFile = (*os.File).Sync
	syncDir  = syncDirectory
)

func writeFileWith(path string, data []byte, fallbackMode os.FileMode, check func() error, replace func(string, string) error, durability Durability) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = removeIfPresent(tmpPath)
		}
	}()

	mode := fallbackMode.Perm()
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, statErr)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("set temporary permissions for %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if durability == Flushed {
		if err := syncFile(tmp); err != nil {
			return fmt.Errorf("sync temporary file for %s: %w", path, err)
		}
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	const maxAttempts = 5
	for attempt := 0; ; attempt++ {
		if check != nil {
			if err := check(); err != nil {
				return fmt.Errorf("check %s before replacement: %w", path, err)
			}
		}
		err = replace(tmpPath, path)
		if err == nil {
			break
		}
		if attempt == maxAttempts-1 {
			return fmt.Errorf("replace %s: %w", path, err)
		}
		time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
	}
	committed = true
	if durability != Flushed {
		return nil
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("%w: sync directory for %s: %w", ErrAfterReplace, path, err)
	}
	return nil
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
