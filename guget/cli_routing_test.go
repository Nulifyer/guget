package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestRouteCLIArgs(t *testing.T) {
	for _, test := range []struct {
		args []string
		want []string
		ok   bool
	}{
		{args: []string{"list"}, want: []string{"list"}, ok: true},
		{args: []string{"--project", ".", "list"}, want: []string{"list", "--project", "."}, ok: true},
		{args: []string{"--project=.", "list", "--format", "json"}, want: []string{"list", "--project=.", "--format", "json"}, ok: true},
		{args: []string{"--no-color", "sources"}, want: []string{"sources", "--no-color"}, ok: true},
		{args: []string{"--include-ignored", "--project", "src", "show", "X"}, want: []string{"show", "--include-ignored", "--project", "src", "X"}, ok: true},
		{args: []string{"--project", "list"}, ok: false},
		{args: []string{"--no-color"}, ok: false},
		{args: []string{"./src"}, ok: false},
		{args: []string{"--theme", "dark"}, ok: false},
		{args: []string{"-p", ".", "list"}, ok: false},
	} {
		got, ok := routeCLIArgs(test.args)
		if ok != test.ok || ok && strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
			t.Errorf("route(%q) = %q, %v; want %q, %v", test.args, got, ok, test.want, test.ok)
		}
	}
}

func TestTUIHelpIsDeterministicAndNamesCommands(t *testing.T) {
	capture := func() string {
		t.Helper()
		resetCLIParserForTest(t)
		registerCLIFlags(effectiveSettings())
		oldEpilogue := usageEpilogue
		usageEpilogue = tuiUsageEpilogue()
		t.Cleanup(func() { usageEpilogue = oldEpilogue })
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		oldStdout := os.Stdout
		os.Stdout = writer
		PrintUsage()
		os.Stdout = oldStdout
		writer.Close()
		data, _ := io.ReadAll(reader)
		return string(data)
	}
	first := capture()
	for i := 0; i < 5; i++ {
		if again := capture(); again != first {
			t.Fatalf("TUI help changed between runs:\n%s\n---\n%s", first, again)
		}
	}
	if !strings.Contains(first, "Commands: list, show") || strings.Index(first, "include-ignored") > strings.Index(first, "no-color") {
		t.Fatalf("help=%s", first)
	}
}
