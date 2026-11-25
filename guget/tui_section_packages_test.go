package main

import (
	"slices"
	"strings"
	"testing"
	"time"

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

func TestStatusSortListsADependencyBeforeTheRowsThatNeedIt(t *testing.T) {
	update := &PackageVersion{SemVer: ParseSemVer("2.0.0")}
	type spec struct {
		name       string
		vulnerable bool
		current    bool // up to date: no update target
		needs      []string
	}
	tests := []struct {
		name     string
		rows     []spec
		reversed bool
		want     string
	}{
		{name: "no requirements keep priority then name", rows: []spec{{name: "C"}, {name: "B", vulnerable: true}, {name: "A"}, {name: "D", current: true}}, want: "B A C D"},
		{name: "dependency before its dependent", rows: []spec{{name: "A", vulnerable: true, needs: []string{"C"}}, {name: "B"}, {name: "C"}}, want: "C A B"},
		{name: "before the first of several dependents", rows: []spec{{name: "A"}, {name: "B", needs: []string{"z"}}, {name: "C", needs: []string{"Z"}}, {name: "Z"}}, want: "A Z B C"},
		{name: "dependency already above stays", rows: []spec{{name: "A"}, {name: "B", needs: []string{"A"}}, {name: "C"}}, want: "A B C"},
		{name: "chain", rows: []spec{{name: "A", vulnerable: true, needs: []string{"B"}}, {name: "B", needs: []string{"C"}}, {name: "C", needs: []string{"D"}}, {name: "D"}, {name: "E", vulnerable: true}}, want: "D C B A E"},
		{name: "several dependencies in requirement order", rows: []spec{{name: "A", needs: []string{"D", "C"}}, {name: "B"}, {name: "C"}, {name: "D"}}, want: "D C A B"},
		{name: "cycle keeps priority order", rows: []spec{{name: "A", needs: []string{"B"}}, {name: "B", needs: []string{"A"}}, {name: "C"}}, want: "A B C"},
		{name: "longer cycle and self requirement", rows: []spec{{name: "A", needs: []string{"A", "C"}}, {name: "B", needs: []string{"A"}}, {name: "C", needs: []string{"B"}}}, want: "A B C"},
		{name: "cycle members still follow an outside dependency", rows: []spec{{name: "A", needs: []string{"B", "D"}}, {name: "B", needs: []string{"A"}}, {name: "C"}, {name: "D"}}, want: "D A B C"},
		{name: "missing dependency row", rows: []spec{{name: "A", vulnerable: true, needs: []string{"Transitive"}}, {name: "B"}}, want: "A B"},
		{name: "dependency with no update stays", rows: []spec{{name: "A", needs: []string{"C"}}, {name: "B"}, {name: "C", current: true}}, want: "A B C"},
		{name: "reversed keeps the dependency first", rows: []spec{{name: "A", vulnerable: true, needs: []string{"C"}}, {name: "B"}, {name: "C"}, {name: "D", current: true}}, reversed: true, want: "D C B A"},
		{name: "reversed hoists a dependency from below", rows: []spec{{name: "A"}, {name: "B", needs: []string{"A"}}, {name: "C"}}, reversed: true, want: "C A B"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := make([]packageRow, len(tt.rows))
			for i, s := range tt.rows {
				rows[i] = packageRow{ref: PackageReference{Name: s.name}, vulnerable: s.vulnerable}
				if !s.current {
					rows[i].updateTarget = update
				}
				for _, id := range s.needs {
					rows[i].updateRequirements = append(rows[i].updateRequirements, updateDependencyRequirement{packageID: id})
				}
			}
			done := make(chan string, 1)
			go func() {
				sortPackageRowsByName(rows)
				sortPackageRowsByStatus(rows)
				if tt.reversed {
					slices.Reverse(rows)
				}
				hoistUpdateDependencies(rows)
				names := make([]string, len(rows))
				for i, row := range rows {
					names[i] = row.ref.Name
				}
				done <- strings.Join(names, " ")
			}()
			select {
			case got := <-done:
				if got != tt.want {
					t.Fatalf("order = %s, want %s", got, tt.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the sort did not finish")
			}
		})
	}
}
