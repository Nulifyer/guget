package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCentralAddOffersOnlyValidLocation(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	project := m.selectedProject()
	central := filepath.Join(filepath.Dir(project.FilePath), "Directory.Packages.props")
	project.AddTargets = []AddTarget{{Kind: AddTargetProject, FilePath: project.FilePath}, {Kind: AddTargetBuildProps, FilePath: filepath.Join(filepath.Dir(project.FilePath), "Directory.Build.props")}, {Kind: AddTargetCPM, FilePath: central}}
	if cmd := m.openLocationPickerOrAdd("C", "2.0.0", project); cmd != nil || !m.locationPick.active {
		t.Fatal("central destination review did not open")
	}
	if len(m.locationPick.targets) != 1 || m.locationPick.targets[0].Kind != AddTargetCPM || m.locationPick.targets[0].FilePath != central {
		t.Fatalf("invalid central add choices: %+v", m.locationPick.targets)
	}
}

func TestCentralVersionsViewAsksForReferenceProject(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	central := filepath.Join(filepath.Dir(m.selectedProject().FilePath), "Directory.Packages.props")
	mustWriteFile(t, central, `<Project><PropertyGroup><ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally></PropertyGroup><ItemGroup><PackageVersion Include="A" Version="1.0.0"/></ItemGroup></Project>`)
	m.selectedProject().AddTargets = append(m.selectedProject().AddTargets, AddTarget{Kind: AddTargetCPM, FilePath: central})
	view, err := ParsePropsAsProject(central)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(central)
	if cmd := m.openLocationPickerOrAdd("C", "2.0.0", view); cmd != nil || m.ctx.Saving || !strings.Contains(m.ctx.StatusLine, "Select a project") {
		t.Fatalf("central view did not explain destination: %q", m.ctx.StatusLine)
	}
	after, _ := os.ReadFile(central)
	if string(after) != string(before) {
		t.Fatal("central view wrote a project reference")
	}
}
