package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsReplacementRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "guget.exe")
	stage := filepath.Join(dir, "stage.exe")
	if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected replacement failure")
	calls := 0
	err := replaceWithBackup(stage, target, func(a, b string) error {
		calls++
		if calls == 2 {
			return failure
		}
		return os.Rename(a, b)
	})
	if !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "original" {
		t.Fatalf("rollback data=%q", data)
	}
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Fatalf("backup remains: %v", err)
	}
}

func TestReplacementReportsBackupWhenRollbackFails(t *testing.T) {
	dir := t.TempDir()
	target, stage := filepath.Join(dir, "guget.exe"), filepath.Join(dir, "stage.exe")
	if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := replaceWithBackup(stage, target, func(a, b string) error {
		calls++
		if calls > 1 {
			return errors.New("locked")
		}
		return os.Rename(a, b)
	})
	if err == nil || !strings.Contains(err.Error(), target+".old") {
		t.Fatalf("missing recovery path: %v", err)
	}
	data, _ := os.ReadFile(target + ".old")
	if string(data) != "original" {
		t.Fatalf("backup lost: %q", data)
	}
}
