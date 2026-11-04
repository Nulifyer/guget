package edit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nulifyer/guget/internal/atomicfile"
)

func planPair(t *testing.T) (Plan, string, string) {
	t.Helper()
	first, second := filepath.Join(t.TempDir(), "A shared packages.props"), filepath.Join(t.TempDir(), "B-世界.csproj")
	var changes []Change
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		change, err := ReadChange(path, []byte("guget"))
		if err != nil {
			t.Fatal(err)
		}
		changes = append(changes, change)
	}
	plan, err := NewPlan(changes...)
	if err != nil {
		t.Fatal(err)
	}
	return plan, plan.Paths()[0], plan.Paths()[1]
}

func TestApplyPreservesExternalEditAfterPreflight(t *testing.T) {
	plan, first, second := planPair(t)
	result, err := plan.apply(func(path string, data []byte, mode os.FileMode) error {
		if path == first && string(data) == "guget" {
			if err := os.WriteFile(second, []byte("external"), 0o600); err != nil {
				return err
			}
		}
		return os.WriteFile(path, data, mode)
	})
	if !errors.Is(err, ErrStale) || len(result.Conflicting) != 1 || result.Conflicting[0] != second {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	if len(result.RolledBack) != 1 || result.RolledBack[0] != first || len(result.Unrecovered) != 0 {
		t.Fatalf("recovery = %+v", result)
	}
	for path, want := range map[string]string{first: "old", second: "external"} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != want {
			t.Fatalf("%s contents = %q, error = %v", path, data, readErr)
		}
	}
}

func TestRollbackPreservesExternalEdit(t *testing.T) {
	plan, first, second := planPair(t)
	failure := errors.New("injected B failure")
	result, err := plan.apply(func(path string, data []byte, mode os.FileMode) error {
		if path == second {
			if err := os.WriteFile(first, []byte("external"), 0o600); err != nil {
				return err
			}
			return failure
		}
		return os.WriteFile(path, data, mode)
	})
	if !errors.Is(err, failure) || !errors.Is(err, ErrStale) {
		t.Fatalf("error = %v, want apply failure and rollback conflict", err)
	}
	if len(result.RolledBack) != 0 || len(result.Unrecovered) != 1 || result.Unrecovered[0] != first || len(result.Conflicting) != 1 || result.Conflicting[0] != first {
		t.Fatalf("result = %+v", result)
	}
	data, _ := os.ReadFile(first)
	if string(data) != "external" {
		t.Fatalf("external revision overwritten: %q", data)
	}
}

func TestRollbackRechecksOnRetry(t *testing.T) {
	plan, first, second := planPair(t)
	failure := errors.New("injected B failure")
	result, err := plan.applyChecked(context.Background(), func(path string, data []byte, mode os.FileMode, check func() error) error {
		if err := check(); err != nil {
			return err
		}
		if path == second {
			return failure
		}
		if string(data) == "old" {
			// Rollback's first check succeeds. An editor saves before the next
			// attempt; the same revision callback must reject that retry.
			if err := os.WriteFile(path, []byte("external"), mode); err != nil {
				return err
			}
			return check()
		}
		return os.WriteFile(path, data, mode)
	})
	if !errors.Is(err, failure) || !errors.Is(err, ErrStale) || len(result.Unrecovered) != 1 || result.Unrecovered[0] != first {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	data, _ := os.ReadFile(first)
	if string(data) != "external" {
		t.Fatalf("rollback retry overwrote external revision: %q", data)
	}
}

func TestCancellationRollsBackCommittedFiles(t *testing.T) {
	plan, first, second := planPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := plan.applyChecked(ctx, func(path string, data []byte, mode os.FileMode, check func() error) error {
		if err := check(); err != nil {
			return err
		}
		if path == first && string(data) == "guget" {
			cancel()
		}
		return os.WriteFile(path, data, mode)
	})
	if !errors.Is(err, context.Canceled) || len(result.RolledBack) != 1 || len(result.Unrecovered) != 0 {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	for _, path := range []string{first, second} {
		data, _ := os.ReadFile(path)
		if string(data) != "old" {
			t.Fatalf("contents after cancellation = %q", data)
		}
	}
}

func TestPlanUsesResolvedSymlinkOwner(t *testing.T) {
	root := t.TempDir()
	owner, alias := filepath.Join(root, "owner.props"), filepath.Join(root, "alias.props")
	if err := os.WriteFile(owner, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(owner, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	change, err := ReadChange(alias, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewPlan(change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(alias)
	data, _ := os.ReadFile(owner)
	if info.Mode()&os.ModeSymlink == 0 || string(data) != "new" {
		t.Fatalf("alias mode = %v, owner contents = %q", info.Mode(), data)
	}
	second, _ := ReadChange(owner, []byte("other"))
	if _, err := NewPlan(change, second); err == nil {
		t.Fatal("accepted duplicate symlink owner")
	}
}

func TestNewPlanRejectsDuplicateHardLinkOwners(t *testing.T) {
	root := t.TempDir()
	owner, alias := filepath.Join(root, "owner.props"), filepath.Join(root, "alias.props")
	if err := os.WriteFile(owner, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(owner, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	first, _ := ReadChange(owner, []byte("new"))
	second, _ := ReadChange(alias, []byte("other"))
	if _, err := NewPlan(first, second); err == nil {
		t.Fatal("accepted duplicate hard-link owner")
	}
}

func TestPlanReportsRemovedOwnerAsConflict(t *testing.T) {
	plan, first, _ := planPair(t)
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	result, err := plan.Apply()
	if !errors.Is(err, ErrStale) || len(result.Conflicting) != 1 || result.Conflicting[0] != first || len(result.Changed) != 0 {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

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

func TestPlanRollsBackWriteThatFailedAfterReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "App.csproj")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	change, _ := ReadChange(path, []byte("new"))
	plan, err := NewPlan(change)
	if err != nil {
		t.Fatal(err)
	}
	path = plan.Paths()[0]
	writes := 0
	result, err := plan.apply(func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writeErr := os.WriteFile(path, data, mode); writeErr != nil {
			return writeErr
		}
		if writes == 1 {
			return atomicfile.ErrAfterReplace
		}
		return nil
	})
	if !errors.Is(err, atomicfile.ErrAfterReplace) {
		t.Fatalf("error = %v, want ErrAfterReplace", err)
	}
	if len(result.Changed) != 1 || result.Changed[0] != path {
		t.Fatalf("Changed = %v, want [%s]", result.Changed, path)
	}
	if len(result.RolledBack) != 1 || result.RolledBack[0] != path {
		t.Fatalf("RolledBack = %v", result.RolledBack)
	}
	data, _ := os.ReadFile(path)
	if got := string(data); got != "old" {
		t.Fatalf("contents = %q, want old", got)
	}
}

func TestPlanReportsReplacedFileWhenRollbackFails(t *testing.T) {
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
	path = plan.Paths()[0]
	rollbackErr := errors.New("injected rollback failure")
	writes := 0
	result, err := plan.apply(func(path string, data []byte, mode os.FileMode) error {
		writes++
		if writes > 1 {
			return rollbackErr
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			return err
		}
		return atomicfile.ErrAfterReplace
	})
	if !errors.Is(err, atomicfile.ErrAfterReplace) || !errors.Is(err, rollbackErr) {
		t.Fatalf("error = %v, want replacement and rollback errors", err)
	}
	if len(result.Changed) != 1 || result.Changed[0] != path || len(result.RolledBack) != 0 {
		t.Fatalf("result = %+v", result)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("contents = %q, err = %v", data, err)
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
	first, second = plan.Paths()[0], plan.Paths()[1]
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
