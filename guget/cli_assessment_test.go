package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// assessmentWorkspace is one net8.0 project referencing Upgradable 1.0.0,
// Current 2.0.0, Risky 1.0.0, and Old 1.0.0, evaluated by a fake dotnet that
// reports the same versions restored, plus a PackageDownload of Upgradable.
// The feed has a compatible 1.1.0 and a net9.0-only 2.0.0 of Upgradable, a
// vulnerable Risky 1.0.0, and a deprecated Old 1.0.0.
func assessmentWorkspace(t *testing.T) (string, *fakeDotnet, *testFeeds) {
	t.Helper()
	feeds := newTestFeeds(t)
	feeds.add("feed", "Upgradable", testLeaf{Version: "1.0.0"}, testLeaf{Version: "1.1.0"}, testLeaf{Version: "2.0.0", Frameworks: []string{"net9.0"}}, testLeaf{Version: "3.0.0-beta"})
	feeds.add("feed", "Current", testLeaf{Version: "1.0.0"}, testLeaf{Version: "2.0.0"})
	feeds.add("feed", "Risky", testLeaf{Version: "1.0.0", Vulnerable: true}, testLeaf{Version: "1.0.1"})
	feeds.add("feed", "Old", testLeaf{Version: "1.0.0", Deprecated: []string{"Legacy"}, Alternate: "New"})
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), feeds.config([]string{"feed"}, nil))
	packages := map[string]string{"Upgradable": "1.0.0", "Current": "2.0.0", "Risky": "1.0.0", "Old": "1.0.0"}
	var xml, items, top []string
	for _, id := range []string{"Current", "Old", "Risky", "Upgradable"} {
		xml = append(xml, fmt.Sprintf(`<PackageReference Include=%q Version=%q />`, id, packages[id]))
		top = append(top, fmt.Sprintf(`{"id":%q,"requestedVersion":%q,"resolvedVersion":%q}`, id, packages[id], packages[id]))
	}
	project := writeCLIProject(t, root, "App", `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup>`+strings.Join(xml, "")+`</ItemGroup></Project>`)
	for _, id := range []string{"Current", "Old", "Risky", "Upgradable"} {
		items = append(items, fmt.Sprintf(`{"Identity":%q,"Version":%q,"DefiningProjectFullPath":%q}`, id, packages[id], project))
	}
	evaluation := fmt.Sprintf(`{"Properties":{"TargetFramework":"net8.0"},"Items":{"PackageReference":[%s],"PackageDownload":[{"Identity":"Upgradable","Version":"[1.0.0]","DefiningProjectFullPath":%q}]}}`, strings.Join(items, ","), project)
	graph := fmt.Sprintf(`{"version":1,"projects":[{"path":"x","frameworks":[{"framework":"net8.0","topLevelPackages":[%s]}]}]}`, strings.Join(top, ","))
	return root, &fakeDotnet{msbuild: map[string]string{project: evaluation}, graph: map[string]string{project: graph}}, feeds
}

func decodeList(t *testing.T, stdout string) listDocument {
	t.Helper()
	var document listDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	return document
}

func rowSummary(rows []cliPackageRow) string {
	text := func(value *string) string {
		if value == nil {
			return "-"
		}
		return *value
	}
	flag := func(value *bool) string {
		if value == nil {
			return "?"
		}
		return fmt.Sprint(*value)
	}
	var lines []string
	for _, row := range rows {
		lines = append(lines, fmt.Sprintf("%s download=%t target=%s compat=%s stable=%s latest=%s vulnerable=%s deprecated=%s",
			row.Package, row.Download, text(row.UpdateTarget), text(row.UpdateTargetCompatibility), text(row.LatestStable), text(row.LatestVersion), flag(row.Vulnerable), flag(row.Deprecated)))
	}
	return strings.Join(lines, "\n")
}

