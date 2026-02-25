package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		packageID string
		pattern   string
		want      bool
	}{
		{"Newtonsoft.Json", "*", true},
		{"Serilog", "*", true},
		{"", "*", true},

		{"Newtonsoft.Json", "Newtonsoft.*", true},
		{"Newtonsoft.Json.Bson", "Newtonsoft.*", true},
		{"newtonsoft.json", "Newtonsoft.*", true},
		{"Newtonsoft.Json", "newtonsoft.*", true},
		{"Other.Package", "Newtonsoft.*", false},
		{"Serilog", "Serilog.*", false},
		{"Serilog.Sinks.Console", "Serilog.*", true},
		{"SerilogExtra", "Serilog.*", false},
		{"Microsoft.Extensions.Logging", "Microsoft.*", true},
		{"Microsoft.Extensions.Logging", "Microsoft.Extensions.*", true},

		{"Newtonsoft.Json", "Newtonsoft.Json", true},
		{"newtonsoft.json", "Newtonsoft.Json", true},
		{"Newtonsoft.Json", "Newtonsoft.Xml", false},
		{"Serilog", "Serilog", true},

		{"Something", "", false},
	}
	for _, tt := range tests {
		got := matchPattern(tt.packageID, tt.pattern)
		if got != tt.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.packageID, tt.pattern, got, tt.want)
		}
	}
}

func TestIsConfigured(t *testing.T) {
	var nilMapping *PackageSourceMapping
	if nilMapping.IsConfigured() {
		t.Fatal("nil mapping should not be configured")
	}

	empty := &PackageSourceMapping{Entries: map[string][]string{}}
	if empty.IsConfigured() {
		t.Fatal("empty mapping should not be configured")
	}

	withEntry := &PackageSourceMapping{Entries: map[string][]string{"nuget.org": {"*"}}}
	if !withEntry.IsConfigured() {
		t.Fatal("mapping with entries should be configured")
	}
}

func TestSourcesForPackage(t *testing.T) {
	m := &PackageSourceMapping{
		Entries: map[string][]string{
			"nuget.org":     {"*"},
			"custom_github": {"redacted.*"},
			"internal_feed": {"mycompany.*", "mycompany.core"},
		},
	}

	assertSources := func(packageID string, want []string) {
		t.Helper()
		got := m.SourcesForPackage(packageID)
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("SourcesForPackage(%q): got %v, want %v", packageID, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("SourcesForPackage(%q): got %v, want %v", packageID, got, want)
			}
		}
	}

	assertSources("Newtonsoft.Json", []string{"nuget.org"})
	assertSources("Redacted.Lib", []string{"custom_github", "nuget.org"})
	assertSources("MyCompany.Core", []string{"internal_feed", "nuget.org"})
	assertSources("MyCompany.Utils", []string{"internal_feed", "nuget.org"})
}

func TestSourcesForPackage_NotConfigured(t *testing.T) {
	var nilMapping *PackageSourceMapping
	got := nilMapping.SourcesForPackage("SomePackage")
	if got != nil {
		t.Fatalf("expected nil for unconfigured mapping, got %v", got)
	}
}

func TestSourcesForPackage_OverlappingPatterns(t *testing.T) {
	m := &PackageSourceMapping{
		Entries: map[string][]string{
			"nuget.org":     {"*"},
			"dotnet-public": {"microsoft.*", "system.*"},
			"dotnet9":       {"microsoft.extensions.*"},
		},
	}

	assert := func(packageID string, want []string) {
		t.Helper()
		got := m.SourcesForPackage(packageID)
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Errorf("SourcesForPackage(%q) = %v, want %v", packageID, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("SourcesForPackage(%q) = %v, want %v", packageID, got, want)
				return
			}
		}
	}

	assert("Microsoft.Extensions.Logging", []string{"dotnet-public", "dotnet9", "nuget.org"})
	assert("Microsoft.CodeAnalysis", []string{"dotnet-public", "nuget.org"})
	assert("Newtonsoft.Json", []string{"nuget.org"})
	assert("System.Text.Json", []string{"dotnet-public", "nuget.org"})
	assert("microsoft.extensions.logging", []string{"dotnet-public", "dotnet9", "nuget.org"})
}

