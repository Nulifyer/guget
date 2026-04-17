package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

var ignoredProjectDirs = map[string]struct{}{
	"node_modules": {}, "bower_components": {}, "dist": {}, "build": {}, ".next": {},
	"bin": {}, "obj": {}, "packages": {}, ".nuget": {},
	".git": {}, ".hg": {}, ".svn": {}, ".gitlab": {}, ".github": {},
	".vs": {}, ".idea": {}, ".vscode": {},
	".venv": {}, "venv": {}, "env": {},
	".gradle": {}, "target": {},
	".cache": {}, "tmp": {}, "temp": {}, "vendor": {}, "coverage": {},
	"wwwroot": {}, "public": {}, "www": {},
	"out": {},
}

func shouldSkipProjectDir(name string) bool {
	_, ok := ignoredProjectDirs[strings.ToLower(name)]
	return ok
}

// FindProjectFiles walks rootDir and returns all .csproj, .fsproj, and .vbproj paths,
// skipping common build-output and metadata directories.
func FindProjectFiles(rootDir string) ([]string, error) {
	var projects []string
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if shouldSkipProjectDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".csproj" || ext == ".fsproj" || ext == ".vbproj" {
			projects = append(projects, path)
		}
		return nil
	})

	return projects, err
}
