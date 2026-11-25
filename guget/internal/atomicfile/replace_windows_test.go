//go:build windows

package atomicfile

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestOnlyFlushedRenamesWriteThrough(t *testing.T) {
	if moveFlags(Flushed)&windows.MOVEFILE_WRITE_THROUGH == 0 {
		t.Fatal("a flushed rename must write through")
	}
	if moveFlags(Buffered)&windows.MOVEFILE_WRITE_THROUGH != 0 {
		t.Fatal("a buffered rename must not write through")
	}
	for _, durability := range []Durability{Flushed, Buffered} {
		if moveFlags(durability)&windows.MOVEFILE_REPLACE_EXISTING == 0 {
			t.Fatalf("durability %d must replace the destination", durability)
		}
	}
}
