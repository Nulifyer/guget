package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// FindProjectFiles walks rootDir and returns all .csproj and .fsproj paths,
// skipping common build-output and metadata directories.
func FindProjectFiles(rootDir string) ([]string, error) {
	ignoreDirs := []string{
		"node_modules", "bower_components", "dist", "build", ".next",
		"bin", "obj", "packages", ".nuget",
		".git", ".hg", ".svn", ".gitlab", ".github",
		".vs", ".idea", ".vscode",
		".venv", "venv", "env",
		".gradle", "target",
		".cache", "tmp", "temp", "vendor", "coverage",
		"wwwroot", "public", "www",
		"out",
	}

	ignore := make(map[string]struct{}, len(ignoreDirs))
	for _, d := range ignoreDirs {
		ignore[strings.ToLower(d)] = struct{}{}
	}

	var projects []string
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if _, ok := ignore[strings.ToLower(d.Name())]; ok {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".csproj" || ext == ".fsproj" {
			projects = append(projects, path)
		}
		return nil
	})

	return projects, err
}
