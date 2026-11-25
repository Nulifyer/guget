package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// unmodeledMSBuildImports names an MSBuild import that static evaluation
// cannot see because the SDK's common props and targets bring it in from
// outside the workspace, or returns "" when none is configured. Static
// ownership is then not trustworthy: such a file can Update or Remove a
// package the workspace declares. Two kinds are checked:
//
//   - a CustomBeforeMicrosoft* or CustomAfterMicrosoft* property set in the
//     environment, such as CustomAfterMicrosoftCommonTargets, which
//     Microsoft.Common.CurrentVersion.targets:43,7140 imports. Environment
//     variables are MSBuild properties, whose names ignore case;
//   - files in a user extension ImportBefore or ImportAfter directory, such as
//     $(MSBuildUserExtensionsPath)/Current/Microsoft.Common.targets/ImportAfter,
//     which the common files import by wildcard (Microsoft.Common.props:84,115,
//     Microsoft.Common.CurrentVersion.targets:27,7143, and the CSharp,
//     VisualBasic and NETFramework targets).
//
// It reads the environment and at most a few directories, so callers run it
// once per plan, off the TUI event loop.
func unmodeledMSBuildImports() string {
	home, _ := os.UserHomeDir()
	return findUnmodeledMSBuildImports(os.Environ(), runtime.GOOS, home)
}

func findUnmodeledMSBuildImports(environ []string, goos, home string) string {
	for _, entry := range environ {
		name, value, _ := strings.Cut(entry, "=")
		lower := strings.ToLower(name)
		if value != "" && (strings.HasPrefix(lower, "custombeforemicrosoft") || strings.HasPrefix(lower, "customaftermicrosoft")) {
			return "the environment sets " + name
		}
	}
	for _, base := range msbuildUserExtensionsPaths(environ, goos, home) {
		if dir := userImportDirectory(base); dir != "" {
			return dir + " has files MSBuild imports"
		}
	}
	return ""
}

// msbuildUserExtensionsPaths returns the possible MSBuildUserExtensionsPath
// values: $(LocalAppData)/Microsoft/MSBuild, where LocalAppData is .NET's
// Environment.SpecialFolder.LocalApplicationData. On Linux that is
// $XDG_DATA_HOME when it is absolute, otherwise ~/.local/share. On macOS,
// .NET 8 and later use ~/Library/Application Support and earlier versions
// ~/.local/share, so both are returned. An MSBuildUserExtensionsPath
// environment variable replaces the default.
func msbuildUserExtensionsPaths(environ []string, goos, home string) []string {
	var paths []string
	if path := environValue(environ, "MSBuildUserExtensionsPath", true); path != "" {
		paths = append(paths, path)
	}
	switch goos {
	case "windows":
		local := environValue(environ, "LOCALAPPDATA", true)
		if local == "" && home != "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		if local != "" {
			paths = append(paths, filepath.Join(local, "Microsoft", "MSBuild"))
		}
	default:
		data := environValue(environ, "XDG_DATA_HOME", false)
		if !strings.HasPrefix(data, "/") {
			data = ""
			if home != "" {
				data = filepath.Join(home, ".local", "share")
			}
		}
		if data != "" {
			paths = append(paths, filepath.Join(data, "Microsoft", "MSBuild"))
		}
		if goos == "darwin" && home != "" {
			paths = append(paths, filepath.Join(home, "Library", "Application Support", "Microsoft", "MSBuild"))
		}
	}
	return paths
}

// userImportDirectory returns the first ImportBefore or ImportAfter directory
// below base/Current, or below base/Current/Imports, that holds a file.
func userImportDirectory(base string) string {
	current := filepath.Join(base, "Current")
	entries, err := os.ReadDir(current)
	if err != nil {
		return ""
	}
	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(current, entry.Name())
		dirs = append(dirs, dir)
		if strings.EqualFold(entry.Name(), "Imports") {
			nested, _ := os.ReadDir(dir)
			for _, imported := range nested {
				if imported.IsDir() {
					dirs = append(dirs, filepath.Join(dir, imported.Name()))
				}
			}
		}
	}
	for _, dir := range dirs {
		for _, point := range []string{"ImportBefore", "ImportAfter"} {
			candidate := filepath.Join(dir, point)
			files, _ := os.ReadDir(candidate)
			for _, file := range files {
				if !file.IsDir() {
					return candidate
				}
			}
		}
	}
	return ""
}

// environValue returns the last value of name in environ, ignoring case when
// fold is set, as Windows and MSBuild property names do.
func environValue(environ []string, name string, fold bool) string {
	value := ""
	for _, entry := range environ {
		key, text, ok := strings.Cut(entry, "=")
		if ok && (key == name || fold && strings.EqualFold(key, name)) {
			value = text
		}
	}
	return value
}
