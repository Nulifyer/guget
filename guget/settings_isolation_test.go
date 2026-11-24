package main

import (
	"os"
	"path/filepath"
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
