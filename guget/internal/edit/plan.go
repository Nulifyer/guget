package edit

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
		Path: path, Before: before, After: append([]byte(nil), after...),
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
	filtered := make([]Change, 0, len(changes))
	for _, change := range changes {
		path, err := filepath.Abs(change.Path)
		if err != nil {
			return Plan{}, fmt.Errorf("resolve %s: %w", change.Path, err)
		}
		if _, ok := seen[path]; ok {
			return Plan{}, fmt.Errorf("duplicate edit target %s", path)
		}
		seen[path] = struct{}{}
		change.Path = path
		if bytes.Equal(change.Before, change.After) {
			continue
		}
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
	Changed    []string
	RolledBack []string
}

type writeFunc func(string, []byte, os.FileMode) error

func (p Plan) Apply() (Result, error) {
	return p.apply(atomicfile.WriteFile)
}

func (p Plan) apply(write writeFunc) (Result, error) {
	var result Result
	for _, change := range p.changes {
		current, err := os.ReadFile(change.Path)
		if err != nil {
			return result, fmt.Errorf("preflight %s: %w", change.Path, err)
		}
		if sha256.Sum256(current) != change.hash {
			return result, fmt.Errorf("%w: %s", ErrStale, change.Path)
		}
	}

	for _, change := range p.changes {
		if err := write(change.Path, change.After, change.Mode); err != nil {
			changed := result.Changed
			if errors.Is(err, atomicfile.ErrAfterReplace) {
				changed = append(append([]string(nil), changed...), change.Path)
			}
			rollbackErr := p.rollback(write, changed, &result)
			return result, errors.Join(fmt.Errorf("apply %s: %w", change.Path, err), rollbackErr)
		}
		result.Changed = append(result.Changed, change.Path)
	}
	return result, nil
}

func (p Plan) rollback(write writeFunc, changed []string, result *Result) error {
	byPath := make(map[string]Change, len(p.changes))
	for _, change := range p.changes {
		byPath[change.Path] = change
	}
	var errs []error
	for i := len(changed) - 1; i >= 0; i-- {
		path := changed[i]
		change := byPath[path]
		if err := write(path, change.Before, change.Mode); err != nil {
			if errors.Is(err, atomicfile.ErrAfterReplace) {
				result.RolledBack = append(result.RolledBack, path)
			}
			errs = append(errs, fmt.Errorf("rollback %s: %w", path, err))
			continue
		}
		result.RolledBack = append(result.RolledBack, path)
	}
	return errors.Join(errs...)
}
