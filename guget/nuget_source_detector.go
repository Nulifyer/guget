package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const defaultNugetSource = "https://api.nuget.org/v3/index.json"

type nugetConfig struct {
	XMLName              xml.Name                 `xml:"configuration"`
	PackageSources       []packageSource          `xml:"packageSources>add"`
	PackageSourcesClear  []struct{}               `xml:"packageSources>clear"` // <clear /> stops inheritance
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
// Errors make the configuration unusable; source fallback is disabled until repaired.
type DetectedConfig struct {
	Errors         []error
	InputRevisions map[string][sha256.Size]byte
	Sources        []NugetSource
	Mapping        *PackageSourceMapping
}

// parsedMappingResult is an internal type returned by sourcesFromNugetConfig
// for a single config file's <packageSourceMapping> section.
type parsedMappingResult struct {
	entries map[string][]string // source key → lowercase patterns
	cleared bool                // <clear/> inside <packageSourceMapping>
}

// DetectSources walks from projectDir up to root collecting NuGet sources and
// package-source mapping rules. <clear/> stops inheritance. Falls back to nuget.org.
func DetectSources(projectDir string) DetectedConfig {
	seen := NewSet[string]()
	revisions := make(map[string][sha256.Size]byte)
	var configErrors []error
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
	var seenConfigs []os.FileInfo
	readConfig := func(path string) ([]byte, bool) {
		if path == "" {
			return nil, false
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			configErrors = append(configErrors, fmt.Errorf("resolve source configuration %s: %w", path, err))
			return nil, false
		}
		info, err := os.Stat(absolute)
		if os.IsNotExist(err) {
			return nil, false
		}
		if err != nil {
			configErrors = append(configErrors, fmt.Errorf("inspect source configuration %s: %w", absolute, err))
			return nil, false
		}
		if !info.Mode().IsRegular() {
			configErrors = append(configErrors, fmt.Errorf("source configuration is not a regular file: %s", absolute))
			return nil, false
		}
		for _, previous := range seenConfigs {
			if os.SameFile(previous, info) {
				return nil, false
			}
		}
		seenConfigs = append(seenConfigs, info)
		data, err := os.ReadFile(absolute)
		if err != nil {
			configErrors = append(configErrors, fmt.Errorf("read source configuration %s: %w", absolute, err))
			return nil, false
		}
		revisions[absolute] = sha256.Sum256(data)
		return data, true
	}
	addConfig := func(path string) bool {
		data, found := readConfig(path)
		if !found {
			return false
		}
		srcs, cleared, mr, err := parseNugetConfig(path, data)
		if err != nil {
			configErrors = append(configErrors, fmt.Errorf("invalid NuGet configuration %s: %w", path, err))
			return false
		}
		for _, source := range srcs {
			add(source)
		}
		if !mappingCleared && mr != nil {
			if mr.cleared {
				mapping = &PackageSourceMapping{Entries: make(map[string][]string)}
				mappingCleared = true
			}
			for key, value := range mr.entries {
				mapping.Entries[key] = append(mapping.Entries[key], value...)
			}
		}
		return cleared
	}
	addBuildProps := func(path string) {
		data, found := readConfig(path)
		if !found {
			return
		}
		sources, err := parseBuildPropsSources(path, data)
		if err != nil {
			configErrors = append(configErrors, fmt.Errorf("invalid source configuration %s: %w", path, err))
			return
		}
		for _, source := range sources {
			add(source)
		}
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
		addBuildProps(filepath.Join(dir, "Directory.Build.props"))

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
	if len(sources) == 0 && !cleared && len(configErrors) == 0 {
		add(NugetSource{Name: "nuget.org", URL: defaultNugetSource})
	}

	// Nil out empty mapping so IsConfigured() returns false.
	if len(mapping.Entries) == 0 {
		mapping = nil
	}

	if len(configErrors) > 0 {
		sources, mapping = nil, nil
	}
	return DetectedConfig{Sources: sources, Mapping: mapping, Errors: configErrors, InputRevisions: revisions}
}

// sourcesFromNugetConfig parses a single NuGet.Config file.
func sourcesFromNugetConfig(path string) ([]NugetSource, bool, *parsedMappingResult) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, nil
	}
	sources, cleared, mapping, err := parseNugetConfig(path, data)
	if err != nil {
		return nil, false, nil
	}
	return sources, cleared, mapping
}