func TestListMetadataFillsAssessmentFields(t *testing.T) {
	root, runner, _ := assessmentWorkspace(t)
	code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--metadata", "--format", "json")
	if code != ExitSuccess {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	document := decodeList(t, stdout)
	if document.UpdateTarget == nil || *document.UpdateTarget != "compatible-stable" {
		t.Fatalf("updateTarget = %v", document.UpdateTarget)
	}
	want := strings.Join([]string{
		"Current download=false target=- compat=- stable=2.0.0 latest=2.0.0 vulnerable=false deprecated=false",
		"Old download=false target=- compat=- stable=1.0.0 latest=1.0.0 vulnerable=false deprecated=true",
		"Risky download=false target=1.0.1 compat=compatible stable=1.0.1 latest=1.0.1 vulnerable=true deprecated=false",
		"Upgradable download=false target=1.1.0 compat=compatible stable=2.0.0 latest=3.0.0-beta vulnerable=false deprecated=false",
		"Upgradable download=true target=- compat=- stable=- latest=- vulnerable=? deprecated=?",
	}, "\n")
	if got := rowSummary(document.Packages); got != want {
		t.Fatalf("rows:\n%s\nwant:\n%s", got, want)
	}
	for _, row := range document.Packages {
		switch row.Package {
		case "Risky":
			if len(row.Advisories) != 1 || row.Advisories[0].Severity != "critical" || row.Advisories[0].URL != "https://advisory.example/1.0.0" {
				t.Fatalf("advisories = %+v", row.Advisories)
			}
		case "Old":
			if len(row.DeprecationReasons) != 1 || row.DeprecationReasons[0] != "Legacy" || row.AlternatePackage == nil || *row.AlternatePackage != "New" {
				t.Fatalf("deprecation = %+v %v", row.DeprecationReasons, row.AlternatePackage)
			}
		}
	}
}

func TestListFiltersCombineAsAnyOf(t *testing.T) {
	root, runner, _ := assessmentWorkspace(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--outdated"}, "Risky Upgradable"},
		{[]string{"--vulnerable"}, "Risky"},
		{[]string{"--deprecated"}, "Old"},
		{[]string{"--deprecated", "--vulnerable"}, "Old Risky"},
		{[]string{"--outdated", "--update-target", "stable"}, "Risky Upgradable"},
	}
	for _, test := range cases {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			args := append([]string{"list", "--project", root, "--format", "json"}, test.args...)
			code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, args...)
			if code != ExitSuccess {
				t.Fatalf("code=%d stderr=%s", code, stderr)
			}
			var got []string
			for _, row := range decodeList(t, stdout).Packages {
				got = append(got, row.Package)
			}
			if strings.Join(got, " ") != test.want {
				t.Fatalf("rows %v, want %s", got, test.want)
			}
		})
	}
	// stable ignores frameworks: Upgradable's target becomes the net9.0 build.
	code, stdout, _ := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--outdated", "--update-target", "stable", "--format", "{{.package}} {{.updateTarget}} {{.updateTargetCompatibility}}")
	if code != ExitSuccess || !strings.Contains(stdout, "Upgradable 2.0.0 incompatible") {
		t.Fatalf("code=%d stdout=%s", code, stdout)
	}
}

func TestListUpdateTargetSettingApplies(t *testing.T) {
	root, runner, _ := assessmentWorkspace(t)
	t.Setenv("GUGET_UPDATE_TARGET", "compatible")
	code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--outdated", "--format", "json")
	document := decodeList(t, stdout)
	if code != ExitSuccess || *document.UpdateTarget != "compatible" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	for _, row := range document.Packages {
		if row.Package == "Upgradable" && (row.UpdateTarget == nil || *row.UpdateTarget != "3.0.0-beta") {
			t.Fatalf("compatible counts prereleases: %s", rowSummary([]cliPackageRow{row}))
		}
	}
}

