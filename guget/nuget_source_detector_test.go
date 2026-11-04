package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectSourcesRejectsMalformedConfigurations(t *testing.T) {
	for index, content := range []string{
		"",
		`<configuration><packageSources><clear/><add key="private" value="https://private.example/index.json"/>`,
		`<configuration/><configuration/>`,
		`<configuration/>credential-secret`,
		`<credential-secret/>`,
	} {
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "NuGet.Config")
			mustWriteFile(t, path, content)
			detected := DetectSources(root)
			if len(detected.Errors) == 0 || len(detected.Sources) != 0 || detected.Mapping != nil {
				t.Fatalf("malformed config accepted: %+v", detected)
			}
			diagnostic := detected.Errors[0].Error()
			if !strings.Contains(diagnostic, path) || strings.Contains(diagnostic, "credential-secret") || strings.Contains(diagnostic, "private.example") {
				t.Fatalf("unsafe or unhelpful diagnostic: %s", diagnostic)
			}
			if digest := detected.InputRevisions[path]; digest != sha256.Sum256([]byte(content)) {
				t.Fatal("configuration revision does not match consumed bytes")
			}
		})
	}
}

func TestDetectSourcesRetainsConsumedSourceRevisions(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "NuGet.Config")
	props := filepath.Join(root, "Directory.Build.props")
	configData := `<configuration><packageSources><clear/><add key="private" value="https://private.example/index.json"/></packageSources><packageSourceMapping><packageSource key="private"><package pattern="Example.*"/></packageSource></packageSourceMapping></configuration>`
	propsData := `<Project><PropertyGroup><RestoreSources>https://second.example/index.json</RestoreSources></PropertyGroup></Project>`
	mustWriteFile(t, config, configData)
	mustWriteFile(t, props, propsData)
	detected := DetectSources(root)
	if len(detected.Errors) != 0 || len(detected.Sources) != 2 || !detected.Mapping.IsConfigured() {
		t.Fatalf("detected=%+v", detected)
	}
	for path, content := range map[string]string{config: configData, props: propsData} {
		if digest := detected.InputRevisions[path]; digest != sha256.Sum256([]byte(content)) {
			t.Fatalf("incorrect consumed revision for %s", path)
		}
	}
	// A later save must not retroactively change the revision attached to the
	// source configuration which was consumed for this snapshot.
	mustWriteFile(t, config, `<configuration/>`)
	if detected.InputRevisions[config] != sha256.Sum256([]byte(configData)) {
		t.Fatal("returned revision changed after subsequent editor save")
	}
}

func TestDetectSourcesMalformedSaveThenRepair(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "NuGet.Config")
	valid := `<configuration><packageSources><clear/><add key="local" value="./packages"/></packageSources></configuration>`
	mustWriteFile(t, path, valid)
	first := DetectSources(root)
	if len(first.Errors) != 0 || len(first.Sources) != 1 || first.Sources[0].Name != "local" {
		t.Fatalf("initial config=%+v", first)
	}
	mustWriteFile(t, path, `<configuration><packageSources>`)
	broken := DetectSources(root)
	if len(broken.Errors) == 0 || len(broken.Sources) != 0 {
		t.Fatal("truncated save produced usable or fallback sources")
	}
	mustWriteFile(t, path, valid)
	repaired := DetectSources(root)
	if len(repaired.Errors) != 0 || len(repaired.Sources) != 1 || repaired.Sources[0] != first.Sources[0] {
		t.Fatalf("repaired config=%+v", repaired)
	}
}

func TestDetectSourcesReportsNonRegularConfiguration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "NuGet.Config")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	detected := DetectSources(root)
	if len(detected.Errors) == 0 || !strings.Contains(detected.Errors[0].Error(), path) || len(detected.Sources) != 0 {
		t.Fatalf("detected=%+v", detected)
	}
}

