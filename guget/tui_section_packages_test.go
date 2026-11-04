package main

import (
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui/style"
	ansi "github.com/nulifyer/guget/internal/ui/text"
)

func TestDivergedPackageVersionsHaveAColumnGutter(t *testing.T) {
	row := packageRow{
		ref: PackageReference{
			Name:    "Polly",
			Version: ParseSemVer("8.5.28"),
		},
		diverged:         true,
		oldest:           ParseSemVer("8.0.0"),
		latestCompatible: &PackageVersion{SemVer: ParseSemVer("9.0.0")},
	}
	app := newMouseTestApp()
	app.packages.rows = []packageRow{row}
	columns := app.packageColumns(80)

	line := ansi.Strip(renderCurrentVersion(row, columns.currentW) + renderAvailableVersion(row))
	if strings.Contains(line, "8.0.0–8.0.0") {
		t.Fatalf("diverged version repeats its lower bound: %q", line)
	}
	if !strings.Contains(line, "8.0.0–8.5.28   9.0.0") {
		t.Fatalf("version columns lack the three-cell gutter: %q", line)
	}
}

func TestNarrowPackageColumnsBudgetDivergentAndPreviewVersions(t *testing.T) {
	m := NewApp(".", &workspaceSnapshot{}, nil, BuiltFlags{})
	m.packages.rows = []packageRow{
		{ref: PackageReference{Name: "Newtonsoft.Json", Version: ParseSemVer("13.0.4")}, diverged: true, oldest: ParseSemVer("12.0.3")},
		{ref: PackageReference{Name: "Microsoft.Extensions.Logging.Abstractions", Version: ParseSemVer("9.0.0-rtm.24516.5"), Requested: "9.0.0-rtm.24516.5"}},
	}
	for _, w := range []int{40, 50, 70, 100} {
		columns := m.packageColumns(w)
		used := 4 + columns.nameW + columns.currentW
		if columns.showAvail {
			used += columns.availableW
		}
		if columns.showSource {
			used += columns.sourceW
		}
		if used > columns.innerW {
			t.Fatalf("width %d allocates %d cells inside %d", w, used, columns.innerW)
		}
		for _, row := range m.packages.rows {
			rendered := ansi.Strip(renderCurrentVersion(row, columns.currentW))
			if len(currentVersionText(row)) >= columns.currentW && !strings.Contains(rendered, "...") {
				t.Fatalf("width %d silently clips version: %q", w, rendered)
			}
			if style.Width(rendered) > columns.currentW {
				t.Fatalf("version exceeds its allocation: %q", rendered)
			}
		}
	}
}
