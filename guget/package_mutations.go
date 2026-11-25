package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	editplan "github.com/nulifyer/guget/internal/edit"
	"github.com/nulifyer/guget/internal/msbuildstatic"
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
	skipLocked      bool // bulk updates leave exact [x.y.z] pins alone
	refuseLocked    bool // explicit updates of an exact pin are refused
	removeInherited bool
	runner          packageops.ProcessRunner // MSBuild runner; nil runs dotnet
	// selectVersion, when set, chooses each update target's version instead
	// of the plan's single version, from the project and its installed
	// reference. ok=false leaves the reference unchanged (nothing newer).
	selectVersion func(project *ParsedProject, installed PackageReference) (version string, ok bool)
}

// pinnedSkipOperation marks a plan row for an exact pin that a bulk update left
// unchanged. Such rows have no file operation.
const pinnedSkipOperation = "skip (pinned)"

// unchangedOperation marks a plan row for a reference that a latest-version
// update leaves alone because nothing newer qualifies. It has no file
// operation.
const unchangedOperation = "unchanged"

// evaluationError reports that MSBuild evaluation, required to establish
// ownership, was unavailable or failed. It is a workspace failure rather than
// an edit-policy refusal.
type evaluationError struct{ err error }

func (e evaluationError) Error() string { return e.err.Error() }
func (e evaluationError) Unwrap() error { return e.err }

// pinnedPackageError refuses an explicit update of an exact version pin.
type pinnedPackageError struct {
	Package, Version, Owner string
}

