package main

import (
	"fmt"
	"strings"
	"testing"

	text "github.com/nulifyer/guget/internal/ui/text"
)

// A package whose projects' sources share no version is explained in the
// detail panel and on u, without the retry hint a failed lookup gets.
func TestNoCommonVersionRowExplainsItself(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Results["A"] = nugetResult{err: fmt.Errorf("package %q: %w", "A", errNoCommonVersion)}
	m.rebuildPackageRows()
	for i, row := range m.packages.rows {
		if row.ref.Name == "A" {
			m.packages.cursor = i
		}
	}
	row := m.packages.rows[m.packages.cursor]
	if row.err == nil || row.statusIcon() != "✗" {
		t.Fatalf("row A: err=%v icon=%q", row.err, row.statusIcon())
	}
	for _, width := range []int{22, 46} {
		m.detail.vp.SetWidth(width)
		detail := text.Strip(m.renderDetail(row))
		joined := strings.Join(strings.Fields(detail), " ")
		for _, want := range []string{"No version for every project", "use different NuGet sources", "Project versions", "1.0.0"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("width %d: detail lacks %q:\n%s", width, want, detail)
			}
		}
		if strings.Contains(joined, "Error:") {
			t.Fatalf("width %d: detail still shows a bare error:\n%s", width, detail)
		}
		for _, line := range strings.Split(detail, "\n") {
			if text.StringWidth(line) > width {
				t.Fatalf("width %d: detail line is %d cells: %q", width, text.StringWidth(line), line)
			}
		}
	}

	m.ctx.Width = 80
	m.updatePackage(false, scopeSelected)
	status := m.ctx.StatusLine
	if !m.ctx.StatusIsErr || status != "Cannot update A: its projects' NuGet sources share no version" {
		t.Fatalf("status %q", status)
	}

	// Any other lookup failure keeps its message and the retry hint.
	m.ctx.Width = 140
	m.ctx.Results["A"] = nugetResult{err: fmt.Errorf("feed unavailable")}
	m.rebuildPackageRows()
	m.updatePackage(false, scopeSelected)
	if !strings.Contains(m.ctx.StatusLine, "feed unavailable; Ctrl+R to retry") {
		t.Fatalf("status %q", m.ctx.StatusLine)
	}
}

// The refused-add reasons lead with the project or file the user must look
// at, so the part the status row keeps is the useful one.
func TestAddSourceProblemLeadsWithTheProject(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	project := m.ctx.ParsedProjects[0]
	m.search.fetchedInfo = &PackageInfo{ID: "Z", Versions: []PackageVersion{{SemVer: ParseSemVer("1.0.0")}}}
	m.search.fetchedSource = "elsewhere"
	got := m.addSourceProblem("Z", "1.0.0", []mutationTarget{{project: project}})
	if want := "App.csproj may not use source elsewhere for Z 1.0.0; see its NuGet configuration"; got != want {
		t.Fatalf("problem %q, want %q", got, want)
	}
}
