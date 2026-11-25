//go:build windows

package atomicfile

import "golang.org/x/sys/windows"

func replaceFile(source, destination string, durability Durability) error {
	sourcePtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePtr, destinationPtr, moveFlags(durability))
}

// moveFlags makes a Flushed rename durable before MoveFileEx returns.
func moveFlags(durability Durability) uint32 {
	flags := uint32(windows.MOVEFILE_REPLACE_EXISTING)
	if durability == Flushed {
		flags |= windows.MOVEFILE_WRITE_THROUGH
	}
	return flags
}