func (e pinnedPackageError) Error() string {
	return fmt.Sprintf("%q is pinned to %s in %s", e.Package, e.Version, e.Owner)
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
	if verb != "remove" && !(verb == "update" && options.selectVersion != nil) {
		if err := projectedit.ValidateVersion(version); err != nil {
			return editplan.Plan{}, nil, 0, err
		}
	}
	var operations []projectedit.Operation
	var rows []planRow
	var skippedRows []planRow
	skipped := 0
	packageSeen := false
	appendOperation := func(kind projectedit.Kind, path, operation, targetVersion string) {
		operations = append(operations, projectedit.Operation{Kind: kind, Path: path, Package: packageID, Version: targetVersion})
		rows = append(rows, planRow{Operation: operation, Package: packageID, Version: targetVersion, Path: path})
	}
	for _, target := range targets {
		project := target.project
		if verb == "update" {
			if err := declarationProblem(project, packageID); err != nil {
				return editplan.Plan{}, nil, skipped, err
			}
		}
		if project.RequiresEvaluation && !((verb == "update" || verb == "remove") && canEditLiteralDeclaration(project, packageID)) {
			ctx := options.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			evaluated, err := evaluatedMutationProject(ctx, project, packageID, verb, options.runner)
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
			if central := projectCPMTarget(project); central != "" && location.Kind != AddTargetCPM {
				return editplan.Plan{}, nil, skipped, fmt.Errorf("%s uses central package management; choose %s as the add location", project.FileName, central)
			}
			if !supportedMutationOwner(location.FilePath) {
				return editplan.Plan{}, nil, skipped, fmt.Errorf("unsupported add target %s; choose a project or .props file", location.FilePath)
			}
			if location.Kind == AddTargetCPM {
				addCentral, err := planCentralAdd(project, location.FilePath, packageID, version)
				if err != nil {
					return editplan.Plan{}, nil, skipped, err
				}
				if addCentral {
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
			owner := project.SourceFileForPackage(packageID)
			if installed.Locked && options.refuseLocked {
				return editplan.Plan{}, nil, skipped, pinnedPackageError{Package: packageID, Version: installed.Requested, Owner: owner}
			}
			if options.skipLocked && installed.Locked {
				skipped++
				skippedRows = append(skippedRows, planRow{Operation: pinnedSkipOperation, Package: packageID, Version: installed.Requested, Path: owner})
				continue
			}
			if !supportedMutationOwner(owner) {
				return editplan.Plan{}, nil, skipped, fmt.Errorf("unsupported package owner %s; supported owners are project and .props files", owner)
			}
			targetVersion := version
			if options.selectVersion != nil {
				selected, ok := options.selectVersion(project, *installed)
				if !ok {
					current := installed.Requested
					if current == "" {
						current = installed.Version.String()
					}
					skippedRows = append(skippedRows, planRow{Operation: unchangedOperation, Package: packageID, Version: current, Path: owner})
					continue
				}
				targetVersion = selected
			}
			if declaration := project.Declarations[strings.ToLower(packageID)]; declaration.VersionSource == msbuildstatic.VersionCentral || declaration.VersionSource == msbuildstatic.VersionGlobal {
				if err := centralFloatingAllowed(project, targetVersion); err != nil {
					return editplan.Plan{}, nil, skipped, err
				}
			}
			appendOperation(projectedit.UpdateVersion, owner, "update version", targetVersion)
		case "remove":
			if installed == nil {
				continue
			}
			declaration := project.Declarations[strings.ToLower(packageID)]
			if declaration.Global {
				// A GlobalPackageReference applies to every project importing its
				// file; removing it is always a shared-file edit.
				if !options.removeInherited {
					return editplan.Plan{}, nil, skipped, fmt.Errorf("package %q is a GlobalPackageReference in %s and applies to every project that imports that file; remove it from that file for all of them", packageID, declaration.ReferenceOwner)
				}
				if !supportedMutationOwner(declaration.ReferenceOwner) {
					return editplan.Plan{}, nil, skipped, fmt.Errorf("unsupported package owner %s; inherited removal is read-only", declaration.ReferenceOwner)
				}
				appendOperation(projectedit.RemoveGlobalReference, declaration.ReferenceOwner, "remove global reference", "")
				continue
			}
			local, err := projectedit.IncludesReference(project.FilePath, packageID)
			if err != nil {
				return editplan.Plan{}, nil, skipped, err
			}
			referenceOwner := project.ReferenceFileForPackage(packageID)
			inherited := !sameMutationFile(referenceOwner, project.FilePath)
			if inherited && !options.removeInherited {
				return editplan.Plan{}, nil, skipped, fmt.Errorf("package %q is inherited by %s from %s; no project-owned reference can be removed safely", packageID, project.FileName, referenceOwner)
			}
			if local {
				appendOperation(projectedit.RemoveReference, project.FilePath, "remove reference", "")
			}
			if inherited {
				if !supportedMutationOwner(referenceOwner) || !strings.HasSuffix(strings.ToLower(referenceOwner), ".props") {
					return editplan.Plan{}, nil, skipped, fmt.Errorf("unsupported package owner %s; inherited removal is read-only", referenceOwner)
				}
				appendOperation(projectedit.RemoveReference, referenceOwner, "remove reference", "")
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
	seenSkips := make(map[string]bool)
	for _, row := range skippedRows {
		path, err := canonicalMutationPath(row.Path)
		if err != nil {
			return editplan.Plan{}, nil, skipped, err
		}
		if !seenSkips[path] {
			row.Path = path
			actualRows = append(actualRows, row)
			seenSkips[path] = true
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

func fileDefinesPackage(path, id string) (bool, error) { return projectedit.DefinesPackage(path, id) }

func sameMutationFile(a, b string) bool {
	first, errA := canonicalMutationPath(a)
	second, errB := canonicalMutationPath(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return first == second
}

// planCentralAdd decides how a CPM add treats the central version. NuGet
// keeps the first PackageVersion for an ID (NU1506), so a second one in the
// central file would have no effect when the ID is already defined, for
// example in an imported parent file. dotnet add package refuses that case
// (NuGet MSBuildAPIUtility.cs:251-257). Guget adds only the reference when
// the existing central version is the requested one, and otherwise refuses
// rather than changing the version every consumer uses.
func planCentralAdd(project *ParsedProject, central, packageID, version string) (bool, error) {
	if entry, ok := project.CentralVersions[strings.ToLower(packageID)]; ok {
		owner := entry.Owner
		switch {
		case entry.Conditional || !entry.VersionKnown:
			return false, fmt.Errorf("central version for %q in %s depends on a condition or property; edit it explicitly", packageID, owner)
		case entry.Global:
			return false, fmt.Errorf("package %q is a GlobalPackageReference in %s; every project that imports it already references it", packageID, owner)
		case entry.Version != version:
			return false, fmt.Errorf("central version for %q is %s in %s, which every project using it shares; to change it for all of them, first run: guget update %s --version %s --all --project %s, then repeat the add",
				packageID, entry.Version, owner, packageID, version, quoteShellArg(filepath.Dir(owner)))
		}
		return false, nil
	}
	defined, err := fileDefinesPackage(central, packageID)
	if err != nil {
		return false, err
	}
	if defined {
		return false, fmt.Errorf("package %q is declared in %s without an unconditional PackageVersion Include; edit that file explicitly", packageID, central)
	}
	if err := centralFloatingAllowed(project, version); err != nil {
		return false, err
	}
	return true, nil
}

// centralFloatingAllowed applies NU1011: a floating PackageVersion needs
// CentralPackageFloatingVersionsEnabled=true (NuGet MSBuildAPIUtility.cs:239-247).
func centralFloatingAllowed(project *ParsedProject, version string) error {
	if strings.Contains(version, "*") && !project.CentralFloatingVersions {
		return fmt.Errorf("floating version %s is not allowed for a central PackageVersion unless CentralPackageFloatingVersionsEnabled is true (NU1011); choose an exact version or range", version)
	}
	return nil
}

// declarationProblem refuses version edits that would keep a restore error.
func declarationProblem(project *ParsedProject, packageID string) error {
	declaration, ok := project.Declarations[strings.ToLower(packageID)]
	if !ok || declaration.Problem == "" {
		return nil
	}
	code, _, _ := strings.Cut(declaration.Problem, ":")
	switch code {
	case "NU1008":
		return fmt.Errorf("%s sets Version on %q, but it uses central package management, so restore fails (NU1008); replace Version with VersionOverride in %s for a project-specific version, or remove it to use the central PackageVersion",
			project.FileName, packageID, declaration.VersionOwner)
	case "NU1010":
		return fmt.Errorf("%q has no central PackageVersion, so restore fails (NU1010); add a PackageVersion to %s first", packageID, projectCPMTarget(project))
	case "NU1013":
		return fmt.Errorf("%s sets VersionOverride on %q while CentralPackageVersionOverrideEnabled is false, so restore fails (NU1013); remove the VersionOverride or enable overrides", project.FileName, packageID)
	}
	return nil
}

// evaluatedMutationProject resolves conditional imports for every framework.
// Source edits still require a unique unconditional literal declaration.
func evaluatedMutationProject(ctx context.Context, project *ParsedProject, packageID, verb string, runner packageops.ProcessRunner) (*ParsedProject, error) {
	snapshot, err := (packageops.MSBuildInspector{Runner: runner}).InspectProject(ctx, project.FilePath)
	if err != nil {
		return nil, fmt.Errorf("%s requires MSBuild evaluation to establish active ownership: %w", project.FileName, evaluationError{err})
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
		if declaredErr != nil || evaluatedErr != nil || declaredOwner != evaluatedOwner || !sameMutationFile(project.ReferenceFileForPackage(packageID), first.ReferenceOwner) {
			return nil, fmt.Errorf("evaluated ownership of %q in %s differs from declared discovery; this scope is read-only", packageID, project.FileName)
		}
		copy.Packages.Add(PackageReference{Name: first.PackageID, Version: requestedVersion(first.EvaluatedExpression), Requested: first.EvaluatedExpression, Locked: isExactLock(first.EvaluatedExpression), PrivateAssets: first.PrivateAssets, Global: first.Global})
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

// quoteShellArg quotes a path for a suggested command line. Double quotes
// work in POSIX shells, PowerShell and cmd for ordinary paths with spaces.
func quoteShellArg(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\"'$`&|;<>()*?[]{}!#~") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
