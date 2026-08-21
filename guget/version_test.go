package main

import "testing"

func TestVersionStringUsesLinkerVersion(t *testing.T) {
	original := version
	version = "v1.2.3"
	t.Cleanup(func() { version = original })

	if got, want := versionString(), "guget v1.2.3"; got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}
}
