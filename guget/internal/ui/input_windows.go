//go:build windows

package ui

import (
	"io"

	"github.com/nulifyer/guget/internal/ui/term"
)

func openInput() (io.Reader, func(), error) {
	reader, err := term.OpenStdinReader()
	if err != nil {
		return nil, nil, err
	}
	return reader, func() { _ = reader.Close() }, nil
}
