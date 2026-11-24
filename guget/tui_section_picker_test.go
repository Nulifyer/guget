package main

import (
	"strings"
	"testing"

	style "github.com/nulifyer/guget/internal/ui/style"
	text "github.com/nulifyer/guget/internal/ui/text"
)

func TestPickerMarksEachDeprecatedVersion(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, "")
	info := testPackage("1.0.0", "2.0.0@net8.0", "3.0.0@net8.0")
	info.ID = "A"
	m.ctx.Results["A"] = nugetResult{pkg: info, source: "local"}
	m.rebuildPackageRows()
	m.selectPackageByName("A")
	m.openVersionPicker()
	if frame := text.Strip(m.picker.Render()); strings.Contains(frame, "~") {
		t.Fatalf("picker marks a deprecation no version has:\n%s", frame)
	}
	info.Versions[2].Deprecation = &PackageDeprecation{Message: "1.0.0 is legacy"}
	m.openVersionPicker()
	frame := text.Strip(m.picker.Render())
	var marked []string
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, " ~") && !strings.Contains(line, "depr") {
			marked = append(marked, line)
		}
	}
	if len(marked) != 1 || !strings.Contains(marked[0], "1.0.0") || !strings.Contains(frame, "~ depr") {
		t.Fatalf("deprecated rows %q in:\n%s", marked, frame)
	}
	for _, line := range strings.Split(frame, "\n") {
		if style.Width(line) > m.ctx.Width {
			t.Fatalf("picker row exceeds the terminal: %q", line)
		}
	}
}
