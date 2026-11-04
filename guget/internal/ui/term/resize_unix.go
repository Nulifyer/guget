//go:build !windows

package term

import (
	"os"
	"os/signal"
	"syscall"
)

// WatchResize installs a SIGWINCH handler and returns a channel that
// fires every time the terminal is resized. The stop closure removes
// the signal handler and closes the channel. Unix kernels deliver
// SIGWINCH the moment the tty resizes, so no polling is needed.
func WatchResize() (<-chan struct{}, func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigCh:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	return out, func() {
		signal.Stop(sigCh)
		close(done)
	}
}
