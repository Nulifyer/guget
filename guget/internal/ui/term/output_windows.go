//go:build windows

package term

import "golang.org/x/sys/windows"

func enableOutput(fd int) (func(), error) {
	handle := windows.Handle(fd)
	var old uint32
	if err := windows.GetConsoleMode(handle, &old); err != nil {
		return nil, err
	}
	if err := windows.SetConsoleMode(handle, old|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return nil, err
	}
	return func() { _ = windows.SetConsoleMode(handle, old) }, nil
}
