package text

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

// The ASCII fast path must produce exactly the graphemes and widths full
// segmentation does, including around combining marks, joiners, prepended
// marks, controls, and CRLF.
func TestTokensMatchFullSegmentation(t *testing.T) {
	pieces := []string{"a", "Z", " ", "~", "#", "1", "\t", "\n", "\r", "\r\n", "\x7f", "\x00", "é", "e\u0301", "\u0301", "\u200d", "\u0600", "\ufe0f", "\u20e3", "你", "👩\u200d💻", "🇨🇦", "🇨", "–", "▲", "\u00a0", "\xff", "한", "\u1100", "\u1161"}
	random := rand.New(rand.NewSource(1))
	check := func(s string) {
		t.Helper()
		var want []token
		g := uniseg.NewGraphemes(s)
		for g.Next() {
			want = append(want, token{g.Str(), g.Width(), false})
		}
		got := tokens(s)
		if len(got) != len(want) {
			t.Fatalf("%q: %d tokens, want %d\n got %+v\nwant %+v", s, len(got), len(want), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%q: token %d is %q (%d cells), want %q (%d cells)", s, i, got[i].s, got[i].width, want[i].s, want[i].width)
			}
		}
	}
	for _, a := range pieces {
		for _, b := range pieces {
			for _, c := range pieces {
				check(a + b + c)
			}
		}
	}
	for range 20000 {
		var b strings.Builder
		for range 1 + random.Intn(12) {
			b.WriteString(pieces[random.Intn(len(pieces))])
		}
		check(b.String())
	}
}

func BenchmarkStringWidthASCII(b *testing.B) {
	line := "[INFO] GET https://api.nuget.org/v3/registration5-gz-semver2/newtonsoft.json/index.json 200 in 143ms"
	b.ReportAllocs()
	for b.Loop() {
		StringWidth(line)
	}
}

func BenchmarkStringWidthMixed(b *testing.B) {
	line := "\x1b[33m▲ Not added: Contoso.Wörld 1.2.3 comes from source nuget.org – 日本語\x1b[0m"
	b.ReportAllocs()
	for b.Loop() {
		StringWidth(line)
	}
}

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
