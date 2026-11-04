//go:build windows

package ui

import (
	"io"

	"github.com/nulifyer/guget/internal/ui/term"
)

func openInput() (io.Reader, func(), error) { return term.StdinReader(), func() {}, nil }
