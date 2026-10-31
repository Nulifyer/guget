package selfupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/nulifyer/guget/internal/atomicfile"
)

type noticeCache struct {
	Checked   time.Time `json:"checked"`
	Version   string    `json:"version"`
	Dismissed string    `json:"dismissed,omitempty"`
}

func CachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "guget", "update.json")
}

func readCache(path string) noticeCache {
	var c noticeCache
	// Oversized or corrupt cache files are treated as absent.
	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 4096 {
		return c
	}
	if json.NewDecoder(f).Decode(&c) != nil {
		return noticeCache{}
	}
	return c
}

func saveCache(path string, c noticeCache) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data, 0o600)
}

// Notice silently checks at most daily when the cache is writable. Failed checks
// are cached too. No package/project information is included in the request.
func (u *Updater) Notice(ctx context.Context, path string) string {
	if _, _, ok := parseVersion(u.current); !ok {
		return ""
	}
	c := readCache(path)
	age := time.Since(c.Checked)
	if c.Checked.IsZero() || age < 0 || age >= 24*time.Hour {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		r, err := u.Latest(ctx)
		c.Checked = time.Now()
		if err != nil {
			c.Version = ""
			_ = saveCache(path, c)
			return ""
		}
		c.Version = r.Version
		_ = saveCache(path, c)
	}
	if c.Version != c.Dismissed && newer(c.Version, u.current) {
		return c.Version
	}
	return ""
}

// Dismiss suppresses this release across TUI sessions. Later releases can appear.
func Dismiss(path, version string) error {
	c := readCache(path)
	c.Dismissed = version
	return saveCache(path, c)
}
