package edit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"

	"github.com/nulifyer/guget/internal/atomicfile"
)

var ErrStale = errors.New("file changed after edit was planned")

type Change struct {
	Path   string
	Before []byte
	After  []byte
	Mode   os.FileMode
	hash   [sha256.Size]byte
}

func NewChange(path string, before, after []byte) (Change, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Change{}, fmt.Errorf("stat %s: %w", path, err)
	}
	return Change{
		Path: path, Before: append([]byte(nil), before...), After: append([]byte(nil), after...),
		Mode: info.Mode().Perm(), hash: sha256.Sum256(before),
	}, nil
}

func ReadChange(path string, after []byte) (Change, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return Change{}, fmt.Errorf("read %s: %w", path, err)
	}
	return NewChange(path, before, after)
}

type Plan struct {
	changes []Change
}

func NewPlan(changes ...Change) (Plan, error) {
	seen := make(map[string]struct{}, len(changes))
	var owners []os.FileInfo
	filtered := make([]Change, 0, len(changes))
	for _, change := range changes {
		path, err := canonicalPath(change.Path)
		if err != nil {
			return Plan{}, fmt.Errorf("resolve %s: %w", change.Path, err)
		}
		key := path
		if _, ok := seen[key]; ok {
			return Plan{}, fmt.Errorf("duplicate edit target %s", path)
		}
		seen[key] = struct{}{}
		info, err := os.Stat(path)
		if err != nil {
			return Plan{}, fmt.Errorf("stat edit target %s: %w", path, err)
		}
		for _, owner := range owners {
			if os.SameFile(info, owner) {
				return Plan{}, fmt.Errorf("duplicate edit target %s", path)
			}
		}
		owners = append(owners, info)
		change.Path = path
		if bytes.Equal(change.Before, change.After) {
			continue
		}
		change.Before = append([]byte(nil), change.Before...)
		change.After = append([]byte(nil), change.After...)
		filtered = append(filtered, change)
	}
	return Plan{changes: filtered}, nil
}

func (p Plan) Paths() []string {
	paths := make([]string, len(p.changes))
	for i, change := range p.changes {
		paths[i] = change.Path
	}
	return paths
}

func (p Plan) Len() int { return len(p.changes) }

type Result struct {
	// Changed includes every path replaced during apply, even if rolled back.
	Changed    []string
	RolledBack []string
	// Conflicting paths changed outside this transaction and were preserved.
	Conflicting []string
	// Unrecovered paths were replaced but could not be rolled back. Their
	// contents must be inspected before retrying a failed transaction.
	Unrecovered []string
}

type writeFunc func(string, []byte, os.FileMode) error
type checkedWriteFunc func(string, []byte, os.FileMode, func() error) error

func (p Plan) Apply() (Result, error) {
	return p.ApplyContext(context.Background())
}

// ApplyContext serializes overlapping Guget transactions, then checks the
// planned revisions before each replacement and every rollback attempt.
// Cancellation stops waiting for owner locks or beginning another write;
// recovery of writes already committed is still attempted after cancellation.
func (p Plan) ApplyContext(ctx context.Context) (Result, error) {
	return p.applyChecked(ctx, atomicfile.WriteFileChecked)
}

func (p Plan) apply(write writeFunc) (Result, error) {
	return p.applyChecked(context.Background(), func(path string, data []byte, mode os.FileMode, check func() error) error {
		if err := check(); err != nil {
			return err
		}
		return write(path, data, mode)
	})
}

func (p Plan) applyChecked(ctx context.Context, write checkedWriteFunc) (Result, error) {
	var result Result
	unlock, err := lockOwners(ctx, p.Paths())
	if err != nil {
		return result, err
	}
	defer unlock()
	for _, change := range p.changes {
		if err := checkRevision(change.Path, change.hash); err != nil {
			if errors.Is(err, ErrStale) {
				result.Conflicting = append(result.Conflicting, change.Path)
			}
			return result, fmt.Errorf("preflight %s: %w", change.Path, err)
		}
	}

	for _, change := range p.changes {
		check := func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return checkRevision(change.Path, change.hash)
		}
		if err := write(change.Path, change.After, change.Mode, check); err != nil {
			if errors.Is(err, ErrStale) {
				result.Conflicting = append(result.Conflicting, change.Path)
			}
			if errors.Is(err, atomicfile.ErrAfterReplace) {
				result.Changed = append(result.Changed, change.Path)
			}
			rollbackErr := p.rollback(write, result.Changed, &result)
			return result, errors.Join(fmt.Errorf("apply %s: %w", change.Path, err), rollbackErr)
		}
		result.Changed = append(result.Changed, change.Path)
	}
	return result, nil
}

func (p Plan) rollback(write checkedWriteFunc, changed []string, result *Result) error {
	byPath := make(map[string]Change, len(p.changes))
	for _, change := range p.changes {
		byPath[change.Path] = change
	}
	var errs []error
	for i := len(changed) - 1; i >= 0; i-- {
		path := changed[i]
		change := byPath[path]
		check := func() error { return checkRevision(path, sha256.Sum256(change.After)) }
		if err := write(path, change.Before, change.Mode, check); err != nil {
			if errors.Is(err, atomicfile.ErrAfterReplace) {
				result.RolledBack = append(result.RolledBack, path)
			} else {
				result.Unrecovered = append(result.Unrecovered, path)
			}
			if errors.Is(err, ErrStale) {
				result.Conflicting = append(result.Conflicting, path)
			}
			errs = append(errs, fmt.Errorf("rollback %s: %w", path, err))
			continue
		}
		result.RolledBack = append(result.RolledBack, path)
	}
	return errors.Join(errs...)
}

func checkRevision(path string, expected [sha256.Size]byte) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s was removed", ErrStale, path)
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is no longer a regular file", ErrStale, path)
	}
	resolved, err := canonicalPath(path)
	if err != nil || ownerKey(resolved) != ownerKey(path) {
		return fmt.Errorf("%w: owner path changed for %s", ErrStale, path)
	}
	current, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s was removed", ErrStale, path)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if sha256.Sum256(current) != expected {
		return fmt.Errorf("%w: %s", ErrStale, path)
	}
	return nil
}
