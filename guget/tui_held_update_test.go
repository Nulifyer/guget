package main

import (
	"os"
	"strings"
	"testing"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
)

const heldTestProject = `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/><PackageReference Include="B" Version="1.0.0"/><PackageReference Include="C" Version="1.0.0"/></ItemGroup></Project>`

// heldTestApp returns an app whose clock the test advances by hand.
func heldTestApp(t *testing.T, content string) (*App, chan ui.Event, string, *time.Time) {
	t.Helper()
	m, messages, path := newActionTestApp(t, content)
	now := time.Unix(1_700_000_000, 0)
	m.clock = func() time.Time { return now }
	return m, messages, path, &now
}

// pressHeld sends key the way keyboard repeat does, 33 ms apart, and returns
// the effect of the last press.
func pressHeld(m *App, now *time.Time, key rune, times int) ui.Effect {
	var effect ui.Effect
	for range times {
		*now = now.Add(33 * time.Millisecond)
		effect = m.HandleEvent(ui.KeyPress(ui.Key{Code: key}))
	}
	return effect
}

// refuseHeldTestPackage leaves name with no version its project can use and
// selects it, so a quick update refuses the row.
func refuseHeldTestPackage(m *App, name string) {
	m.ctx.Results[name] = nugetResult{pkg: &PackageInfo{ID: name, Versions: []PackageVersion{
		{SemVer: ParseSemVer("9.0.0"), Frameworks: []TargetFramework{ParseTargetFramework("net9.0")}},
	}}}
	m.rebuildPackageRows()
	m.selectPackageByName(name)
}

func heldTestVersions(t *testing.T, path string) map[string]string {
	t.Helper()
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	versions := make(map[string]string)
	for ref := range project.Packages {
		versions[ref.Name] = ref.Version.String()
	}
	return versions
}

func TestHeldUpdateSkipsRefusedRowsAndRestsOnThem(t *testing.T) {
	for _, key := range []rune{'u', 'U'} {
		t.Run(string(key), func(t *testing.T) {
			m, messages, path, now := heldTestApp(t, heldTestProject)
			refuseHeldTestPackage(m, "A")
			// The first presses are taps: they report the refusal and stay.
			if pressHeld(m, now, key, 2) != nil || selectedActionPackage(m) != "A" || !strings.Contains(m.ctx.StatusLine, "No compatible stable version for A") {
				t.Fatalf("selected=%s status=%q", selectedActionPackage(m), m.ctx.StatusLine)
			}
			// From the third press the key is held: each press updates the
			// next row that can be updated.
			for _, name := range []string{"B", "C"} {
				save := pressHeld(m, now, key, 1)
				if save == nil || !m.ctx.Saving || selectedActionPackage(m) != name {
					t.Fatalf("held press did not start %s: selected=%s", name, selectedActionPackage(m))
				}
				executeActionCmd(t, m, save)
				finishActionReload(t, m, messages)
			}
			versions := heldTestVersions(t, path)
			if versions["A"] != "1.0.0" || versions["B"] != "2.0.0" || versions["C"] != "2.0.0" {
				t.Fatalf("versions=%v", versions)
			}
			// With nothing left to update, the hold rests on the row it could not.
			if pressHeld(m, now, key, 3) != nil || m.ctx.Saving || selectedActionPackage(m) != "A" ||
				!strings.Contains(m.ctx.StatusLine, "No compatible stable version for A") || !m.ctx.StatusIsErr {
				t.Fatalf("selected=%s status=%q", selectedActionPackage(m), m.ctx.StatusLine)
			}
		})
	}
}

// Sorted by name, an updated row keeps its place, so a held key must step
// past it to reach the next package.
func TestHeldUpdateSweepsAListThatDoesNotResort(t *testing.T) {
	m, messages, path, now := heldTestApp(t, heldTestProject)
	m.setPackageSort(sortByName)
	m.selectPackageByName("A")
	for range 6 {
		save := pressHeld(m, now, 'u', 1)
		executeActionCmd(t, m, save)
		finishActionReload(t, m, messages)
	}
	for name, version := range heldTestVersions(t, path) {
		if version != "2.0.0" {
			t.Fatalf("%s=%s, want 2.0.0", name, version)
		}
	}
}

