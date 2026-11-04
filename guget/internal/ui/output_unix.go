//go:build !windows

package ui

import (
	"os"
	"sync"
	"syscall"
)

type terminalOutput struct {
	file *os.File
	once sync.Once
}

func openTerminalOutput() (outputSink, error) {
	// Reopen instead of dup so O_NONBLOCK does not change the caller's stdout
	// open-file description. Close interrupts Go's pollable pending Write.
	file, err := os.OpenFile(os.Stdout.Name(), os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return &terminalOutput{file: file}, nil
}
func (t *terminalOutput) Write(data []byte) (int, error) { return t.file.Write(data) }
func (t *terminalOutput) Interrupt()                     { _ = t.Close() }
func (t *terminalOutput) Close() error {
	var err error
	t.once.Do(func() { err = t.file.Close() })
	return err
}
