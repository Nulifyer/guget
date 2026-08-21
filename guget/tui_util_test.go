package main

import (
	"testing"

	lipgloss "charm.land/lipgloss/v2"
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
			if width := lipgloss.Width(got); width > test.width {
				t.Fatalf("truncate(%q) width = %d, want <= %d; output %q", test.input, width, test.width, got)
			}
		})
	}
}

func TestTruncateStyledPreservesANSIAndWidth(t *testing.T) {
	styled := "\x1b[31m包裹-package\x1b[0m"
	got := truncateStyled(styled, 6)
	if width := lipgloss.Width(got); width > 6 {
		t.Fatalf("width = %d; output %q", width, got)
	}
}
