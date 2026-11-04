//go:build windows

package term

import "time"

const resizePollWindows = 250 * time.Millisecond

// WatchResize polls the terminal size on a ticker. Windows has no
// SIGWINCH equivalent that is usable alongside stream-based stdin
// reads, so a short tick is the pragmatic cross-terminal approach
// (Windows Terminal, ConHost, SSH-to-Windows). The consumer still
// calls Terminal.RefreshSize() -- this tick just wakes the main
// loop so the check runs without needing a key press.
func WatchResize() (<-chan struct{}, func()) {
	out := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(resizePollWindows)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	return out, func() { close(done) }
}
