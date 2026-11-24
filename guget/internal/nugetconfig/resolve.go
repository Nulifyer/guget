package nugetconfig

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// resolve merges files ordered highest priority first; files from index
// machineStart on are machine-wide. Each section is merged independently: a
// closer file wins for the same name, and <clear/> drops what lower-priority
// files contribute to that section only.
func resolve(files []*configFile, machineStart int, defaults *configFile, env Environment) *Settings {
	settings := &Settings{}

	type loadedSource struct {
		source  Source
		enabled bool
		machine bool
	}
	disabled := make(map[string]bool)
	for _, file := range files {
		for _, key := range file.disabled.items {
			disabled[key] = true // restore compares names case-sensitively
		}
		if file.disabled.cleared {
			break
		}
	}
	credentials := credentialsByName(files)

	var loaded []loadedSource
	names := make(map[string]bool)
	for index, file := range files {
		for _, item := range file.sources.items {
			if lower := strings.ToLower(item.key); !names[lower] {
				names[lower] = true
				loaded = append(loaded, loadedSource{
					source:  buildSource(item, file.path, credentials, env, &settings.Warnings),
					enabled: !disabled[item.key],
					machine: index >= machineStart,
				})
			}
		}
		if file.sources.cleared {
			break
		}
	}

	if defaults != nil {
		// NuGetDefaults.config sources survive <clear/> unless a loaded source,
		// enabled or not, has the same name or location. Only the defaults file
		// itself can disable them or supply their credentials.
		defaultsDisabled := make(map[string]bool)
		for _, key := range defaults.disabled.items {
			defaultsDisabled[strings.ToLower(key)] = true
		}
		defaultsCredentials := credentialsByName([]*configFile{defaults})
		var added []loadedSource
		for _, item := range defaults.sources.items {
			source := buildSource(item, defaults.path, defaultsCredentials, env, &settings.Warnings)
			duplicate := false
			for _, existing := range loaded {
				duplicate = duplicate || strings.EqualFold(existing.source.Name, source.Name) || strings.EqualFold(existing.source.URL, source.URL)
			}
			if !duplicate {
				added = append(added, loadedSource{source: source, enabled: !defaultsDisabled[strings.ToLower(item.key)]})
			}
		}
		at := len(loaded)
		for index, existing := range loaded {
			if existing.machine {
				at = index
				break
			}
		}
		loaded = append(loaded[:at], append(added, loaded[at:]...)...)
	}

	for _, candidate := range loaded {
		if candidate.enabled {
			settings.Sources = append(settings.Sources, candidate.source)
		}
	}

	mapping := &Mapping{}
	mapped := make(map[string]bool)
	for _, file := range files {
		for _, item := range file.mapping.items {
			if lower := strings.ToLower(item.key); !mapped[lower] {
				mapped[lower] = true
				mapping.add(item.key, item.patterns)
			}
		}
		if file.mapping.cleared {
			break
		}
	}
	if mapping.configured() {
		settings.Mapping = mapping
	}
	return settings
}

func credentialsByName(files []*configFile) map[string]credentialItem {
	credentials := make(map[string]credentialItem)
	for _, file := range files {
		for _, item := range file.credentials {
			if _, known := credentials[item.source]; !known {
				credentials[item.source] = item
			}
		}
	}
	return credentials
}

// buildSource expands a declared source and resolves a local feed against the
// directory of the file that declared it.
func buildSource(item sourceItem, configPath string, credentials map[string]credentialItem, env Environment, warnings *[]string) Source {
	value := expandEnv(item.value, env)
	if !isHTTP(value) {
		value = resolveLocalPath(value, filepath.Dir(configPath))
	}
	source := Source{Name: item.key, URL: value}
	source.Username, source.Password = sourceCredential(item.key, credentials, env, warnings)
	return source
}

// credentialEnvPattern is NuGet's format for NuGetPackageSourceCredentials_<name>.
var credentialEnvPattern = regexp.MustCompile(`(?i)^Username=(.*?);\s*Password=(.*?)(?:;ValidAuthenticationTypes=(.*?))?$`)

// sourceCredential returns Basic credentials for a source. The environment
// variable takes precedence over configuration; a malformed variable is ignored.
func sourceCredential(name string, configured map[string]credentialItem, env Environment, warnings *[]string) (string, string) {
	if raw, ok := env.lookup("NuGetPackageSourceCredentials_" + name); ok {
		if match := credentialEnvPattern.FindStringSubmatch(strings.TrimSpace(raw)); match != nil {
			return basicCredential(name, match[1], match[2], match[3], warnings)
		}
	}
	item, ok := configured[name]
	if !ok {
		return "", ""
	}
	password := expandEnv(item.clearText, env)
	if item.encrypted {
		decrypted, err := decryptPassword(expandEnv(item.password, env))
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("source %q: encrypted password unavailable: %v", name, err))
			return "", ""
		}
		password = decrypted
	}
	return basicCredential(name, expandEnv(item.username, env), password, expandEnv(item.authTypes, env), warnings)
}

// basicCredential applies NuGet's rules for when a credential is sent: both
// parts must be present and Basic must be an allowed authentication type.
func basicCredential(name, username, password, authTypes string, warnings *[]string) (string, string) {
	if username == "" || password == "" {
		return "", ""
	}
	if strings.TrimSpace(authTypes) != "" {
		allowed := false
		for _, authType := range strings.Split(authTypes, ",") {
			allowed = allowed || strings.EqualFold(strings.TrimSpace(authType), "basic")
		}
		if !allowed {
			*warnings = append(*warnings, fmt.Sprintf("source %q: credentials do not allow Basic authentication", name))
			return "", ""
		}
	}
	return username, password
}

// expandEnv matches .NET Environment.ExpandEnvironmentVariables, which NuGet
// applies on every platform: %NAME% expands when defined and stays literal
// otherwise. $NAME is never expanded.
func expandEnv(value string, env Environment) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(value, '%')
		if start < 0 {
			break
		}
		end := strings.IndexByte(value[start+1:], '%')
		if end < 0 {
			break
		}
		end += start + 1
		b.WriteString(value[:start])
		name := value[start+1 : end]
		if expanded, ok := env.lookup(name); ok && name != "" {
			b.WriteString(expanded)
			value = value[end+1:]
			continue
		}
		// The closing % may open the next variable.
		b.WriteString(value[start:end])
		value = value[end:]
	}
	b.WriteString(value)
	return b.String()
}
