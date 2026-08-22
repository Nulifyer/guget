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

// WriteFile replaces path only after data has been written and synced to a
// same-directory temporary file. Existing permissions are preserved.
func WriteFile(path string, data []byte, fallbackMode os.FileMode) error {
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
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	const maxAttempts = 5
	for attempt := 0; ; attempt++ {
		err = replaceFile(tmpPath, path)
		if err == nil {
			break
		}
		if attempt == maxAttempts-1 {
			return fmt.Errorf("replace %s: %w", path, err)
		}
		time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
	}
	committed = true
	if err := syncDirectory(dir); err != nil {
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
