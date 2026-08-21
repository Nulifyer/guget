package edit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPlanRefusesStaleFileBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.csproj")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	change, err := ReadChange(path, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewPlan(change)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); !errors.Is(err, ErrStale) {
		t.Fatalf("Apply error = %v, want ErrStale", err)
	}
	data, _ := os.ReadFile(path)
	if got := string(data); got != "external" {
		t.Fatalf("contents = %q, want external edit preserved", got)
	}
}

func TestPlanRollsBackEarlierFiles(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "Directory.Packages.props")
	second := filepath.Join(dir, "App.csproj")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	firstChange, _ := ReadChange(first, []byte("first-new"))
	secondChange, _ := ReadChange(second, []byte("second-new"))
	plan, err := NewPlan(firstChange, secondChange)
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	result, err := plan.apply(func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writes == 2 {
			return errors.New("injected failure")
		}
		return os.WriteFile(path, data, mode)
	})
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if len(result.RolledBack) != 1 || result.RolledBack[0] != first {
		t.Fatalf("RolledBack = %v, want [%s]", result.RolledBack, first)
	}
	data, _ := os.ReadFile(first)
	if got := string(data); got != "old" {
		t.Fatalf("first contents = %q, want rollback", got)
	}
}

func TestNewPlanRejectsDuplicateTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.csproj")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	change, _ := ReadChange(path, []byte("new"))
	if _, err := NewPlan(change, change); err == nil {
		t.Fatal("expected duplicate-target error")
	}
}
