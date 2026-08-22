package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
