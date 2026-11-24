package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sharedCentralWorkspace is a root Directory.Packages.props pinning
// Contoso.Lib 1.0.0 for two projects with their own NuGet configuration:
// Secure uses the private feed (with mapping: private and public, mapping
// Contoso.* to private), Other only the public one. Private has 1.0.0, 1.1.0,
// and 1.2.0; public has 1.0.0, 99.0.0, and 1.1.0 when publicHasShared. New.Lib
// 2.0.0 is only on private. With sameScope, both projects use secure's
// configuration from the root instead.
func sharedCentralWorkspace(t *testing.T, mapping, publicHasShared, sameScope bool) (root, secure, other string, feeds *testFeeds) {
	t.Helper()
	isolateNuGetUser(t)
	feeds = newTestFeeds(t)
	feeds.add("private", "Contoso.Lib", testLeaf{Version: "1.0.0"}, testLeaf{Version: "1.1.0"}, testLeaf{Version: "1.2.0"})
	feeds.add("private", "New.Lib", testLeaf{Version: "2.0.0"})
	feeds.add("public", "Contoso.Lib", testLeaf{Version: "1.0.0"}, testLeaf{Version: "99.0.0"})
	if publicHasShared {
		feeds.add("public", "Contoso.Lib", testLeaf{Version: "1.1.0"})
	}
	root = t.TempDir()
	mustWriteFile(t, filepath.Join(root, "Directory.Packages.props"), `<Project>`+testCPMOn+`<ItemGroup><PackageVersion Include="Contoso.Lib" Version="1.0.0" /></ItemGroup></Project>`)
	secureConfig := feeds.config([]string{"private"}, nil)
	if mapping {
		secureConfig = feeds.config([]string{"private", "public"}, map[string][]string{"private": {"Contoso.*", "New.*"}, "public": {"*"}})
	}
	if sameScope {
		mustWriteFile(t, filepath.Join(root, "nuget.config"), secureConfig)
	} else {
		mustWriteFile(t, filepath.Join(root, "secure", "nuget.config"), secureConfig)
		mustWriteFile(t, filepath.Join(root, "other", "nuget.config"), feeds.config([]string{"public"}, nil))
	}
	project := `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Contoso.Lib" /></ItemGroup></Project>`
	secure = writeCLIProject(t, filepath.Join(root, "secure"), "Secure", project)
	other = writeCLIProject(t, filepath.Join(root, "other"), "Other", project)
	return root, secure, other, feeds
}

func centralProps(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "Directory.Packages.props"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestUpdateLatestOfASharedOwnerUsesVersionsEveryConsumerCanUse(t *testing.T) {
	forEachSourceScoping(t, func(t *testing.T, mapping bool) {
		root, secure, _, _ := sharedCentralWorkspace(t, mapping, true, false)
		for _, scope := range [][]string{{"--file", secure}, {"--all"}} {
			args := append([]string{"update", "Contoso.Lib", "--latest", "--dry-run", "--project", root, "--format", "json"}, scope...)
			code, stdout, stderr := runHermetic(t, args...)
			if code != ExitSuccess {
				t.Fatalf("%v: code=%d stderr=%s", scope, code, stderr)
			}
			for _, change := range decodePlan(t, stdout).Changes {
				if change.Version != "1.1.0" {
					t.Fatalf("%v: plan = %+v; Directory.Packages.props is shared with Other, which cannot get 1.2.0 or 99.0.0", scope, change)
				}
			}
		}

		// With no newer common version the owner stays unchanged, and a
		// warning names the projects sharing it.
		root, secure, _, _ = sharedCentralWorkspace(t, mapping, false, false)
		code, stdout, stderr := runHermetic(t, "update", "Contoso.Lib", "--latest", "--file", secure, "--project", root, "--format", "json")
		plan := decodePlan(t, stdout)
		if code != ExitSuccess || len(plan.Changes) != 1 || plan.Changes[0].Operation != unchangedOperation || !strings.Contains(centralProps(t, root), `Version="1.0.0"`) {
			t.Fatalf("code=%d plan=%+v stderr=%s", code, plan.Changes, stderr)
		}
		if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "Secure.csproj") || !strings.Contains(plan.Warnings[0], "Other.csproj") || !strings.Contains(stderr, "left unchanged") {
			t.Fatalf("warnings = %v", plan.Warnings)
		}
	})
}

