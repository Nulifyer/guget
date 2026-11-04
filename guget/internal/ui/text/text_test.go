package text

import (
	"strings"
	"testing"
)

func TestTruncationPreservesGraphemesAndClosesLinks(t *testing.T) {
	s := "\x1b[31mA你好é👩‍💻\x1b[0m"
	for width := 0; width < 12; width++ {
		got := Truncate(s, width, "...")
		if StringWidth(got) > width {
			t.Fatalf("width %d => %q", width, got)
		}
	}
	if got := Strip(Truncate(s, 4, "")); got != "A你" {
		t.Fatalf("split wide glyph=%q", got)
	}
	link := "\x1b]8;;https://example.test\x1b\\abcdef\x1b]8;;\x1b\\"
	got := Truncate(link, 3, "")
	if Strip(got) != "abc" || !strings.HasSuffix(got, "\x1b]8;;\x1b\\") {
		t.Fatalf("link clipping=%q", got)
	}
}
func TestWrappedStylesStayInBounds(t *testing.T) {
	for _, line := range Wrap("\x1b[32m你好abcdef👩‍💻\x1b[0m", 4) {
		if StringWidth(line) > 4 {
			t.Fatalf("overflow=%q", line)
		}
	}
}

func TestWrapPrefersWordBoundaries(t *testing.T) {
	lines := Wrap("\x1b[32mThis release improves logs.\x1b[0m", 18)
	if len(lines) != 2 || Strip(lines[0]) != "This release" || Strip(lines[1]) != "improves logs." {
		t.Fatalf("word wrapping=%q", lines)
	}
}

func TestReleaseNoteLineEndingsAndTabsBecomeLayoutText(t *testing.T) {
	// Public release bodies commonly contain Windows line endings and indented lists.
	source := "## What's Changed\r\n\r\n\t* Fix - Fixed TimeOnly deserialization\r\n"
	lines := Wrap(source, 30)
	if got := strings.Join(lines, "\n"); strings.ContainsAny(got, "\r\t") {
		t.Fatalf("terminal movement controls remain: %q", got)
	}
	if !strings.Contains(lines[2], "    * Fix") {
		t.Fatalf("indentation lost: %q", lines)
	}
	for _, line := range lines {
		if StringWidth(line) > 30 {
			t.Fatalf("wrapped content overflows: %q", line)
		}
	}
}
