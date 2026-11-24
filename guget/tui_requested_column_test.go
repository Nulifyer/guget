package main

import (
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
	text "github.com/nulifyer/guget/internal/ui/text"
)

const longRequest = "[1.0.0-preview.1.24080.9, 3.0.0-preview.7.24405.7)"

func longRequestApp(t *testing.T) *App {
	t.Helper()
	initTheme("auto-dark", false)
	m, _, _ := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="`+longRequest+`"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
	m.rebuildPackageRows()
	return m
}

// One long range or prerelease request used to take the Available column
// away from every row in a 120-column terminal. Requested now gives way
// first and shows an ellipsis; the detail panel has the request in full.
func TestLongRequestDoesNotHideAvailable(t *testing.T) {
	m := longRequestApp(t)
	// The package panel is 62 cells wide in a 120-column terminal.
	for _, w := range []int{62, 56, 52} {
		columns := m.packageColumns(w)
		if !columns.showAvail {
			t.Fatalf("width %d hides Available: %+v", w, columns)
		}
		if columns.currentW < len("Requested")+3 {
			t.Fatalf("width %d leaves Requested %d cells", w, columns.currentW)
		}
		if used := 4 + columns.nameW + columns.currentW + columns.availableW; used > columns.innerW {
			t.Fatalf("width %d allocates %d cells inside %d", w, used, columns.innerW)
		}
		frame := text.Strip((&packagePanel{sectionBase: sectionBase{app: m}}).Draw(ui.Rect{W: w, H: 12}))
		if !strings.Contains(frame, "Available") || !strings.Contains(frame, "[1.0.0-") || !strings.Contains(frame, "...") {
			t.Fatalf("width %d:\n%s", w, frame)
		}
		for _, line := range strings.Split(frame, "\n") {
			if style.Width(line) > w {
				t.Fatalf("width %d: row is %d cells: %q", w, style.Width(line), line)
			}
		}
	}
	// With room for everything, the request is whole.
	wide := m.packageColumns(140)
	if !wide.showAvail || wide.currentW != style.Width(longRequest)+3 {
		t.Fatalf("wide layout: %+v", wide)
	}

	m.selectPackageByName("A")
	detail := strings.Join(strings.Fields(text.Strip(m.renderDetail(selectedRow(m)))), " ")
	if !strings.Contains(detail, "Requested "+longRequest) {
		t.Fatalf("detail lacks the full request: %s", detail)
	}
	m.selectPackageByName("B")
	if detail := text.Strip(m.renderDetail(selectedRow(m))); strings.Contains(detail, "Requested") {
		t.Fatalf("a plain version gained a Requested note:\n%s", detail)
	}
}

// In a narrow detail panel a long project file name or request used to wrap
// to column 0, under the section heading. Both now stay inside the panel,
// the request indented under its project.
func TestProjectVersionsStayIndentedInANarrowPanel(t *testing.T) {
	m := longRequestApp(t)
	project := m.ctx.ParsedProjects[0]
	project.FileName = "OtherWithALongerProjectName.csproj"
	m.projects.cursor = 0
	m.rebuildPackageRows()
	m.selectPackageByName("A")
	for _, width := range []int{21, 31, 46} {
		m.detail.vp.SetWidth(width)
		block := strings.Split(strings.TrimRight(text.Strip(m.renderDetailProjectVersions(selectedRow(m))), "\n"), "\n")
		if block[0] != "Project versions" || len(block) < 3 {
			t.Fatalf("width %d: %q", width, block)
		}
		if !strings.HasPrefix(block[1], "OtherWithALonger") || strings.HasPrefix(block[1], " ") {
			t.Fatalf("width %d: project row %q", width, block[1])
		}
		for _, line := range block[2:] {
			if !strings.HasPrefix(line, "  ") {
				t.Fatalf("width %d: request row at column 0: %q in %q", width, line, block)
			}
		}
		for _, line := range block {
			if text.StringWidth(line) > width {
				t.Fatalf("width %d: row is %d cells: %q", width, text.StringWidth(line), line)
			}
		}
		if joined := strings.ReplaceAll(strings.Join(block[2:], ""), " ", ""); joined != strings.ReplaceAll(longRequest, " ", "") {
			t.Fatalf("width %d: request rows %q", width, block[2:])
		}
	}
	// A short request still sits beside its project.
	m.selectPackageByName("B")
	m.detail.vp.SetWidth(46)
	if block := text.Strip(m.renderDetailProjectVersions(selectedRow(m))); !strings.Contains(block, "  OtherWithALongerP... 1.0.0") {
		t.Fatalf("short request layout: %q", block)
	}
}
