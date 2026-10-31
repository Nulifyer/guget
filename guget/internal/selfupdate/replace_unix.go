//go:build !windows

package selfupdate

import "os"

func replaceExecutable(stage, target string) error { return os.Rename(stage, target) }
