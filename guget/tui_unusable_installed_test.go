package main

import (
	"strings"
	"testing"

	text "github.com/nulifyer/guget/internal/ui/text"
)

// An installed version the project's frameworks cannot use, such as one
// taken with a, used to read as up to date: a green check and no note. The
// row now carries its own marker and the detail panel says why.
func TestInstalledVersionTheFrameworksCannotUseIsFlagged(t *testing.T) {
	initTheme("auto-dark", false)
	// The test feed lists 3.0.0 for net9.0 only, 2.0.0 for net8.0, and 1.0.0
	// without frameworks; C 5.0.0 is not on the feed at all.
	m, _, _ := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="3.0.0"/><PackageReference Include="B" Version="2.0.0"/><PackageReference Include="C" Version="5.0.0"/></ItemGroup></Project>`)
	m.rebuildPackageRows()
	rows := func() map[string]packageRow {
		byName := map[string]packageRow{}
		for _, row := range m.packages.rows {
			byName[row.ref.Name] = row
		}
		return byName
	}

	selected := rows()
	a := selected["A"]
	if a.statusIcon() != "!" || a.statusStyle().Render("x") != styleYellow.Render("x") || len(a.unusable) != 1 {
		t.Fatalf("A: icon=%q unusable=%+v", a.statusIcon(), a.unusable)
	}
	if b := selected["B"]; b.statusIcon() != "✓" || len(b.unusable) != 0 {
		t.Fatalf("B, on a usable version: icon=%q unusable=%+v", b.statusIcon(), b.unusable)
	}
	if c := selected["C"]; len(c.unusable) != 0 {
		t.Fatalf("C, on a version the feed does not list: unusable=%+v", c.unusable)
	}
	detail := strings.Join(strings.Fields(text.Strip(m.renderDetail(a))), " ")
	if !strings.Contains(detail, "Installed version not usable 3.0.0 does not support every target framework of the affected projects. v marks the versions that do.") {
		t.Fatalf("selected project detail: %s", detail)
	}
	if detail := text.Strip(m.renderDetail(selected["B"])); strings.Contains(detail, "Installed version not usable") {
		t.Fatalf("B gained the note:\n%s", detail)
	}

	// All Projects names the project whose frameworks the version misses.
	m.projects.cursor = 0
	m.rebuildPackageRows()
	a = rows()["A"]
	if a.statusIcon() != "!" {
		t.Fatalf("All Projects A: icon=%q", a.statusIcon())
	}
	detail = strings.Join(strings.Fields(text.Strip(m.renderDetail(a))), " ")
	if !strings.Contains(detail, "3.0.0 does not support every target framework of App.csproj. v marks the versions that do.") {
		t.Fatalf("All Projects detail: %s", detail)
	}

	// An available update, a vulnerability, or a deprecation keeps its own
	// marker; the note still appears.
	a.updateTarget = &PackageVersion{SemVer: ParseSemVer("4.0.0")}
	if a.statusIcon() != "↑" {
		t.Fatalf("update marker lost: %q", a.statusIcon())
	}
}
