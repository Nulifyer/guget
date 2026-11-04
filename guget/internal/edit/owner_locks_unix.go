//go:build !windows

package edit

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func ownerFileIdentity(path string) (string, error) {
	var info unix.Stat_t
	if err := unix.Stat(path, &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("file:%d:%d", info.Dev, info.Ino), nil
}

func tryLockOwner(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func unlockOwner(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