func TestSharedOwnerWithAFailedConsumerSourceIsLeftUnchanged(t *testing.T) {
	root, secure, _, feeds := sharedCentralWorkspace(t, false, true, false)
	feeds.failing["public"] = true
	code, stdout, stderr := runHermetic(t, "update", "Contoso.Lib", "--latest", "--file", secure, "--project", root, "--format", "json")
	plan := decodePlan(t, stdout)
	if code != ExitPartial || len(plan.Changes) != 1 || plan.Changes[0].Operation != unchangedOperation || !strings.Contains(stderr, "Other.csproj takes its version from there too") {
		t.Fatalf("--latest: code=%d plan=%+v stderr=%s", code, plan.Changes, stderr)
	}
	code, _, stderr = runHermetic(t, "update", "Contoso.Lib", "--version", "1.1.0", "--file", secure, "--project", root, "--format", "json")
	if code != ExitSource || !strings.Contains(stderr, "cannot check") || !strings.Contains(centralProps(t, root), `Version="1.0.0"`) {
		t.Fatalf("--version: code=%d stderr=%s", code, stderr)
	}
}

func TestUpdateVersionOfASharedOwnerRefusesAVersionAConsumerCannotGet(t *testing.T) {
	forEachSourceScoping(t, func(t *testing.T, mapping bool) {
		root, secure, _, _ := sharedCentralWorkspace(t, mapping, true, false)
		before := centralProps(t, root)
		code, _, stderr := runHermetic(t, "update", "Contoso.Lib", "--version", "1.2.0", "--file", secure, "--project", root, "--format", "json")
		if code != ExitRefused || !strings.Contains(stderr, "not available from the NuGet sources of Other.csproj") || centralProps(t, root) != before {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
		code, stdout, stderr := runHermetic(t, "update", "Contoso.Lib", "--version", "1.1.0", "--file", secure, "--dry-run", "--project", root, "--format", "json")
		if plan := decodePlan(t, stdout); code != ExitSuccess || len(plan.Changes) != 1 || plan.Changes[0].Version != "1.1.0" {
			t.Fatalf("available version refused: code=%d stderr=%s", code, stderr)
		}
	})
}

func TestUpdateVersionInOneScopeNeedsNoMetadata(t *testing.T) {
	root, secure, _, feeds := sharedCentralWorkspace(t, false, false, true)
	code, _, stderr := runHermetic(t, "update", "Contoso.Lib", "--version", "5.0.0", "--file", secure, "--dry-run", "--project", root, "--format", "json")
	feeds.mu.Lock()
	requests := len(feeds.requests)
	feeds.mu.Unlock()
	if code != ExitSuccess || requests != 0 {
		t.Fatalf("single-scope --version: code=%d requests=%d stderr=%s", code, requests, stderr)
	}
}

func TestAddToASharedCentralFileRefusesAVersionATargetCannotGet(t *testing.T) {
	forEachSourceScoping(t, func(t *testing.T, mapping bool) {
		root, secure, other, _ := sharedCentralWorkspace(t, mapping, true, false)
		before := centralProps(t, root)
		code, _, stderr := runHermetic(t, "add", "New.Lib", "--version", "2.0.0", "--file", secure, "--file", other, "--project", root, "--format", "json")
		if code != ExitRefused || !strings.Contains(stderr, "New.Lib 2.0.0 is not available from the NuGet sources of Other.csproj") || centralProps(t, root) != before {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
		// One target is one scope: the add proceeds as before.
		code, _, stderr = runHermetic(t, "add", "New.Lib", "--version", "2.0.0", "--file", secure, "--dry-run", "--project", root, "--format", "json")
		if code != ExitSuccess {
			t.Fatalf("single-project add: code=%d stderr=%s", code, stderr)
		}
	})
}

func TestTUIUpdateOfASharedOwnerRefusesAVersionAConsumerCannotGet(t *testing.T) {
	forEachSourceScoping(t, func(t *testing.T, mapping bool) {
		root, secure, _, _ := sharedCentralWorkspace(t, mapping, true, false)
		m := fetchTUIMetadata(t, root)
		if got := resultVersions(m.ctx.Results["Contoso.Lib"]); got != "1.1.0 1.0.0" {
			t.Fatalf("shared versions = %s", got)
		}
		m.selectProjectByPath(secure)
		before := centralProps(t, root)
		m.applyVersion("Contoso.Lib", "1.2.0", m.selectedProject())
		if !m.ctx.StatusIsErr || !strings.HasPrefix(m.ctx.StatusLine, "▲ Not updated") || !strings.Contains(m.ctx.StatusLine, "Contoso.Lib 1.2.0 is not available") || m.ctx.Saving {
			t.Fatalf("status=%q saving=%v", m.ctx.StatusLine, m.ctx.Saving)
		}
		if centralProps(t, root) != before {
			t.Fatal("refused update wrote the shared owner")
		}
		if problem := m.sharedOwnerProblem("Contoso.Lib", "1.1.0", declaredPackageTargets([]*ParsedProject{m.selectedProject()}, "Contoso.Lib")); problem != "" {
			t.Fatalf("common version refused: %s", problem)
		}
	})
}
