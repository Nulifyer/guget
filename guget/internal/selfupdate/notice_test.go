package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNoticeCacheDismissalAndNewRelease(t *testing.T) {
	f := newFixture(t, "1.2.0")
	path := filepath.Join(t.TempDir(), "cache", "update.json")
	ctx := context.Background()
	if got := f.updater.Notice(ctx, path); got != "1.3.0" {
		t.Fatalf("notice=%q", got)
	}
	if got := f.updater.Notice(ctx, path); got != "1.3.0" || f.requests.Load() != 1 {
		t.Fatalf("cached notice=%q requests=%d", got, f.requests.Load())
	}
	if err := Dismiss(path, "1.3.0"); err != nil {
		t.Fatal(err)
	}
	if got := f.updater.Notice(ctx, path); got != "" {
		t.Fatalf("dismissed notice=%q", got)
	}
	c := readCache(path)
	c.Checked = time.Now().Add(-25 * time.Hour)
	if err := saveCache(path, c); err != nil {
		t.Fatal(err)
	}
	f.release = `{"tag_name":"v1.4.0"}`
	if got := f.updater.Notice(ctx, path); got != "1.4.0" {
		t.Fatalf("new release notice=%q", got)
	}
	if f.requests.Load() != 2 {
		t.Fatalf("requests=%d", f.requests.Load())
	}
}

func TestNoticeFailuresAreQuietAndCached(t *testing.T) {
	for _, kind := range []string{"http", "corrupt cache", "up to date", "dev", "downgrade"} {
		t.Run(kind, func(t *testing.T) {
			current := "1.2.0"
			if kind == "up to date" {
				current = "1.3.0"
			}
			if kind == "dev" {
				current = "dev"
			}
			if kind == "downgrade" {
				current = "2.0.0"
			}
			f := newFixture(t, current)
			path := filepath.Join(t.TempDir(), "update.json")
			if kind == "http" {
				f.status = 403
			}
			if kind == "corrupt cache" {
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
				f.status = 500
			}
			for range 2 {
				if got := f.updater.Notice(context.Background(), path); got != "" {
					t.Fatalf("notice=%q", got)
				}
			}
			want := int32(1)
			if kind == "dev" {
				want = 0
			}
			if f.requests.Load() != want {
				t.Fatalf("requests=%d", f.requests.Load())
			}
		})
	}
}

func TestCachedVersionValidatedBeforeDisplay(t *testing.T) {
	f := newFixture(t, "1.2.0")
	for _, value := range []string{"evil\ntext", "v1.4.0-beta.1", "1.1.0"} {
		path := filepath.Join(t.TempDir(), "update.json")
		if err := saveCache(path, noticeCache{Checked: time.Now(), Version: value}); err != nil {
			t.Fatal(err)
		}
		if got := f.updater.Notice(context.Background(), path); got != "" {
			t.Fatalf("notice=%q", got)
		}
	}
	if f.requests.Load() != 0 {
		t.Fatal("fresh cache triggered network")
	}
}
