package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"runtime"
	"strings"

)

const defaultNugetSource = "https://api.nuget.org/v3/index.json"

type nugetConfig struct {
	XMLName              xml.Name                 `xml:"configuration"`
	PackageSources       []packageSource          `xml:"packageSources>add"`
	PackageSourcesClear  []struct{}               `xml:"packageSources>clear"`       // <clear /> stops inheritance
	DisabledSources      []packageSource          `xml:"disabledPackageSources>add"`
	DisabledSourcesClear []struct{}               `xml:"disabledPackageSources>clear"`
	SourceMapping        *packageSourceMappingXML `xml:"packageSourceMapping"`
}

type packageSource struct {
	Key   string `xml:"key,attr"`
	Value string `xml:"value,attr"`
}

type NugetSource struct {
	Name     string
	URL      string
	Username string // from <packageSourceCredentials> (cleartext or DPAPI-decrypted)
	Password string
}

// DetectedConfig holds everything discovered from the nuget.config hierarchy.
type DetectedConfig struct {
	Sources []NugetSource
	Mapping *PackageSourceMapping
}

// parsedMappingResult is an internal type returned by sourcesFromNugetConfig
// for a single config file's <packageSourceMapping> section.
type parsedMappingResult struct {
	entries map[string][]string // source key → lowercase patterns
	cleared bool               // <clear/> inside <packageSourceMapping>
}

// DetectSources walks from projectDir up to root collecting NuGet sources and
// package-source mapping rules. <clear/> stops inheritance. Falls back to nuget.org.
func DetectSources(projectDir string) DetectedConfig {
	seen := NewSet[string]()
	var sources []NugetSource
	mapping := &PackageSourceMapping{Entries: make(map[string][]string)}
	mappingCleared := false

	add := func(s NugetSource) {
		url := strings.TrimRight(s.URL, "/")
		if !seen.Contains(url) {
			seen.Add(url)
			sources = append(sources, s)
		}
	}

	// addConfig adds sources and mapping rules from a config file.
	// Returns true if a <clear/> was found in <packageSources>.
	// Deduplicates by resolved path so case-insensitive filesystems
	// (Windows) don't parse the same file twice.
	seenConfigs := NewSet[string]()
	addConfig := func(path string) bool {
		resolved, err := filepath.Abs(path)
		if err == nil {
			resolved = strings.ToLower(resolved)
			if seenConfigs.Contains(resolved) {
				return false
			}
			seenConfigs.Add(resolved)
		}
		srcs, cleared, mr := sourcesFromNugetConfig(path)
		for _, s := range srcs {
			add(s)
		}
		if !mappingCleared && mr != nil {
			if mr.cleared {
				mapping = &PackageSourceMapping{Entries: make(map[string][]string)}
				mappingCleared = true
			}
			for k, v := range mr.entries {
				mapping.Entries[k] = append(mapping.Entries[k], v...)
			}
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

	// Nil out empty mapping so IsConfigured() returns false.
	if len(mapping.Entries) == 0 {
		mapping = nil
	}

	return DetectedConfig{Sources: sources, Mapping: mapping}
}

// sourcesFromNugetConfig parses a single NuGet.Config file.
func sourcesFromNugetConfig(path string) ([]NugetSource, bool, *parsedMappingResult) {
	data, err := os.ReadFile(path)
	if err != nil {
		logTrace("sourcesFromNugetConfig: skipping %q (%v)", path, err)
		return nil, false, nil
	}
	logTrace("sourcesFromNugetConfig: reading %q", path)

	var cfg nugetConfig
	if err := xml.Unmarshal(data, &cfg); err != nil {
		return nil, false, nil
	}

	cleared := len(cfg.PackageSourcesClear) > 0

	// Build disabled-sources set from this file only.
	disabled := NewSet[string]()
	for _, d := range cfg.DisabledSources {
		disabled.Add(strings.ToLower(d.Key))
	}

	// Parse credentials keyed by normalised source name
	creds := parseCredentials(data)
	logTrace("sourcesFromNugetConfig: %q — %d credential block(s), cleared=%v", path, len(creds), cleared)

	var sources []NugetSource
	for _, ps := range cfg.PackageSources {
		if disabled.Contains(strings.ToLower(ps.Key)) {
			logTrace("sourcesFromNugetConfig: [%s] skipped (disabled)", ps.Key)
			continue
		}
		// Only include http/https sources (skip local folder paths)
		if strings.HasPrefix(ps.Value, "http://") || strings.HasPrefix(ps.Value, "https://") {
			s := NugetSource{Name: ps.Key, URL: ps.Value}
			if c, ok := creds[normalizeCredentialKey(ps.Key)]; ok {
				s.Username = c.Username
				s.Password = c.Password
				logTrace("sourcesFromNugetConfig: [%s] credentials matched (username=%q, password=%d chars)", ps.Key, c.Username, len(c.Password))
			} else {
				logTrace("sourcesFromNugetConfig: [%s] no credentials found (lookup key=%q)", ps.Key, normalizeCredentialKey(ps.Key))
			}
			sources = append(sources, s)
		} else {
			logTrace("sourcesFromNugetConfig: [%s] skipped (not http/https: %q)", ps.Key, ps.Value)
		}
	}

	// GitHub Packages NuGet feeds accept "nobody" with an empty password for
	// public packages. Set a dummy username so Basic Auth is sent.
	for i := range sources {
		if strings.Contains(strings.ToLower(sources[i].URL), "nuget.pkg.github.com") && sources[i].Username == "" {
			sources[i].Username = "nobody"
			logTrace("sourcesFromNugetConfig: [%s] set default GitHub username %q", sources[i].Name, sources[i].Username)
		}
	}

	// Extract <packageSourceMapping> entries
	var mr *parsedMappingResult
	if cfg.SourceMapping != nil {
		mr = &parsedMappingResult{
			entries: make(map[string][]string),
			cleared: len(cfg.SourceMapping.Clear) > 0,
		}
		for _, src := range cfg.SourceMapping.Sources {
			for _, pkg := range src.Patterns {
				mr.entries[src.Key] = append(mr.entries[src.Key], strings.ToLower(pkg.Pattern))
			}
		}
		logTrace("sourcesFromNugetConfig: %q — %d mapping source(s), mapping-cleared=%v", path, len(mr.entries), mr.cleared)
	}

	return sources, cleared, mr
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