func TestHeldUpdateStartsNothingWithoutAPress(t *testing.T) {
	m, messages, path, now := heldTestApp(t, heldTestProject)
	save := pressHeld(m, now, 'u', 1)
	if save == nil || !m.ctx.Saving {
		t.Fatal("first press did not start an update")
	}
	// Presses during the save start nothing. Once they are a hold, they also
	// leave the status alone.
	if pressHeld(m, now, 'u', 1) != nil {
		t.Fatal("a press during a save scheduled work")
	}
	m.ctx.StatusLine = "last result"
	if pressHeld(m, now, 'u', 3) != nil || m.ctx.StatusLine != "last result" {
		t.Fatalf("held presses during a save scheduled work or changed the status to %q", m.ctx.StatusLine)
	}
	// The key is released: the finished save starts no further update.
	executeActionCmd(t, m, save)
	finishActionReload(t, m, messages)
	versions := heldTestVersions(t, path)
	if m.ctx.Saving || versions["A"] != "2.0.0" || versions["B"] != "1.0.0" || versions["C"] != "1.0.0" {
		t.Fatalf("saving=%v versions=%v, want only A updated", m.ctx.Saving, versions)
	}
}

func TestSeparateTapsDoNotSweep(t *testing.T) {
	m, _, _, now := heldTestApp(t, heldTestProject)
	refuseHeldTestPackage(m, "A")
	for range 5 {
		*now = now.Add(heldKeyWindow + time.Millisecond)
		if m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'})) != nil || selectedActionPackage(m) != "A" {
			t.Fatal("separate taps moved off the refused row")
		}
	}
}

func TestHeldUpdateEndsOnAnotherKeyOrTheMouse(t *testing.T) {
	events := map[string]ui.Event{
		"other key":  ui.KeyPress(ui.Key{Code: 'l'}),
		"other case": ui.KeyPress(ui.Key{Code: 'U'}),
		"mouse":      mouseInputMsg{event: ui.MouseWheel(ui.Mouse{X: 1, Y: 1, Button: ui.MouseWheelDown})},
	}
	for name, event := range events {
		t.Run(name, func(t *testing.T) {
			m, _, _, now := heldTestApp(t, heldTestProject)
			refuseHeldTestPackage(m, "A")
			pressHeld(m, now, 'u', 2)
			m.HandleEvent(event)
			// Without the interruption this press would be the third of a hold.
			if pressHeld(m, now, 'u', 1) != nil || m.ctx.Saving || selectedActionPackage(m) != "A" {
				t.Fatalf("hold survived: saving=%v selected=%s", m.ctx.Saving, selectedActionPackage(m))
			}
		})
	}
}

func TestHeldUpdateNeverConfirmsAPin(t *testing.T) {
	m, messages, path, now := heldTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="[1.0.0]"/><PackageReference Include="B" Version="1.0.0"/></ItemGroup></Project>`)
	m.selectPackageByName("A")
	m.held = heldUpdate{key: "u", run: heldKeyRun, last: *now}
	save := pressHeld(m, now, 'u', 1)
	if save == nil || m.confirmUpdate.active || selectedActionPackage(m) != "B" {
		t.Fatalf("held press did not skip the pin: confirm=%v selected=%s", m.confirmUpdate.active, selectedActionPackage(m))
	}
	executeActionCmd(t, m, save)
	finishActionReload(t, m, messages)
	if pressHeld(m, now, 'u', 2) != nil || m.confirmUpdate.active || m.ctx.Saving || selectedActionPackage(m) != "A" || !strings.Contains(m.ctx.StatusLine, "A is pinned") {
		t.Fatalf("confirm=%v selected=%s status=%q", m.confirmUpdate.active, selectedActionPackage(m), m.ctx.StatusLine)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `Include="A" Version="[1.0.0]"`) || !strings.Contains(string(data), `Include="B" Version="2.0.0"`) {
		t.Fatalf("project=%s", data)
	}
}
