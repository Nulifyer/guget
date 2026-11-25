package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// isolateGugetSettings points GUGET_CONFIG at a file under dir that does not
// exist and unsets every guget setting variable and legacy alias, so tests
// never read the developer's own settings. Both TestMain variants call it.
func isolateGugetSettings(dir string) error {
	if err := os.Setenv("GUGET_CONFIG", filepath.Join(dir, "guget", "config.toml")); err != nil {
		return err
	}
	for _, key := range settingKeys() {
		if err := os.Unsetenv(key.EnvName()); err != nil {
			return err
		}
		for _, alias := range key.Aliases {
			if err := os.Unsetenv(alias.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// quietDotnetFirstRun stops dotnet's first-run setup from changing the
// machine. Tests give dotnet a new DOTNET_CLI_HOME, and on Windows each new
// one would otherwise add its .dotnet\tools folder to the user PATH in the
// registry. Both TestMain variants call it.
func quietDotnetFirstRun() error {
	settings := map[string]string{
		"DOTNET_GENERATE_ASPNET_CERTIFICATE": "false",
		"DOTNET_NOLOGO":                      "1",
		"DOTNET_CLI_TELEMETRY_OPTOUT":        "1",
	}
	if runtime.GOOS == "windows" {
		settings["DOTNET_ADD_GLOBAL_TOOLS_TO_PATH"] = "false"
	}
	for name, value := range settings {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}
	return nil
}
