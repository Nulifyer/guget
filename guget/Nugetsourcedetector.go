package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"logger"
)

const defaultNugetSource = "https://api.nuget.org/v3/index.json"

// ─────────────────────────────────────────────
// NuGet.config XML types
// ─────────────────────────────────────────────

type nugetConfig struct {
	XMLName              xml.Name        `xml:"configuration"`
	PackageSources       []packageSource `xml:"packageSources>add"`
	PackageSourcesClear  []struct{}      `xml:"packageSources>clear"`       // <clear /> stops inheritance
	DisabledSources      []packageSource `xml:"disabledPackageSources>add"`
	DisabledSourcesClear []struct{}      `xml:"disabledPackageSources>clear"`
}

type packageSource struct {
	Key   string `xml:"key,attr"`
	Value string `xml:"value,attr"`
}

// ─────────────────────────────────────────────
// NugetSource
// ─────────────────────────────────────────────

type NugetSource struct {
	Name     string
	URL      string
	Username string // from <packageSourceCredentials> (cleartext or DPAPI-decrypted)
	Password string
}

// ─────────────────────────────────────────────
// Detection
// ─────────────────────────────────────────────

// DetectSources finds all NuGet sources relevant to the given project directory.
// Sources are collected in priority order: solution/project → parents → user → machine.
// A <clear /> element inside <packageSources> stops inheritance: no lower-priority
// configs contribute sources beyond that point.
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

	// addConfig adds sources from a config file and returns true if a <clear/>
	// was found (meaning lower-priority configs should be ignored).
	addConfig := func(path string) bool {
		srcs, cleared := sourcesFromNugetConfig(path)
		for _, s := range srcs {
			add(s)
		}
		return cleared
	}

	// 1. Walk from projectDir up to root, collecting nuget.config + Directory.Build.props
	cleared := false
	dir := projectDir
	for {
		if addConfig(filepath.Join(dir, "nuget.config")) {
			cleared = true
		}
		if addConfig(filepath.Join(dir, "NuGet.Config")) {
			cleared = true
		}
		if addConfig(filepath.Join(dir, ".nuget", "NuGet.Config")) {
			cleared = true
		}
		for _, s := range sourcesFromBuildProps(filepath.Join(dir, "Directory.Build.props")) {
			add(s)
		}

		if cleared {
			break // <clear/> found — do not inherit from parent dirs, user, or machine
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached root
		}
		dir = parent
	}

	// 2. User-level config (skipped if any config declared <clear/>)
	if !cleared {
		if addConfig(userNugetConfigPath()) {
			cleared = true
		}
	}

	// 3. Machine-level config (skipped if any config declared <clear/>)
	if !cleared {
		addConfig(machineNugetConfigPath())
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

// sourcesFromNugetConfig parses a NuGet.Config file and returns its sources.
// The second return value is true when a <clear /> element was found inside
// <packageSources>, which means no lower-priority configs should contribute
// further sources (NuGet inheritance stops at that point).
func sourcesFromNugetConfig(path string) ([]NugetSource, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		logger.Trace("sourcesFromNugetConfig: skipping %q (%v)", path, err)
		return nil, false
	}
	logger.Trace("sourcesFromNugetConfig: reading %q", path)

	var cfg nugetConfig
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return nil, false
	}

	cleared := len(cfg.PackageSourcesClear) > 0

	// Build the disabled-sources set from this file only. Inherited disabled
	// sources are not tracked here; each config file is parsed independently
	// and the caller controls inheritance via the cleared flag.
	disabled := NewSet[string]()
	for _, d := range cfg.DisabledSources {
		disabled.Add(strings.ToLower(d.Key))
	}

	// Parse credentials keyed by normalised source name
	creds := parseCredentials(data)
	logger.Trace("sourcesFromNugetConfig: %q — %d credential block(s), cleared=%v", path, len(creds), cleared)

	var sources []NugetSource
	for _, ps := range cfg.PackageSources {
		if disabled.Contains(strings.ToLower(ps.Key)) {
			logger.Trace("sourcesFromNugetConfig: [%s] skipped (disabled)", ps.Key)
			continue
		}
		// Only include http/https sources (skip local folder paths)
		if strings.HasPrefix(ps.Value, "http://") || strings.HasPrefix(ps.Value, "https://") {
			s := NugetSource{Name: ps.Key, URL: ps.Value}
			if c, ok := creds[normalizeCredentialKey(ps.Key)]; ok {
				s.Username = c.Username
				s.Password = c.Password
				logger.Trace("sourcesFromNugetConfig: [%s] credentials matched (username=%q, password=%d chars)", ps.Key, c.Username, len(c.Password))
			} else {
				logger.Trace("sourcesFromNugetConfig: [%s] no credentials found (lookup key=%q)", ps.Key, normalizeCredentialKey(ps.Key))
			}
			sources = append(sources, s)
		} else {
			logger.Trace("sourcesFromNugetConfig: [%s] skipped (not http/https: %q)", ps.Key, ps.Value)
		}
	}
	return sources, cleared
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
