//go:build windows

package edit

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func ownerFileIdentity(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("file:%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

func tryLockOwner(file *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return err == nil, err
}

func unlockOwner(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
