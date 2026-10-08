package projectedit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nulifyer/guget/internal/edit"
)

type Kind int

const (
	AddReference Kind = iota
	AddVersion
	UpdateVersion
	RemoveReference
)

type Operation struct {
	Kind    Kind
	Path    string
	Package string
	Version string
}

// BuildPlan prepares the whole operation before any writes. Identical edits to
// a shared props file are planned once; conflicting edits are refused.
func BuildPlan(operations ...Operation) (edit.Plan, error) {
	seen := make(map[string]Operation)
	var changes []edit.Change
	for _, op := range operations {
		path, err := filepath.Abs(op.Path)
		if err != nil {
			return edit.Plan{}, err
		}
		op.Path = path
		if previous, ok := seen[path]; ok {
			if previous.Kind == op.Kind && strings.EqualFold(previous.Package, op.Package) && previous.Version == op.Version {
				continue
			}
			return edit.Plan{}, fmt.Errorf("conflicting package edits to %s", path)
		}
		seen[path] = op
		var change edit.Change
		switch op.Kind {
		case AddReference:
			change, err = PlanAddPackageReference(path, op.Package, op.Version)
		case AddVersion:
			change, err = PlanAddPackageVersion(path, op.Package, op.Version)
		case UpdateVersion:
			change, err = PlanUpdatePackageVersion(path, op.Package, op.Version)
		case RemoveReference:
			change, err = PlanRemovePackageReference(path, op.Package)
		default:
			err = fmt.Errorf("unknown package edit kind %d", op.Kind)
		}
		if err != nil {
			return edit.Plan{}, err
		}
		changes = append(changes, change)
	}
	return edit.NewPlan(changes...)
}

func HasReference(path, packageID string) (bool, error) {
	return hasPackageElement(path, packageID, "PackageReference")
}

func DefinesPackage(path, packageID string) (bool, error) {
	return hasPackageElement(path, packageID, "")
}

func hasPackageElement(path, packageID, tag string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	elements, err := scanPackageElements(data)
	if err != nil {
		return false, err
	}
	for _, element := range elements {
		if strings.EqualFold(element.name, packageID) && (tag == "" || strings.EqualFold(element.tag, tag)) {
			return true, nil
		}
	}
	return false, nil
}
