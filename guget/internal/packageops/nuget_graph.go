package packageops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type NuGetGraphInspector struct {
	Runner ProcessRunner
}

type graphDocument struct {
	Version  int `json:"version"`
	Projects []struct {
		Path       string `json:"path"`
		Frameworks []struct {
			Framework        string `json:"framework"`
			TopLevelPackages []struct {
				ID               string `json:"id"`
				RequestedVersion string `json:"requestedVersion"`
				ResolvedVersion  string `json:"resolvedVersion"`
			} `json:"topLevelPackages"`
			TransitivePackages []struct {
				ID              string `json:"id"`
				ResolvedVersion string `json:"resolvedVersion"`
			} `json:"transitivePackages"`
		} `json:"frameworks"`
	} `json:"projects"`
}

// ResolveProject loads versioned dotnet package-list JSON without restoring.
// Missing or stale assets are returned as errors instead of being presented as
// a resolved graph.
func (i NuGetGraphInspector) ResolveProject(ctx context.Context, projectPath string, includeTransitive bool) ([]PackageUse, error) {
	if i.Runner == nil {
		i.Runner = ExecRunner{}
	}
	versionOutput, err := i.Runner.Run(ctx, "dotnet", "--version")
	if err != nil {
		return nil, fmt.Errorf("detect dotnet SDK: %w", err)
	}
	majorText, _, _ := strings.Cut(strings.TrimSpace(string(versionOutput)), ".")
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return nil, fmt.Errorf("parse dotnet SDK version %q", strings.TrimSpace(string(versionOutput)))
	}
	var args []string
	if major >= 10 {
		args = []string{"package", "list", "--project", projectPath}
	} else {
		args = []string{"list", projectPath, "package"}
	}
	args = append(args, "--format", "json", "--output-version", "1", "--no-restore")
	if includeTransitive {
		args = append(args, "--include-transitive")
	}
	data, err := i.Runner.Run(ctx, "dotnet", args...)
	if err != nil {
		return nil, fmt.Errorf("read restored package graph: %w", err)
	}
	var document graphDocument
	if err := decodeCommandJSON(data, &document); err != nil {
		return nil, fmt.Errorf("decode dotnet package-list JSON: %w", err)
	}
	if document.Version != 1 {
		return nil, fmt.Errorf("unsupported dotnet package-list output version %d", document.Version)
	}
	var uses []PackageUse
	for _, project := range document.Projects {
		for _, framework := range project.Frameworks {
			for _, pkg := range framework.TopLevelPackages {
				uses = append(uses, PackageUse{ProjectPath: project.Path, TargetFramework: framework.Framework, PackageID: pkg.ID, EvaluatedExpression: pkg.RequestedVersion, ResolvedVersion: pkg.ResolvedVersion, Direct: true})
			}
			if includeTransitive {
				for _, pkg := range framework.TransitivePackages {
					uses = append(uses, PackageUse{ProjectPath: project.Path, TargetFramework: framework.Framework, PackageID: pkg.ID, ResolvedVersion: pkg.ResolvedVersion})
				}
			}
		}
	}
	return uses, nil
}
