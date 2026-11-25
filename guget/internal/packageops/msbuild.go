package packageops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nulifyer/guget/internal/msbuildstatic"
	"github.com/nulifyer/guget/internal/projectedit"
)

type ProcessRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	// Stdout is returned with the error: dotnet reports some failures there,
	// such as package-list problems in JSON.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return output, fmt.Errorf("%s: %w", strings.TrimSpace(string(exitErr.Stderr)), err)
	}
	return output, err
}

type MSBuildInspector struct {
	Runner ProcessRunner
}

type msbuildOutput struct {
	Properties map[string]string        `json:"Properties"`
	Items      map[string][]msbuildItem `json:"Items"`
}

type msbuildItem struct {
	Identity                string `json:"Identity"`
	Version                 string `json:"Version"`
	VersionOverride         string `json:"VersionOverride"`
	PrivateAssets           string `json:"PrivateAssets"`
	DefiningProjectFullPath string `json:"DefiningProjectFullPath"`
	IsImplicitlyDefined     string `json:"IsImplicitlyDefined"`
}

func (i MSBuildInspector) InspectProject(ctx context.Context, projectPath string) (ProjectSnapshot, error) {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return ProjectSnapshot{}, err
	}
	if i.Runner == nil {
		i.Runner = ExecRunner{}
	}
	base, err := i.evaluate(ctx, abs, "")
	if err != nil {
		return ProjectSnapshot{ProjectPath: abs}, fmt.Errorf("evaluate %s: %w", abs, err)
	}
	frameworks := splitFrameworks(base.Properties["TargetFrameworks"])
	if len(frameworks) == 0 && strings.TrimSpace(base.Properties["TargetFramework"]) != "" {
		frameworks = []string{strings.TrimSpace(base.Properties["TargetFramework"])}
	}
	if len(frameworks) == 0 {
		frameworks = []string{""}
	}

	// Static evaluation supplies the file that set each metadata value, which
	// MSBuild's DefiningProjectFullPath does not report for Update items or
	// GlobalPackageReference expansions. MSBuild remains the value authority.
	declared, declaredErr := msbuildstatic.Evaluate(abs, msbuildstatic.Options{})
	if declaredErr != nil {
		declared = nil
	}
	snapshot := ProjectSnapshot{CentralPackageManagement: strings.EqualFold(base.Properties["ManagePackageVersionsCentrally"], "true"), ProjectPath: abs, TargetFrameworks: append([]string(nil), frameworks...), Evaluated: true}
	for _, framework := range frameworks {
		output := base
		if len(frameworks) > 1 {
			output, err = i.evaluate(ctx, abs, framework)
			if err != nil {
				return ProjectSnapshot{ProjectPath: abs}, fmt.Errorf("evaluate %s for %s: %w", abs, framework, err)
			}
		}
		if strings.EqualFold(output.Properties["ManagePackageVersionsCentrally"], "true") != snapshot.CentralPackageManagement {
			return ProjectSnapshot{ProjectPath: abs}, fmt.Errorf("central package management differs across target frameworks")
		}
		snapshot.PackageUses = append(snapshot.PackageUses, packageUses(abs, framework, output, declared)...)
		for _, download := range output.Items["PackageDownload"] {
			snapshot.PackageDownloads = append(snapshot.PackageDownloads, PackageDownload{
				ProjectPath: abs, TargetFramework: framework, PackageID: download.Identity, Version: download.Version, Owner: download.DefiningProjectFullPath,
			})
		}
	}
	sort.Slice(snapshot.PackageUses, func(a, b int) bool {
		if snapshot.PackageUses[a].TargetFramework != snapshot.PackageUses[b].TargetFramework {
			return snapshot.PackageUses[a].TargetFramework < snapshot.PackageUses[b].TargetFramework
		}
		return strings.ToLower(snapshot.PackageUses[a].PackageID) < strings.ToLower(snapshot.PackageUses[b].PackageID)
	})
	return snapshot, nil
}

func (i MSBuildInspector) evaluate(ctx context.Context, projectPath, framework string) (msbuildOutput, error) {
	args := []string{"msbuild", projectPath,
		"-getProperty:TargetFramework,TargetFrameworks,ManagePackageVersionsCentrally,_CentralPackageVersionsEnabled",
		"-getItem:PackageReference,PackageVersion,GlobalPackageReference,PackageDownload",
	}
	if framework != "" {
		args = append(args, "-property:TargetFramework="+framework)
	}
	data, err := i.Runner.Run(ctx, "dotnet", args...)
	if err != nil {
		return msbuildOutput{}, err
	}
	var output msbuildOutput
	if err := decodeCommandJSON(data, &output); err != nil {
		return msbuildOutput{}, fmt.Errorf("decode MSBuild JSON: %w", err)
	}
	return output, nil
}

func splitFrameworks(raw string) []string {
	var result []string
	for _, framework := range strings.Split(raw, ";") {
		if framework = strings.TrimSpace(framework); framework != "" {
			result = append(result, framework)
		}
	}
	return result
}

// nugetTargets reports whether path is NuGet.targets, which defines the
// items it expands from GlobalPackageReference (NuGet.targets:138-162).
func nugetTargets(path string) bool {
	return strings.EqualFold(filepath.Base(path), "NuGet.targets")
}