func TestDetectSourcesIgnoresNormalMissingConfigurations(t *testing.T) {
	root := t.TempDir()
	// The explicit clear isolates user/machine settings; absent case variants,
	// .nuget configuration, and build props remain normal optional inputs.
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/></packageSources></configuration>`)
	detected := DetectSources(root)
	if len(detected.Errors) != 0 {
		t.Fatalf("missing optional configurations reported errors: %v", detected.Errors)
	}
}

func TestDetectSourcesReadsDistinctCaseConfigurationsOnCaseSensitiveFilesystems(t *testing.T) {
	root := t.TempDir()
	lower := filepath.Join(root, "nuget.config")
	upper := filepath.Join(root, "NuGet.Config")
	mustWriteFile(t, lower, `<configuration><packageSources><clear/></packageSources></configuration>`)
	lowerInfo, _ := os.Stat(lower)
	if info, err := os.Stat(upper); err == nil && os.SameFile(lowerInfo, info) {
		t.Skip("case-insensitive filesystem")
	}
	mustWriteFile(t, upper, `<configuration><packageSources>`)
	detected := DetectSources(root)
	if len(detected.Errors) == 0 {
		t.Fatal("case-based deduplication hid a distinct malformed configuration")
	}
}

func TestDetectSourcesRejectsMalformedBuildPropsSourceSettings(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="valid" value="https://feed.example/index.json"/></packageSources></configuration>`)
	path := filepath.Join(root, "Directory.Build.props")
	content := `<Project><PropertyGroup><RestoreSources>https://secret-feed.example/index.json</RestoreSources>`
	mustWriteFile(t, path, content)
	detected := DetectSources(root)
	if len(detected.Errors) == 0 || len(detected.Sources) != 0 || !strings.Contains(detected.Errors[0].Error(), path) || strings.Contains(detected.Errors[0].Error(), "secret-feed") {
		t.Fatalf("errors=%v sources=%v", detected.Errors, detected.Sources)
	}
	if detected.InputRevisions[path] != sha256.Sum256([]byte(content)) {
		t.Fatal("malformed source props revision was not captured")
	}
}

func TestDetectSourcesReportsUnreadableConfiguration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "NuGet.Config")
	mustWriteFile(t, path, `<configuration/>`)
	if err := os.Chmod(path, 0); err != nil {
		t.Skipf("permissions unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("current account can read permission-restricted files")
	}
	detected := DetectSources(root)
	if len(detected.Errors) == 0 || len(detected.Sources) != 0 || !strings.Contains(detected.Errors[0].Error(), path) {
		t.Fatalf("errors=%v sources=%v", detected.Errors, detected.Sources)
	}
}

func TestDetectSourcesAcceptsUTF8BOMAndTrailingComments(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "NuGet.Config")
	content := "\xef\xbb\xbf" + `<?xml version="1.0" encoding="utf-8"?><configuration><packageSources><clear/><add key="local" value="./packages"/></packageSources></configuration><!-- saved by editor -->`
	mustWriteFile(t, path, content)
	detected := DetectSources(root)
	if len(detected.Errors) != 0 || len(detected.Sources) != 1 || detected.Sources[0].Name != "local" {
		t.Fatalf("detected=%+v", detected)
	}
}

func TestDetectSourcesHonorsExplicitlyEmptySourceClear(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/></packageSources></configuration>`)
	detected := DetectSources(root)
	if len(detected.Errors) != 0 || len(detected.Sources) != 0 {
		t.Fatalf("explicitly cleared sources must stay empty: %+v", detected)
	}
}

func TestDetectSourcesDefaultsOnlyWithoutConfiguredSourcesOrClear(t *testing.T) {
	root := t.TempDir()
	isolatedHome := t.TempDir()
	t.Setenv("HOME", isolatedHome)
	t.Setenv("APPDATA", isolatedHome)
	t.Setenv("ProgramData", isolatedHome)
	for dir := root; ; dir = filepath.Dir(dir) {
		for _, name := range []string{"nuget.config", "NuGet.Config", filepath.Join(".nuget", "NuGet.Config"), "Directory.Build.props"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				t.Skip("host ancestor configuration prevents isolated default-source test")
			}
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if _, err := os.Stat(machineNugetConfigPath()); err == nil {
		t.Skip("host machine configuration prevents isolated default-source test")
	}
	detected := DetectSources(root)
	if len(detected.Errors) != 0 || len(detected.Sources) != 1 || detected.Sources[0].URL != defaultNugetSource {
		t.Fatalf("default source missing: %+v", detected)
	}
}
