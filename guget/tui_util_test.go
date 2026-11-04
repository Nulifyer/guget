package main

import (
	"testing"

	style "github.com/nulifyer/guget/internal/ui/style"
)

func TestTruncateUsesTerminalCellWidth(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		width int
	}{
		{name: "wide characters", input: "包管理器-example", width: 8},
		{name: "emoji grapheme", input: "📦packages", width: 7},
		{name: "combining mark", input: "e\u0301-package", width: 6},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := truncate(test.input, test.width)
			if width := style.Width(got); width > test.width {
				t.Fatalf("truncate(%q) width = %d, want <= %d; output %q", test.input, width, test.width, got)
			}
		})
	}
}

func TestTruncateStyledPreservesANSIAndWidth(t *testing.T) {
	styled := "\x1b[31m包裹-package\x1b[0m"
	got := truncateStyled(styled, 6)
	if width := style.Width(got); width > 6 {
		t.Fatalf("width = %d; output %q", width, got)
	}
}

func TestSingleRowViewportKeepsLastItemVisible(t *testing.T) {
	scroll := 0
	clampListScroll(4, &scroll, 1, 5, 1)
	if scroll != 4 {
		t.Fatalf("last selected item scrolled out of one-row viewport: offset=%d", scroll)
	}
}