func parseNugetConfig(path string, data []byte) ([]NugetSource, bool, *parsedMappingResult, error) {
	var cfg nugetConfig
	if err := decodeSourceConfigXML(data, &cfg); err != nil {
		return nil, false, nil, err
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
		// Keep local/UNC sources visible for restore provenance even though the
		// metadata browser currently supports only HTTP(S) V3 service indexes.
		if strings.HasPrefix(ps.Value, "http://") || strings.HasPrefix(ps.Value, "https://") {
			s := NugetSource{Name: ps.Key, URL: ps.Value}
			if c, ok := creds[normalizeCredentialKey(ps.Key)]; ok {
				s.Username = c.Username
				s.Password = c.Password
				logRegisterSecret(c.Password)
				logTrace("sourcesFromNugetConfig: [%s] credentials matched (username=%q, password=%d chars)", ps.Key, c.Username, len(c.Password))
			} else {
				logTrace("sourcesFromNugetConfig: [%s] no credentials found (lookup key=%q)", ps.Key, normalizeCredentialKey(ps.Key))
			}
			sources = append(sources, s)
		} else if strings.TrimSpace(ps.Value) != "" {
			value := os.ExpandEnv(ps.Value)
			if !filepath.IsAbs(value) && !strings.HasPrefix(value, `\\`) {
				value = filepath.Join(filepath.Dir(path), value)
			}
			sources = append(sources, NugetSource{Name: ps.Key, URL: filepath.Clean(value)})
			logTrace("sourcesFromNugetConfig: [%s] included as restore-only source", ps.Key)
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

	return sources, cleared, mr, nil
}

func sourcesFromBuildProps(path string) []NugetSource {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	sources, _ := parseBuildPropsSources(path, data)
	return sources
}

func parseBuildPropsSources(path string, data []byte) ([]NugetSource, error) {

	// Parse RestoreSources property from MSBuild XML
	type propertyGroup struct {
		RestoreSources string `xml:"RestoreSources"`
	}
	type msbuild struct {
		XMLName        xml.Name        `xml:"Project"`
		PropertyGroups []propertyGroup `xml:"PropertyGroup"`
	}

	var props msbuild
	if err := decodeSourceConfigXML(data, &props); err != nil {
		return nil, err
	}

	var sources []NugetSource
	for _, pg := range props.PropertyGroups {
		if pg.RestoreSources == "" {
			continue
		}
		for _, raw := range strings.Split(pg.RestoreSources, ";") {
			raw = strings.TrimSpace(raw)
			if raw != "" {
				value := raw
				isHTTP := strings.HasPrefix(strings.ToLower(value), "http://") || strings.HasPrefix(strings.ToLower(value), "https://")
				if !isHTTP && !filepath.IsAbs(value) {
					value = filepath.Join(filepath.Dir(path), value)
				}
				if !isHTTP {
					value = filepath.Clean(value)
				}
				sources = append(sources, NugetSource{Name: raw, URL: value})
			}
		}
	}
	return sources, nil
}

func userNugetConfigPath() string {
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		return filepath.Join(appdata, "NuGet", "NuGet.Config")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		logWarn("os.UserHomeDir(): %v", err)
		return ""
	}
	return filepath.Join(home, ".nuget", "NuGet", "NuGet.Config")
}

func machineNugetConfigPath() string {
	if runtime.GOOS == "windows" {
		programdata := os.Getenv("ProgramData")
		return filepath.Join(programdata, "NuGet", "NuGet.Config")
	}
	return "/etc/opt/nuget/NuGet.Config"
}

// Reject trailing roots/text as well as malformed XML. Errors deliberately omit
// document content because configuration files can contain source credentials.
func decodeSourceConfigXML(data []byte, target any) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return safeSourceConfigXMLError(err)
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return safeSourceConfigXMLError(err)
		}
		switch token := token.(type) {
		case xml.Comment, xml.ProcInst:
		case xml.CharData:
			if len(bytes.TrimSpace(token)) != 0 {
				return fmt.Errorf("unexpected content outside the configuration root")
			}
		default:
			return fmt.Errorf("expected a single configuration root")
		}
	}
}

func safeSourceConfigXMLError(err error) error {
	var syntax *xml.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("malformed XML at line %d", syntax.Line)
	}
	return fmt.Errorf("invalid configuration XML")
}
