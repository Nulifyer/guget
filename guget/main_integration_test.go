//go:build integration

package main

import (
	"fmt"
	"os"
	"testing"
)

// TestMain for integration builds isolates only guget's own settings. It
// leaves HOME, DOTNET_CLI_HOME, and the NuGet variables alone because these
// tests restore real packages with the host's dotnet configuration. Unit
// builds use the TestMain in main_test.go instead.
func TestMain(m *testing.M) {
	os.Exit(runWithIsolatedSettings(m))
}

func runWithIsolatedSettings(m *testing.M) int {
	dir, err := os.MkdirTemp("", "guget-settings-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	if err := isolateGugetSettings(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := isolateDotnet(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}
