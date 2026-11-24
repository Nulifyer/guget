package main

import (
	"os"
	"strings"
	"testing"
)

func setTestPackage(m *App, id string, specs ...string) {
	info := testPackage(specs...)
	info.ID = id
	m.ctx.Results[id] = nugetResult{pkg: info, source: "local"}
	m.rebuildPackageRows()
	m.selectPackageByName(id)
}

func selectedRow(m *App) packageRow { return m.packages.rows[m.packages.cursor] }

// Issue #14: a preview reference sees the next preview under the default,
// a stable reference does not.
func TestTUIRowsFollowThePrereleaseRule(t *testing.T) {
	m, _, _ := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="11.0.0-preview.6"/><PackageReference Include="B" Version="10.6.0"/></ItemGroup></Project>`)
	feed := []string{"10.6.0", "10.7.0", "11.0.0-preview.6", "11.0.0-preview.7"}
	setTestPackage(m, "B", feed...)
	setTestPackage(m, "A", feed...)
	row := selectedRow(m)
	if versionText(row.updateTarget) != "11.0.0-preview.7" || row.statusIcon() != "↑" {
		t.Fatalf("preview reference: target %s icon %s", versionText(row.updateTarget), row.statusIcon())
	}
	m.selectPackageByName("B")
	if row := selectedRow(m); versionText(row.updateTarget) != "10.7.0" || availableVersionText(row) != "10.7.0" {
		t.Fatalf("stable reference: target %s available %q", versionText(row.updateTarget), availableVersionText(row))
	}
	m.updateTarget = UpdateTargetCompatible
	m.rebuildPackageRows()
	m.selectPackageByName("B")
	if row := selectedRow(m); versionText(row.updateTarget) != "11.0.0-preview.7" || availableVersionText(row) != "11.0.0-preview.7 (10.7.0)" {
		t.Fatalf("compatible: target %s available %q", versionText(row.updateTarget), availableVersionText(row))
	}
}

// Only an incompatible newer version exists: that is not an update.
func TestTUIIncompatibleOnlyUpdateIsNotOffered(t *testing.T) {
	m, _, path := newActionTestApp(t, "")
	setTestPackage(m, "A", "1.0.0", "2.0.0@net9.0")
	row := selectedRow(m)
	if row.updateTarget != nil || row.statusIcon() != "✓" || versionText(row.latestStable) != "2.0.0" {
		t.Fatalf("target %s icon %s stable %s", versionText(row.updateTarget), row.statusIcon(), versionText(row.latestStable))
	}
	// Nothing compatible newer, and also nothing compatible at all.
	setTestPackage(m, "A", "2.0.0@net9.0", "3.0.0@net9.0")
	if row := selectedRow(m); row.updateTarget != nil || row.statusIcon() != "✓" || availableVersionText(row) != "- (3.0.0)" {
		t.Fatalf("icon %s available %q", row.statusIcon(), availableVersionText(row))
	}
	before, _ := os.ReadFile(path)
	m.updatePackage(false, scopeSelected)
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || !strings.Contains(m.ctx.StatusLine, "No compatible") {
		t.Fatalf("status %q", m.ctx.StatusLine)
	}
	// a follows the stable policy, which ignores frameworks.
	executeActionCmd(t, m, m.updatePackage(true, scopeSelected))
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `Include="A" Version="3.0.0"`) {
		t.Fatalf("a did not take the stable target: %s", data)
	}
}

func TestTUIQuickUpdateNeverLowersAPrerelease(t *testing.T) {
	m, _, path := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="3.0.0-beta"/></ItemGroup></Project>`)
	setTestPackage(m, "A", "1.0.0", "2.0.0", "3.0.0-beta")
	before, _ := os.ReadFile(path)
	for _, stable := range []bool{false, true} {
		if cmd := m.updatePackage(stable, scopeSelected); cmd != nil {
			executeActionCmd(t, m, cmd)
		}
	}
	if after, _ := os.ReadFile(path); string(before) != string(after) {
		t.Fatalf("quick update lowered a prerelease: %s", after)
	}
	if row := selectedRow(m); row.updateTarget != nil || row.statusIcon() != "✓" {
		t.Fatalf("icon %s", row.statusIcon())
	}
}

func TestTUIDeprecationFollowsTheInstalledVersion(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	info := testPackage("1.0.0", "2.0.0")
	info.ID = "A"
	info.Deprecated, info.DeprecationMessage = true, "package-level notice"
	info.Versions[0].Deprecation = &PackageDeprecation{Message: "2.0.0 is broken", AlternatePackageID: "A2"}
	m.ctx.Results["A"] = nugetResult{pkg: info, source: "local"}
	m.rebuildPackageRows()
	m.selectPackageByName("A")
	row := selectedRow(m)
	if row.deprecated || strings.Contains(m.renderDetail(row), "Deprecated") {
		t.Fatal("installed 1.0.0 is not deprecated")
	}
	info.Versions[1].Deprecation = &PackageDeprecation{Message: "1.0.0 is legacy", AlternatePackageID: "A2"}
	m.rebuildPackageRows()
	m.selectPackageByName("A")
	row = selectedRow(m)
	detail := m.renderDetail(row)
	if !row.deprecated || !strings.Contains(detail, "1.0.0 is legacy") || !strings.Contains(detail, "A2") {
		t.Fatalf("deprecated=%v detail=%s", row.deprecated, detail)
	}
}

func TestTUIPickerDefaultFollowsThePolicy(t *testing.T) {
	m, _, _ := newActionTestApp(t, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="2.0.0-beta"/></ItemGroup></Project>`)
	setTestPackage(m, "A", "1.0.0", "2.0.0-beta", "2.0.0-rc")
	m.openVersionPicker()
	if v := m.picker.selectedVersion(); v == nil || v.SemVer.String() != "2.0.0-rc" {
		t.Fatalf("picker default %v", v)
	}
}
