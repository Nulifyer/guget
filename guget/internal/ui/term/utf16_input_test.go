package term

import (
	"strings"
	"testing"
)

func TestUTF16InputPairSplitAtConsoleBufferBoundary(t *testing.T) {
	units := make([]uint16, 255)
	for i := range units {
		units[i] = 'a'
	}
	units = append(units, 0xd83d, 0xde00, 'z')
	var d utf16InputDecoder
	got := d.decode(units[:256], false) + d.decode(units[256:], true)
	if got != strings.Repeat("a", 255)+"😀z" {
		t.Fatalf("buffer boundary corrupted supplementary character: %q", got)
	}
}

func TestUTF16InputAcrossReadBoundaries(t *testing.T) {
	for _, chunks := range [][][]uint16{{{0xd83d, 0xde00}}, {{0xd83d}, {0xde00}}, {{'a', 0xd83d}, {0xde00, 'b'}}} {
		var d utf16InputDecoder
		got := ""
		for _, chunk := range chunks {
			got += d.decode(chunk, false)
		}
		got += d.decode(nil, true)
		want := "😀"
		if len(chunks[0]) > 1 && chunks[0][0] == 'a' {
			want = "a😀b"
		}
		if got != want {
			t.Fatalf("decode %x = %q, want %q", chunks, got, want)
		}
	}
}
func TestUTF16InputRejectsBrokenPairsWithoutLosingFollowingText(t *testing.T) {
	var d utf16InputDecoder
	if got := d.decode([]uint16{0xd83d, 'a', 0xdc00}, true); got != "�a�" {
		t.Fatalf("decoded %q", got)
	}
}
