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
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(string(exitErr.Stderr)), err)
	}
	return nil, err
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

	snapshot := ProjectSnapshot{ProjectPath: abs, TargetFrameworks: append([]string(nil), frameworks...), Evaluated: true}
	for _, framework := range frameworks {
		output := base
		if len(frameworks) > 1 {
			output, err = i.evaluate(ctx, abs, framework)
			if err != nil {
				return ProjectSnapshot{ProjectPath: abs}, fmt.Errorf("evaluate %s for %s: %w", abs, framework, err)
			}
		}
		snapshot.PackageUses = append(snapshot.PackageUses, packageUses(abs, framework, output)...)
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
		"-getProperty:TargetFramework,TargetFrameworks,ManagePackageVersionsCentrally",
		"-getItem:PackageReference,PackageVersion",
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

func packageUses(projectPath, framework string, output msbuildOutput) []PackageUse {
	central := make(map[string]msbuildItem)
	for _, item := range output.Items["PackageVersion"] {
		central[strings.ToLower(item.Identity)] = item
	}
	uses := make([]PackageUse, 0, len(output.Items["PackageReference"]))
	for _, reference := range output.Items["PackageReference"] {
		version := reference.VersionOverride
		versionOwner := reference.DefiningProjectFullPath
		if version == "" {
			version = reference.Version
		}
		if version == "" {
			if item, ok := central[strings.ToLower(reference.Identity)]; ok {
				version = item.Version
				versionOwner = item.DefiningProjectFullPath
			}
		}
		support := editSupport(reference.DefiningProjectFullPath, versionOwner)
		uses = append(uses, PackageUse{
			ProjectPath: projectPath, TargetFramework: framework,
			PackageID: reference.Identity, EvaluatedExpression: version,
			ReferenceOwner: reference.DefiningProjectFullPath, VersionOwner: versionOwner,
			Direct: true, Implicit: strings.EqualFold(reference.IsImplicitlyDefined, "true"),
			Edit: support,
		})
	}
	return uses
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
