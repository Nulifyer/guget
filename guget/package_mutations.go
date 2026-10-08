package main

import (
	"fmt"
	"path/filepath"
	"strings"

	editplan "github.com/nulifyer/guget/internal/edit"
	"github.com/nulifyer/guget/internal/projectedit"
)

// mutationTarget records an explicit add location when the TUI location picker
// supplies one. CLI additions use the project's central version owner by default.
type mutationTarget struct {
	project   *ParsedProject
	addTarget *AddTarget
}

type mutationOptions struct {
	skipLocked      bool
	removeInherited bool
}

func buildMutationPlan(verb, packageID, version string, projects []*ParsedProject) (editplan.Plan, []planRow, error) {
	targets := make([]mutationTarget, 0, len(projects))
	for _, project := range projects {
		targets = append(targets, mutationTarget{project: project})
	}
	plan, rows, _, err := planPackageMutation(verb, packageID, version, targets, mutationOptions{})
	return plan, rows, err
}

// planPackageMutation is the shared scope and ownership policy for both front
// ends. Every file edit is prepared before the resulting transaction can apply.
func planPackageMutation(verb, packageID, version string, targets []mutationTarget, options mutationOptions) (editplan.Plan, []planRow, int, error) {
	var operations []projectedit.Operation
	var rows []planRow
	skipped := 0
	packageSeen := false
	appendOperation := func(kind projectedit.Kind, path, operation, targetVersion string) {
		operations = append(operations, projectedit.Operation{Kind: kind, Path: path, Package: packageID, Version: targetVersion})
		rows = append(rows, planRow{Operation: operation, Package: packageID, Version: targetVersion, Path: path})
	}
	for _, target := range targets {
		project := target.project
		var installed *PackageReference
		for ref := range project.Packages {
			if strings.EqualFold(ref.Name, packageID) {
				copy := ref
				installed = &copy
				packageSeen = true
				break
			}
		}
		switch verb {
		case "add":
			if installed != nil {
				continue
			}
			location := AddTarget{Kind: AddTargetProject, FilePath: project.FilePath}
			if target.addTarget != nil {
				location = *target.addTarget
			} else if central := projectCPMTarget(project); central != "" {
				location = AddTarget{Kind: AddTargetCPM, FilePath: central}
			}
			if location.Kind == AddTargetCPM {
				defined, err := fileDefinesPackage(location.FilePath, packageID)
				if err != nil {
					return editplan.Plan{}, nil, skipped, err
				}
				if !defined {
					appendOperation(projectedit.AddVersion, location.FilePath, "add central version", version)
				}
				appendOperation(projectedit.AddReference, project.FilePath, "add reference", "")
			} else {
				appendOperation(projectedit.AddReference, location.FilePath, "add reference", version)
			}
		case "update":
			if installed == nil {
				continue
			}
			if options.skipLocked && installed.Locked {
				skipped++
				continue
			}
			appendOperation(projectedit.UpdateVersion, project.SourceFileForPackage(packageID), "update version", version)
		case "remove":
			if installed == nil {
				continue
			}
			local, err := fileHasPackageReference(project.FilePath, packageID)
			if err != nil {
				return editplan.Plan{}, nil, skipped, err
			}
			if !local && !options.removeInherited {
				return editplan.Plan{}, nil, skipped, fmt.Errorf("package %q is inherited by %s from %s; no project-owned reference can be removed safely", packageID, project.FileName, project.SourceFileForPackage(packageID))
			}
			if local {
				appendOperation(projectedit.RemoveReference, project.FilePath, "remove reference", "")
			}
			if options.removeInherited {
				owner := project.SourceFileForPackage(packageID)
				if owner != project.FilePath && strings.HasSuffix(strings.ToLower(owner), ".props") {
					appendOperation(projectedit.RemoveReference, owner, "remove reference", "")
				}
			}
		default:
			return editplan.Plan{}, nil, skipped, fmt.Errorf("unknown mutation %q", verb)
		}
	}
	if verb != "add" && !packageSeen {
		return editplan.Plan{}, nil, skipped, fmt.Errorf("package %q is not installed in the selected scope", packageID)
	}
	plan, err := projectedit.BuildPlan(operations...)
	if err != nil {
		return editplan.Plan{}, nil, skipped, err
	}
	changed := make(map[string]bool)
	for _, path := range plan.Paths() {
		changed[path] = true
	}
	actualRows := make([]planRow, 0, plan.Len())
	for _, row := range rows {
		path, err := filepath.Abs(row.Path)
		if err != nil {
			return editplan.Plan{}, nil, skipped, err
		}
		if changed[path] {
			row.Path = path
			actualRows = append(actualRows, row)
			delete(changed, path)
		}
	}
	return plan, actualRows, skipped, nil
}

func projectCPMTarget(project *ParsedProject) string {
	for _, target := range project.AddTargets {
		if target.Kind == AddTargetCPM {
			return target.FilePath
		}
	}
	return ""
}

func fileHasPackageReference(path, id string) (bool, error) {
	return projectedit.HasReference(path, id)
}
func fileDefinesPackage(path, id string) (bool, error) { return projectedit.DefinesPackage(path, id) }
