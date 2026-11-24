package main

import (
	"path/filepath"
	"slices"

	"github.com/nulifyer/guget/internal/nugetconfig"
)

// Include every loaded package/import owner, plus ancestor configuration paths
// that may be created after startup. This list is copied across the watcher seam.
func workspaceInputPaths(root string, snapshot *workspaceSnapshot) []string {
	paths := make(map[string]struct{})
	for path := range snapshot.InputRevisions {
		paths[path] = struct{}{}
	}
	for _, project := range append(append([]*ParsedProject(nil), snapshot.ParsedProjects...), snapshot.PropsProjects...) {
		for _, path := range projectInputPaths(project) {
			paths[path] = struct{}{}
		}
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		for _, name := range []string{"Directory.Packages.props", "Directory.Build.props", "Directory.Build.targets"} {
			paths[filepath.Join(dir, name)] = struct{}{}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	// Configuration files that would change the NuGet view if created. Those
	// below root are found by the workspace walk.
	for _, path := range nugetconfig.CandidatePaths(root, nugetconfig.Environment{}) {
		paths[path] = struct{}{}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	slices.Sort(result)
	return result
}

func projectInputPaths(project *ParsedProject) []string {
	paths := []string{project.FilePath}
	for path := range project.InputRevisions {
		paths = append(paths, path)
	}
	for _, path := range project.PackageSources {
		paths = append(paths, path)
	}
	for _, target := range project.AddTargets {
		paths = append(paths, target.FilePath)
	}
	return paths
}