func TestListMetadataFailuresExitCodes(t *testing.T) {
	t.Run("every source failed", func(t *testing.T) {
		root, runner, feeds := assessmentWorkspace(t)
		feeds.failing["feed"] = true
		code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--outdated", "--format", "json")
		if code != ExitSource || !strings.Contains(stderr, "every source failed") || decodeList(t, stdout).Status != "failed" {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	})
	t.Run("some packages missing", func(t *testing.T) {
		root, runner, feeds := assessmentWorkspace(t)
		feeds.mu.Lock()
		delete(feeds.packages["feed"], "old")
		feeds.mu.Unlock()
		code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--deprecated", "--outdated", "--format", "json")
		document := decodeList(t, stdout)
		if code != ExitPartial || document.Status != "partial" || !strings.Contains(stderr, "metadata for Old unavailable") {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
		if got := rowSummary(document.Packages); strings.Contains(got, "Old") {
			t.Fatalf("a package without metadata was listed:\n%s", got)
		}
	})
	t.Run("default metadata timeout", func(t *testing.T) {
		root, runner, feeds := assessmentWorkspace(t)
		feeds.blocking["feed"] = true
		saved := defaultMetadataTimeout
		defaultMetadataTimeout = 50 * time.Millisecond
		defer func() { defaultMetadataTimeout = saved }()
		code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--outdated", "--format", "json")
		if code != ExitTimeout || !strings.Contains(stderr, "fetching package metadata") || decodeList(t, stdout).Status != "timed-out" {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	})
	t.Run("--timeout bounds the metadata fetch", func(t *testing.T) {
		root, runner, feeds := assessmentWorkspace(t)
		feeds.blocking["feed"] = true
		code, _, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--timeout", "100ms", "--outdated")
		if code != ExitTimeout || !strings.Contains(stderr, "timed out after 100ms") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	})
}

func TestListWithoutMetadataStaysOffline(t *testing.T) {
	root, runner, feeds := assessmentWorkspace(t)
	code, stdout, stderr := runCLIWith(t, &cliRuntime{runner: runner}, "list", "--project", root, "--format", "json")
	document := decodeList(t, stdout)
	if code != ExitSuccess || document.UpdateTarget != nil {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	for _, row := range document.Packages {
		if row.Vulnerable != nil || row.UpdateTarget != nil || row.Advisories == nil || row.DeprecationReasons == nil {
			t.Fatalf("unfetched row has assessment values: %+v", row)
		}
	}
	feeds.mu.Lock()
	defer feeds.mu.Unlock()
	if feeds.requests["feed"] != 0 {
		t.Fatal("plain list contacted a feed")
	}
}

// latestWorkspace has Behind (Example 1.0.0, net8.0), Ahead (Example 2.0.0,
// net8.0), and Pinned ([1.0.0]); the feed has 1.0.0, 2.0.0, a net9.0-only
// 3.0.0, and 4.0.0-beta.
func latestWorkspace(t *testing.T) (string, map[string]string) {
	t.Helper()
	feeds := newTestFeeds(t)
	feeds.add("feed", "Example", testLeaf{Version: "1.0.0"}, testLeaf{Version: "2.0.0"}, testLeaf{Version: "3.0.0", Frameworks: []string{"net9.0"}}, testLeaf{Version: "4.0.0-beta"})
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), feeds.config([]string{"feed"}, nil))
	projects := map[string]string{}
	for name, version := range map[string]string{"Behind": "1.0.0", "Ahead": "2.0.0", "Pinned": "[1.0.0]"} {
		projects[name] = writeCLIProject(t, root, name, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Example" Version="`+version+`" /></ItemGroup></Project>`)
	}
	return root, projects
}

func decodePlan(t *testing.T, stdout string) planDocument {
	t.Helper()
	var document planDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("%v\n%s", err, stdout)
	}
	return document
}

func projectVersion(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, _ := strings.Cut(string(data), `Version="`)
	version, _, _ := strings.Cut(rest, `"`)
	return version
}

func TestUpdateLatestPicksEachProjectsTarget(t *testing.T) {
	root, projects := latestWorkspace(t)
	code, stdout, stderr := runHermetic(t, "update", "Example", "--all", "--latest", "--project", root, "--format", "json")
	document := decodePlan(t, stdout)
	if code != ExitSuccess || document.Status != "changed" || document.UpdateTarget == nil || *document.UpdateTarget != "compatible-stable" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	operations := map[string]string{}
	for _, change := range document.Changes {
		operations[filepath.Base(change.Path)] = change.Operation + " " + change.Version
	}
	want := map[string]string{"Behind.csproj": "update version 2.0.0", "Ahead.csproj": "unchanged 2.0.0", "Pinned.csproj": "skip (pinned) [1.0.0]"}
	for path, operation := range want {
		if operations[path] != operation {
			t.Fatalf("changes = %v, want %v", operations, want)
		}
	}
	if projectVersion(t, projects["Behind"]) != "2.0.0" || projectVersion(t, projects["Pinned"]) != "[1.0.0]" {
		t.Fatal("files do not match the plan")
	}
}

func TestUpdateLatestSelectors(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--latest-stable"}, "3.0.0"},
		{[]string{"--latest", "--update-target", "stable"}, "3.0.0"},
		{[]string{"--latest-compatible"}, "2.0.0"},
		{[]string{"--latest", "--update-target", "compatible"}, "4.0.0-beta"},
	}
	for _, test := range cases {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			root, projects := latestWorkspace(t)
			args := append([]string{"update", "Example", "--file", projects["Behind"], "--project", root, "--dry-run", "--format", "json"}, test.args...)
			code, stdout, stderr := runHermetic(t, args...)
			document := decodePlan(t, stdout)
			if code != ExitSuccess || len(document.Changes) != 1 || document.Changes[0].Version != test.want {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
	}
	t.Run("nothing newer is unchanged", func(t *testing.T) {
		root, projects := latestWorkspace(t)
		code, stdout, stderr := runHermetic(t, "update", "Example", "--file", projects["Ahead"], "--project", root, "--latest", "--format", "json")
		document := decodePlan(t, stdout)
		if code != ExitSuccess || document.Status != "unchanged" || len(document.Changes) != 1 || document.Changes[0].Operation != unchangedOperation {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
	})
	t.Run("selectors are exclusive", func(t *testing.T) {
		root, projects := latestWorkspace(t)
		for _, args := range [][]string{
			{"--latest", "--version", "2.0.0"},
			{"--latest", "--latest-stable"},
			{"--latest-stable", "--update-target", "stable"},
			{"--version", "2.0.0", "--update-target", "stable"},
		} {
			code, _, stderr := runHermetic(t, append([]string{"update", "Example", "--file", projects["Behind"], "--project", root}, args...)...)
			if code != ExitUsage {
				t.Fatalf("%v: code=%d stderr=%s", args, code, stderr)
			}
		}
	})
	t.Run("package on no source", func(t *testing.T) {
		root, projects := latestWorkspace(t)
		mustWriteFile(t, projects["Behind"], `<Project><ItemGroup><PackageReference Include="Missing" Version="1.0.0" /></ItemGroup></Project>`)
		code, _, stderr := runHermetic(t, "update", "Missing", "--file", projects["Behind"], "--project", root, "--latest")
		if code != ExitSource || !strings.Contains(stderr, "no source supplied metadata") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	})
	t.Run("add still needs a version", func(t *testing.T) {
		root, projects := latestWorkspace(t)
		code, _, _ := runHermetic(t, "add", "Other", "--file", projects["Behind"], "--project", root, "--latest-stable")
		if code != ExitRefused {
			t.Fatalf("code=%d", code)
		}
	})
}
