package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	editplan "github.com/nulifyer/guget/internal/edit"
	"github.com/nulifyer/guget/internal/packageops"
	"github.com/nulifyer/guget/internal/projectedit"
)

// mutationTarget records an explicit add location when the TUI location picker
// supplies one. CLI additions use the project's central version owner by default.
type mutationTarget struct {
	project   *ParsedProject
	addTarget *AddTarget
}

type mutationOptions struct {
	ctx             context.Context
	skipLocked      bool
	removeInherited bool
}

func buildMutationPlan(verb, packageID, version string, projects []*ParsedProject) (editplan.Plan, []planRow, error) {
	return buildMutationPlanContext(context.Background(), verb, packageID, version, projects)
}

func buildMutationPlanContext(ctx context.Context, verb, packageID, version string, projects []*ParsedProject) (editplan.Plan, []planRow, error) {
	targets := make([]mutationTarget, 0, len(projects))
	for _, project := range projects {
		targets = append(targets, mutationTarget{project: project})
	}
	plan, rows, _, err := planPackageMutation(verb, packageID, version, targets, mutationOptions{ctx: ctx})
	return plan, rows, err
}

// planPackageMutation is the shared scope and ownership policy for both front
// ends. Every file edit is prepared before the resulting transaction can apply.
func planPackageMutation(verb, packageID, version string, targets []mutationTarget, options mutationOptions) (editplan.Plan, []planRow, int, error) {
	if verb != "remove" {
		if err := projectedit.ValidateVersion(version); err != nil {
			return editplan.Plan{}, nil, 0, err
		}
	}
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
		if project.RequiresEvaluation {
			ctx := options.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			evaluated, err := evaluatedMutationProject(ctx, project, packageID, verb)
			if err != nil {
				return editplan.Plan{}, nil, skipped, err
			}
			project = evaluated
		}
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
			if !supportedMutationOwner(location.FilePath) {
				return editplan.Plan{}, nil, skipped, fmt.Errorf("unsupported add target %s; choose a project or .props file", location.FilePath)
			}
			if location.Kind == AddTargetCPM {
				defined, err := fileDefinesPackage(location.FilePath, packageID)
				if err != nil {
					return editplan.Plan{}, nil, skipped, err
				}
				if defined {
					existing, err := projectedit.LiteralVersion(location.FilePath, packageID)
					if err != nil {
						return editplan.Plan{}, nil, skipped, err
					}
					if existing != version {
						return editplan.Plan{}, nil, skipped, fmt.Errorf("central version for %q is %s in %s; requested %s would change shared consumers; update the central version explicitly first", packageID, existing, location.FilePath, version)
					}
				} else {
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
			owner := project.SourceFileForPackage(packageID)
			if !supportedMutationOwner(owner) {
				return editplan.Plan{}, nil, skipped, fmt.Errorf("unsupported package owner %s; supported owners are project and .props files", owner)
			}
			appendOperation(projectedit.UpdateVersion, owner, "update version", version)
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
				if !supportedMutationOwner(owner) {
					return editplan.Plan{}, nil, skipped, fmt.Errorf("unsupported package owner %s; inherited removal is read-only", owner)
				}
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
		path, err := canonicalMutationPath(row.Path)
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

// evaluatedMutationProject resolves conditional imports for every framework.
// Source edits still require a unique unconditional literal declaration.
func evaluatedMutationProject(ctx context.Context, project *ParsedProject, packageID, verb string) (*ParsedProject, error) {
	snapshot, err := (packageops.MSBuildInspector{}).InspectProject(ctx, project.FilePath)
	if err != nil {
		return nil, fmt.Errorf("%s requires MSBuild evaluation to establish active ownership: %w", project.FileName, err)
	}
	if verb == "add" && snapshot.CentralPackageManagement != (projectCPMTarget(project) != "") {
		return nil, fmt.Errorf("%s has evaluated central-package configuration that cannot be assigned to a unique add target safely", project.FileName)
	}
	copy := *project
	copy.Packages = NewSet[PackageReference]()
	copy.PackageSources = make(map[string]string)
	var first *packageops.PackageUse
	for _, use := range snapshot.PackageUses {
		if !strings.EqualFold(use.PackageID, packageID) {
			continue
		}
		if !use.Edit.Supported || use.Implicit {
			return nil, fmt.Errorf("%q in %s is read-only: %s", packageID, project.FileName, use.Edit.Reason)
		}
		if first != nil && (first.ReferenceOwner != use.ReferenceOwner || first.VersionOwner != use.VersionOwner || first.EvaluatedExpression != use.EvaluatedExpression) {
			return nil, fmt.Errorf("%q in %s has different ownership or versions across target frameworks", packageID, project.FileName)
		}
		value := use
		first = &value
	}
	if first != nil {
		declaredOwner, declaredErr := canonicalMutationPath(project.SourceFileForPackage(packageID))
		evaluatedOwner, evaluatedErr := canonicalMutationPath(first.VersionOwner)
		if declaredErr != nil || evaluatedErr != nil || declaredOwner != evaluatedOwner {
			return nil, fmt.Errorf("evaluated ownership of %q in %s differs from declared discovery; this scope is read-only", packageID, project.FileName)
		}
		copy.Packages.Add(PackageReference{Name: first.PackageID, Version: ParseSemVer(first.EvaluatedExpression), Requested: first.EvaluatedExpression, Locked: isExactLock(first.EvaluatedExpression)})
		copy.PackageSources[strings.ToLower(packageID)] = first.VersionOwner
	}
	return &copy, nil
}

func supportedMutationOwner(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csproj", ".fsproj", ".vbproj", ".props":
		return true
	}
	return false
}

func canonicalMutationPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}
