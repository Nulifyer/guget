//go:build !integration

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates unit tests from the host's NuGet user and machine
// configuration. User-level credentials, disabled sources, and mappings apply
// even beneath a repository <clear/>, so host settings would leak into tests.
// The isolated user config matches the one dotnet creates on first use.
// Integration builds keep the real environment because they restore packages.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

func runIsolated(m *testing.M) int {
	dir, err := os.MkdirTemp("", "guget-nuget-env-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	home := filepath.Join(dir, "home")
	userConfig := filepath.Join(home, ".nuget", "NuGet", "NuGet.Config")
	if err := os.MkdirAll(filepath.Dir(userConfig), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defaultConfig := `<?xml version="1.0" encoding="utf-8"?><configuration><packageSources><add key="nuget.org" value="https://api.nuget.org/v3/index.json" protocolVersion="3" /></packageSources></configuration>`
	if err := os.WriteFile(userConfig, []byte(defaultConfig), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for name, value := range map[string]string{
		"DOTNET_CLI_HOME":               home,
		"APPDATA":                       filepath.Join(home, ".nuget"),
		"NUGET_COMMON_APPLICATION_DATA": filepath.Join(dir, "machine"),
		"ProgramFiles(x86)":             filepath.Join(dir, "machine"),
	} {
		if err := os.Setenv(name, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return m.Run()
}
