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

// isolateDotnet makes each dotnet process a test starts independent of the
// machine and of earlier processes. Both TestMain variants call it.
//
//   - Tests give dotnet a new DOTNET_CLI_HOME, and on Windows each new one
//     would otherwise make first-run setup add its .dotnet\tools folder to
//     the user PATH in the registry.
//   - A reused MSBuild node or MSBuild server keeps the environment and
//     NuGet's cached folders of the process that started it, so a restore
//     would read an earlier test's credentials and configuration folders.
func isolateDotnet() error {
	settings := map[string]string{
		"DOTNET_GENERATE_ASPNET_CERTIFICATE": "false",
		"DOTNET_NOLOGO":                      "1",
		"DOTNET_CLI_TELEMETRY_OPTOUT":        "1",
		"MSBUILDDISABLENODEREUSE":            "1",
		"DOTNET_CLI_USE_MSBUILD_SERVER":      "false",
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
