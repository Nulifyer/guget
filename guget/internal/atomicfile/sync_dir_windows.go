//go:build windows

package atomicfile

// MoveFileEx is called with MOVEFILE_WRITE_THROUGH on Windows.
func syncDirectory(string) error { return nil }
