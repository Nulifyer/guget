package main

import (
	"reflect"
	"testing"
)

func TestExternalURLCommand(t *testing.T) {
	tests := []struct {
		goos        string
		wantCommand string
		wantArgs    []string
	}{
		{goos: "linux", wantCommand: "xdg-open", wantArgs: []string{"https://example.com/package"}},
		{goos: "darwin", wantCommand: "open", wantArgs: []string{"https://example.com/package"}},
		{goos: "windows", wantCommand: "rundll32.exe", wantArgs: []string{"url.dll,FileProtocolHandler", "https://example.com/package"}},
	}
	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			command, args, err := externalURLCommand(test.goos, "https://example.com/package")
			if err != nil {
				t.Fatal(err)
			}
			if command != test.wantCommand || !reflect.DeepEqual(args, test.wantArgs) {
				t.Fatalf("command = %q %v, want %q %v", command, args, test.wantCommand, test.wantArgs)
			}
		})
	}
}

func TestExternalURLCommandRejectsUnsafeSchemes(t *testing.T) {
	for _, rawURL := range []string{"file:///tmp/package", "javascript:alert(1)", "relative/path"} {
		if _, _, err := externalURLCommand("linux", rawURL); err == nil {
			t.Fatalf("expected %q to be rejected", rawURL)
		}
	}
}
