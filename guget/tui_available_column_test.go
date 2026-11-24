package main

import (
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
	text "github.com/nulifyer/guget/internal/ui/text"
)

// Under compatible, or with a prerelease reference, Available holds values
// such as "4.4.1-dev-02453 (4.4.0)". The column narrows instead of
// disappearing for every row, and never shows a clipped version as whole.
func TestAvailableColumnNarrowsBeforeItHides(t *testing.T) {
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, "")
	m.updateTarget = UpdateTargetCompatible
	setTestPackage(m, "B", "1.0.0", "1.1.0", "1.2.0-preview.7.26381.103")
	setTestPackage(m, "A", "1.0.0", "1.1.0")
	wide := m.packageColumns(140)
	if !wide.showAvail || wide.availableW != style.Width("1.2.0-preview.7.26381.103 (1.1.0)")+3 {
		t.Fatalf("wide layout: %+v", wide)
	}
	for _, w := range []int{64, 60, 56, 52} {
		columns := m.packageColumns(w)
		if !columns.showAvail {
			t.Fatalf("width %d hides Available: %+v", w, columns)
		}
		if used := 4 + columns.nameW + columns.currentW + columns.availableW; used > columns.innerW {
			t.Fatalf("width %d allocates %d cells inside %d", w, used, columns.innerW)
		}
		for _, row := range m.packages.rows {
			cell := text.Strip(renderAvailableCell(row, columns.availableW))
			full := availableVersionText(row)
			if style.Width(cell) > columns.availableW-1 {
				t.Fatalf("width %d: %q exceeds its %d cells", w, cell, columns.availableW)
			}
			if cell != full && !strings.HasSuffix(cell, "...") && !strings.HasSuffix(cell, "(...)") {
				t.Fatalf("width %d silently clips %q to %q", w, full, cell)
			}
		}
		frame := text.Strip((&packagePanel{sectionBase: sectionBase{app: m}}).Draw(ui.Rect{W: w, H: 12}))
		for _, line := range strings.Split(frame, "\n") {
			if style.Width(line) > w {
				t.Fatalf("width %d: row is %d cells: %q", w, style.Width(line), line)
			}
		}
	}
	// The stable choice gives way first; the update target stays whole.
	row := packageRow{ref: PackageReference{Name: "A", Version: ParseSemVer("11.12.0")}, latestCompatible: &PackageVersion{SemVer: ParseSemVer("11.12.0")}, latestStable: &PackageVersion{SemVer: ParseSemVer("12.2.0")}}
	if cell := text.Strip(renderAvailableCell(row, 16)); cell != "11.12.0 (...)" {
		t.Fatalf("collapsed cell = %q", cell)
	}
	if cell := text.Strip(renderAvailableCell(row, 19)); cell != "11.12.0 (12.2.0)" {
		t.Fatalf("full cell = %q", cell)
	}
	// Too narrow for the header: the column goes, as before.
	if m.packageColumns(40).showAvail {
		t.Fatal("Available shown without room for its header")
	}
}
