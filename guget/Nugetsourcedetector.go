package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const defaultNugetSource = "https://api.nuget.org/v3/index.json"

// ─────────────────────────────────────────────
// NuGet.config XML types
// ─────────────────────────────────────────────

type nugetConfig struct {
	XMLName         xml.Name        `xml:"configuration"`
	PackageSources  []packageSource `xml:"packageSources>add"`
	DisabledSources []packageSource `xml:"disabledPackageSources>add"`
}

type packageSource struct {
	Key   string `xml:"key,attr"`
	Value string `xml:"value,attr"`
}

// ─────────────────────────────────────────────
// NugetSource
// ─────────────────────────────────────────────

type NugetSource struct {
	Name string
	URL  string
}

// ─────────────────────────────────────────────
// Detection
// ─────────────────────────────────────────────

// DetectSources finds all NuGet sources relevant to the given project directory.
// Sources are collected in priority order: solution/project → parents → user → machine.
// Duplicates (by URL) are removed. Falls back to nuget.org if nothing is found.
func DetectSources(projectDir string) []NugetSource {
	seen := NewSet[string]()
	var sources []NugetSource

	add := func(s NugetSource) {
		url := strings.TrimRight(s.URL, "/")
		if !seen.Contains(url) {
			seen.Add(url)
			sources = append(sources, s)
		}
	}

	// 1. Walk from projectDir up to root, collecting nuget.config + Directory.Build.props
	dir := projectDir
	for {
		for _, s := range sourcesFromNugetConfig(filepath.Join(dir, "nuget.config")) {
			add(s)
		}
		for _, s := range sourcesFromNugetConfig(filepath.Join(dir, "NuGet.Config")) {
			add(s)
		}
		for _, s := range sourcesFromNugetConfig(filepath.Join(dir, ".nuget", "NuGet.Config")) {
			add(s)
		}
		for _, s := range sourcesFromBuildProps(filepath.Join(dir, "Directory.Build.props")) {
			add(s)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached root
		}
		dir = parent
	}

	// 2. User-level config
	for _, s := range sourcesFromNugetConfig(userNugetConfigPath()) {
		add(s)
	}

	// 3. Machine-level config
	for _, s := range sourcesFromNugetConfig(machineNugetConfigPath()) {
		add(s)
	}

	// 4. Fallback to nuget.org
	if len(sources) == 0 {
		add(NugetSource{Name: "nuget.org", URL: defaultNugetSource})
	}

	return sources
}

// ─────────────────────────────────────────────
// Parsers
// ─────────────────────────────────────────────

func sourcesFromNugetConfig(path string) []NugetSource {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var cfg nugetConfig
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return nil
	}

	// Build disabled set for fast lookup
	disabled := NewSet[string]()
	for _, d := range cfg.DisabledSources {
		disabled.Add(strings.ToLower(d.Key))
	}

	var sources []NugetSource
	for _, ps := range cfg.PackageSources {
		if disabled.Contains(strings.ToLower(ps.Key)) {
			continue
		}
		// Only include http/https sources (skip local folder paths)
		if strings.HasPrefix(ps.Value, "http://") || strings.HasPrefix(ps.Value, "https://") {
			sources = append(sources, NugetSource{
				Name: ps.Key,
				URL:  ps.Value,
			})
		}
	}
	return sources
}

func sourcesFromBuildProps(path string) []NugetSource {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// Parse RestoreSources property from MSBuild XML
	type propertyGroup struct {
		RestoreSources string `xml:"RestoreSources"`
	}
	type msbuild struct {
		PropertyGroups []propertyGroup `xml:"PropertyGroup"`
	}

	var props msbuild
	if err := xml.Unmarshal(data, &props); err != nil {
		return nil
	}

	var sources []NugetSource
	for _, pg := range props.PropertyGroups {
		if pg.RestoreSources == "" {
			continue
		}
		for _, raw := range strings.Split(pg.RestoreSources, ";") {
			raw = strings.TrimSpace(raw)
			if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
				sources = append(sources, NugetSource{
					Name: raw,
					URL:  raw,
				})
			}
		}
	}
	return sources
}

// ─────────────────────────────────────────────
// OS-specific config paths
// ─────────────────────────────────────────────

func userNugetConfigPath() string {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		return filepath.Join(appdata, "NuGet", "NuGet.Config")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nuget", "NuGet", "NuGet.Config")
}

func machineNugetConfigPath() string {
	if runtime.GOOS == "windows" {
		programdata := os.Getenv("ProgramData")
		return filepath.Join(programdata, "NuGet", "NuGet.Config")
	}
	return "/etc/opt/nuget/NuGet.Config"
}
