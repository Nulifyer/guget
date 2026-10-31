//go:build windows

package selfupdate

import "os"

// Windows permits renaming a running executable, but cannot overwrite it.
func replaceExecutable(stage, target string) error {
	return replaceWithBackup(stage, target, os.Rename)
}
