package main

import "strings"

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