// firstItems keeps the first item for each identity, as NuGet does for
// duplicate PackageReference (NU1504) and PackageVersion (NU1506) items
// (CheckForDuplicateNuGetItemsTask.cs:75). It also reports duplicated IDs.
func firstItems(items []msbuildItem) ([]msbuildItem, map[string]msbuildItem, map[string]bool) {
	var ordered []msbuildItem
	first := make(map[string]msbuildItem)
	duplicates := make(map[string]bool)
	for _, item := range items {
		key := strings.ToLower(item.Identity)
		if _, seen := first[key]; seen {
			duplicates[key] = true
			continue
		}
		first[key] = item
		ordered = append(ordered, item)
	}
	return ordered, first, duplicates
}

func packageUses(projectPath, framework string, output msbuildOutput, declared *msbuildstatic.Evaluation) []PackageUse {
	_, central, _ := firstItems(output.Items["PackageVersion"])
	_, globals, _ := firstItems(output.Items["GlobalPackageReference"])
	references, _, duplicates := firstItems(output.Items["PackageReference"])
	centralEnabled := strings.EqualFold(output.Properties["_CentralPackageVersionsEnabled"], "true")
	staticUses := make(map[string]msbuildstatic.Package)
	if declared != nil {
		for _, pkg := range declared.References {
			staticUses[strings.ToLower(pkg.ID)] = pkg
		}
	}
	uses := make([]PackageUse, 0, len(references))
	for _, reference := range references {
		key := strings.ToLower(reference.Identity)
		referenceOwner := reference.DefiningProjectFullPath
		global, isGlobal := globals[key]
		isGlobal = isGlobal && nugetTargets(referenceOwner)
		if isGlobal {
			referenceOwner = global.DefiningProjectFullPath
		}
		version := reference.VersionOverride
		versionOwner := referenceOwner
		if version == "" {
			version = reference.Version
		}
		if version == "" {
			if item, ok := central[key]; ok {
				version = item.Version
				versionOwner = item.DefiningProjectFullPath
				if global, ok := globals[key]; ok && nugetTargets(versionOwner) {
					versionOwner = global.DefiningProjectFullPath
				}
			}
		}
		implicit := strings.EqualFold(reference.IsImplicitlyDefined, "true")
		support := literalSupport(reference.Identity, referenceOwner, versionOwner, version, isGlobal)
		if !support.Supported {
			// MSBuild reports the file that included an item, not the file of a
			// later Update that set the version. Accept the statically evaluated
			// owners when they declare exactly the version MSBuild evaluated.
			if pkg, ok := staticUses[key]; ok && !pkg.Conditional && pkg.VersionKnown && pkg.Version == version && pkg.VersionOwner != "" {
				if candidate := literalSupport(reference.Identity, pkg.ReferenceOwner, pkg.VersionOwner, version, pkg.Global); candidate.Supported {
					support, referenceOwner, versionOwner = candidate, pkg.ReferenceOwner, pkg.VersionOwner
				}
			}
		}
		switch {
		case implicit:
			support = EditSupport{Reason: "implicit SDK reference is read-only"}
		case duplicates[key]:
			support = EditSupport{Reason: "NU1504: the project has more than one PackageReference for this package; restore uses the first"}
		case centralEnabled && reference.Version != "" && reference.VersionOverride == "":
			support = EditSupport{Reason: "NU1008: a centrally managed PackageReference cannot set Version; use VersionOverride or a PackageVersion"}
		}
		uses = append(uses, PackageUse{
			ProjectPath: projectPath, TargetFramework: framework,
			PackageID: reference.Identity, EvaluatedExpression: version,
			ReferenceOwner: referenceOwner, VersionOwner: versionOwner,
			Direct: true, Implicit: implicit, Global: isGlobal, PrivateAssets: reference.PrivateAssets,
			Edit: support,
		})
	}
	return uses
}

// literalSupport checks that the owners are editable files whose literal
// declarations hold the evaluated version.
func literalSupport(id, referenceOwner, versionOwner, version string, global bool) EditSupport {
	support := editSupport(referenceOwner, versionOwner)
	if support.Supported && !global {
		if _, err := projectedit.LiteralVersion(referenceOwner, id); err != nil {
			support = EditSupport{Reason: err.Error()}
		}
	}
	if support.Supported {
		if expression, err := projectedit.LiteralVersion(versionOwner, id); err != nil {
			support = EditSupport{Reason: err.Error()}
		} else if expression == "" {
			support = EditSupport{Reason: "version owner has no editable literal version"}
		} else if expression != version {
			support = EditSupport{Reason: "evaluated version differs from the literal source; metadata overrides require explicit owner editing"}
		}
	}
	return support
}

func editSupport(referenceOwner, versionOwner string) EditSupport {
	if referenceOwner == "" {
		return EditSupport{Reason: "MSBuild did not report a reference owner"}
	}
	if versionOwner == "" {
		return EditSupport{Reason: "MSBuild did not report a version owner"}
	}
	for _, owner := range []string{referenceOwner, versionOwner} {
		extension := strings.ToLower(filepath.Ext(owner))
		if extension != ".csproj" && extension != ".fsproj" && extension != ".vbproj" && extension != ".props" {
			return EditSupport{Reason: "owner is not a supported project or props file: " + owner}
		}
		if info, err := os.Stat(owner); err != nil || info.IsDir() {
			return EditSupport{Reason: "owner is unavailable: " + owner}
		}
	}
	return EditSupport{Supported: true}
}
