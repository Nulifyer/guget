package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// FindProjectFiles walks rootDir recursively and returns paths to all
// .csproj and .fsproj files, skipping common build-output and metadata
// directories.
func FindProjectFiles(rootDir string) ([]string, error) {
	ignoreDirs := []string{
		// Node / front-end
		"node_modules", "bower_components", "dist", "build", ".next",

		// .NET / typical build outputs
		"bin", "obj", "packages", ".nuget",

		// Version control / metadata
		".git", ".hg", ".svn", ".gitlab", ".github",

		// IDE / editor dirs
		".vs", ".idea", ".vscode",

		// Python / virtualenvs
		".venv", "venv", "env",

		// Java / other build caches
		".gradle", "target",

		// General caches / temp / vendor
		".cache", "tmp", "temp", "vendor", "coverage",

		// Static/web folders that commonly contain lots of assets
		"wwwroot", "public", "www",

		// Other common folders
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

		// If this is a directory and its name is in the ignore set, skip it entirely.
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
