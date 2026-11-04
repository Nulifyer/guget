//go:build !windows

package term

func enableOutput(int) (func(), error) { return func() {}, nil }