func TestPackageSourceMappingXMLParsing(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
    <add key="custom_github" value="https://nuget.pkg.github.com/test/index.json" />
  </packageSources>
  <packageSourceMapping>
    <packageSource key="nuget.org">
      <package pattern="*" />
    </packageSource>
    <packageSource key="custom_github">
      <package pattern="Redacted.*" />
      <package pattern="Internal.Core" />
    </packageSource>
  </packageSourceMapping>
</configuration>`

	dir := t.TempDir()
	path := filepath.Join(dir, "nuget.config")
	if err := os.WriteFile(path, []byte(xmlData), 0644); err != nil {
		t.Fatal(err)
	}

	sources, _, mr := sourcesFromNugetConfig(path)
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
	if mr == nil {
		t.Fatal("expected non-nil mapping result")
	}
	if mr.cleared {
		t.Fatal("expected cleared=false")
	}
	if len(mr.entries) != 2 {
		t.Fatalf("expected 2 mapping entries, got %d", len(mr.entries))
	}

	nugetPatterns := mr.entries["nuget.org"]
	if len(nugetPatterns) != 1 || nugetPatterns[0] != "*" {
		t.Fatalf("expected nuget.org patterns [*], got %v", nugetPatterns)
	}

	ghPatterns := mr.entries["custom_github"]
	if len(ghPatterns) != 2 {
		t.Fatalf("expected 2 custom_github patterns, got %v", ghPatterns)
	}
	if ghPatterns[0] != "redacted.*" || ghPatterns[1] != "internal.core" {
		t.Fatalf("unexpected custom_github patterns: %v", ghPatterns)
	}
}

func TestPackageSourceMappingXML_Clear(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSourceMapping>
    <clear />
    <packageSource key="only-source">
      <package pattern="*" />
    </packageSource>
  </packageSourceMapping>
</configuration>`

	dir := t.TempDir()
	path := filepath.Join(dir, "nuget.config")
	if err := os.WriteFile(path, []byte(xmlData), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, mr := sourcesFromNugetConfig(path)
	if mr == nil {
		t.Fatal("expected non-nil mapping result")
	}
	if !mr.cleared {
		t.Fatal("expected cleared=true")
	}
	if len(mr.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(mr.entries))
	}
}

func TestPackageSourceMappingXML_Empty(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
  </packageSources>
</configuration>`

	dir := t.TempDir()
	path := filepath.Join(dir, "nuget.config")
	if err := os.WriteFile(path, []byte(xmlData), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, mr := sourcesFromNugetConfig(path)
	if mr != nil {
		t.Fatalf("expected nil mapping result for config without mapping, got %+v", mr)
	}
}

func TestDetectSources_WithMapping(t *testing.T) {
	td := testDataDir(t)
	detected := DetectSources(td)

	if len(detected.Sources) == 0 {
		t.Fatal("expected at least one source")
	}

	m := detected.Mapping
	if !m.IsConfigured() {
		t.Fatal("expected mapping to be configured")
	}
	if len(m.Entries) != 4 {
		t.Fatalf("expected 4 mapping entries, got %d: %v", len(m.Entries), m.Entries)
	}

	nugetPatterns := m.Entries["nuget.org"]
	if len(nugetPatterns) != 1 || nugetPatterns[0] != "*" {
		t.Fatalf("expected nuget.org patterns [*], got %v", nugetPatterns)
	}

	dpPatterns := m.Entries["dotnet-public"]
	if len(dpPatterns) != 2 {
		t.Fatalf("expected 2 dotnet-public patterns, got %v", dpPatterns)
	}

	d9Patterns := m.Entries["dotnet9"]
	if len(d9Patterns) != 1 || d9Patterns[0] != "microsoft.extensions.*" {
		t.Fatalf("expected dotnet9 patterns [microsoft.extensions.*], got %v", d9Patterns)
	}

	ghPatterns := m.Entries["github-nulifyer"]
	if len(ghPatterns) != 1 || ghPatterns[0] != "guget.*" {
		t.Fatalf("expected github-nulifyer patterns [guget.*], got %v", ghPatterns)
	}
}

func TestDetectSources_MappingFiltersByPattern(t *testing.T) {
	td := testDataDir(t)
	detected := DetectSources(td)
	m := detected.Mapping

	if !m.IsConfigured() {
		t.Fatal("expected mapping to be configured")
	}

	assertMappedSources := func(packageID string, want []string) {
		t.Helper()
		got := m.SourcesForPackage(packageID)
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("SourcesForPackage(%q): got %v, want %v", packageID, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("SourcesForPackage(%q): got %v, want %v", packageID, got, want)
			}
		}
	}

	assertMappedSources("Serilog", []string{"nuget.org"})
	assertMappedSources("Microsoft.Extensions.Logging", []string{"dotnet-public", "dotnet9", "nuget.org"})
	assertMappedSources("System.Text.Json", []string{"dotnet-public", "nuget.org"})
	assertMappedSources("Microsoft.CodeAnalysis", []string{"dotnet-public", "nuget.org"})
	assertMappedSources("Guget.TestPackage", []string{"github-nulifyer", "nuget.org"})
}

func TestPackageSourceMappingXML_Unmarshal(t *testing.T) {
	data := []byte(`<packageSourceMapping>
  <packageSource key="nuget.org">
    <package pattern="*" />
  </packageSource>
  <packageSource key="private">
    <package pattern="MyCompany.*" />
    <package pattern="MyCompany.Core" />
  </packageSource>
</packageSourceMapping>`)

	var m packageSourceMappingXML
	if err := xml.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(m.Sources))
	}
	if m.Sources[0].Key != "nuget.org" {
		t.Fatalf("expected first source key nuget.org, got %s", m.Sources[0].Key)
	}
	if len(m.Sources[1].Patterns) != 2 {
		t.Fatalf("expected 2 patterns for private, got %d", len(m.Sources[1].Patterns))
	}
}
