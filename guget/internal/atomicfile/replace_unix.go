//go:build !windows

package atomicfile

import "os"

// For Flushed, the caller syncs the directory afterwards.
func replaceFile(source, destination string, _ Durability) error {
	return os.Rename(source, destination)
}
