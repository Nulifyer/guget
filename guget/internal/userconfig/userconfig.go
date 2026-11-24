// Package userconfig reads and edits guget's user settings file and maps
// settings to environment variables. It knows nothing about specific keys:
// callers describe them with Key.
//
// The file holds flat `key = value` lines, where a value is a TOML basic or
// literal string or a boolean. Blank lines and # comments are allowed, so the
// file stays valid TOML. Tables are not supported.
package userconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Environment is the process state settings are read from. The zero value
// uses the current process.
type Environment struct {
	LookupEnv func(string) (string, bool) // nil uses os.LookupEnv
	GOOS      string                      // empty uses runtime.GOOS
}

func (e Environment) lookup(name string) (string, bool) {
	if e.LookupEnv == nil {
		return os.LookupEnv(name)
	}
	return e.LookupEnv(name)
}

// getenv returns a variable's value. An empty value counts as unset.
func (e Environment) getenv(name string) string {
	value, _ := e.lookup(name)
	return value
}

func (e Environment) goos() string {
	if e.GOOS == "" {
		return runtime.GOOS
	}
	return e.GOOS
}

// PathVariable names the environment variable that overrides the file location.
const PathVariable = "GUGET_CONFIG"

// Path returns the settings file location: $GUGET_CONFIG, then
// %APPDATA%\guget\config.toml on Windows, or $XDG_CONFIG_HOME/guget/config.toml
// and ~/.config/guget/config.toml elsewhere. The file need not exist.
func Path(env Environment) (string, error) {
	if path := env.getenv(PathVariable); path != "" {
		return path, nil
	}
	if env.goos() == "windows" {
		appData := env.getenv("APPDATA")
		if appData == "" {
			return "", errors.New("cannot locate the settings file: APPDATA is not set; set " + PathVariable)
		}
		return filepath.Join(appData, "guget", "config.toml"), nil
	}
	// The XDG base directory specification ignores relative paths.
	if xdg := env.getenv("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "guget", "config.toml"), nil
	}
	home := env.getenv("HOME")
	if home == "" {
		return "", errors.New("cannot locate the settings file: HOME is not set; set " + PathVariable)
	}
	return filepath.Join(home, ".config", "guget", "config.toml"), nil
}

// Kind is a setting's value type.
type Kind int

const (
	String Kind = iota
	Bool
)

// EnvAlias is a legacy environment variable for a key. Map converts the
// variable's value to a setting value; ok is false when the value means
// nothing, in which case the alias is ignored.
type EnvAlias struct {
	Name string
	Map  func(value string) (setting string, ok bool)
}

// Key describes one setting.
type Key struct {
	Name        string
	Kind        Kind
	Default     string
	Description string
	// Validate checks a value and returns its normalized form. Bool values
	// are normalized to "true" or "false" before Validate runs. nil accepts
	// any value.
	Validate func(string) (string, error)
	// Aliases are legacy environment variables, consulted in order when the
	// canonical variable (see EnvName) is unset.
	Aliases []EnvAlias
}

// EnvName returns the key's canonical environment variable: GUGET_ followed
// by the upper-case name with '-' replaced by '_'.
func (k Key) EnvName() string {
	return "GUGET_" + strings.ToUpper(strings.ReplaceAll(k.Name, "-", "_"))
}

// Parse validates raw and returns the normalized value. Bool keys accept
// true/false, 1/0, yes/no, and on/off in any case.
func (k Key) Parse(raw string) (string, error) {
	value := raw
	if k.Kind == Bool {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "1", "yes", "on":
			value = "true"
		case "false", "0", "no", "off":
			value = "false"
		default:
			return "", fmt.Errorf("invalid value %q for %s: expected true or false", raw, k.Name)
		}
	}
	if k.Validate == nil {
		return value, nil
	}
	normalized, err := k.Validate(value)
	if err != nil {
		return "", fmt.Errorf("invalid value %q for %s: %v", raw, k.Name, err)
	}
	return normalized, nil
}

// Source reports where an effective value came from.
type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
)

// Setting is one key's effective value.
type Setting struct {
	Key    Key
	Value  string
	Source Source
	EnvVar string // the variable that supplied the value when Source is SourceEnv
}

// Values holds the effective value of every key, in key order.
type Values struct {
	Path     string // empty when the location could not be determined
	Settings []Setting
	Warnings []string
}

// Get returns the named setting.
func (v Values) Get(name string) (Setting, bool) {
	for _, setting := range v.Settings {
		if setting.Key.Name == name {
			return setting, true
		}
	}
	return Setting{}, false
}

// Load resolves every key from the environment, then the settings file, then
// its default. Problems never fail the load: an invalid value is reported in
// Warnings and the next source applies, unknown keys and unreadable files are
// reported and ignored.
func Load(keys []Key, env Environment) Values {
	values := Values{Settings: make([]Setting, 0, len(keys)), Warnings: []string{}}
	file := map[string]string{}
	path, err := Path(env)
	if err != nil {
		values.Warnings = append(values.Warnings, err.Error())
	} else {
		values.Path = path
		data, readErr := os.ReadFile(path)
		switch {
		case readErr == nil:
			var warnings []string
			file, warnings = decode(path, data, keys)
			values.Warnings = append(values.Warnings, warnings...)
		case !errors.Is(readErr, os.ErrNotExist):
			values.Warnings = append(values.Warnings, fmt.Sprintf("cannot read settings file: %v", readErr))
		}
	}
	for _, key := range keys {
		values.Settings = append(values.Settings, resolveKey(key, env, file, &values.Warnings))
	}
	return values
}

func resolveKey(key Key, env Environment, file map[string]string, warnings *[]string) Setting {
	if raw := env.getenv(key.EnvName()); raw != "" {
		value, err := key.Parse(raw)
		if err == nil {
			return Setting{Key: key, Value: value, Source: SourceEnv, EnvVar: key.EnvName()}
		}
		*warnings = append(*warnings, fmt.Sprintf("%s: %v; ignoring it", key.EnvName(), err))
	} else {
		for _, alias := range key.Aliases {
			raw := env.getenv(alias.Name)
			if raw == "" {
				continue
			}
			mapped, ok := alias.Map(raw)
			if !ok {
				continue
			}
			value, err := key.Parse(mapped)
			if err != nil {
				*warnings = append(*warnings, fmt.Sprintf("%s: %v; ignoring it", alias.Name, err))
				continue
			}
			return Setting{Key: key, Value: value, Source: SourceEnv, EnvVar: alias.Name}
		}
	}
	if value, ok := file[key.Name]; ok {
		return Setting{Key: key, Value: value, Source: SourceFile}
	}
	return Setting{Key: key, Value: key.Default, Source: SourceDefault}
}

// decode returns the valid values a settings file defines.
func decode(path string, data []byte, keys []Key) (map[string]string, []string) {
	byName := make(map[string]Key, len(keys))
	for _, key := range keys {
		byName[key.Name] = key
	}
	values := map[string]string{}
	seen := map[string]int{}
	var warnings []string
	warn := func(line int, format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf("%s:%d: %s", path, line, fmt.Sprintf(format, args...)))
	}
	for _, line := range parse(data) {
		switch {
		case line.err != nil:
			warn(line.number, "%v", line.err)
		case line.table != "":
			warn(line.number, "tables are not supported; ignoring [%s] and the keys after it", line.table)
		case line.inTable || line.key == "":
		default:
			key, known := byName[line.key]
			if !known {
				warn(line.number, "unknown setting %q", line.key)
				continue
			}
			if first, dup := seen[line.key]; dup {
				warn(line.number, "%s is already set on line %d; using this later value (TOML readers reject duplicate keys)", line.key, first)
				delete(values, line.key)
			}
			seen[line.key] = line.number
			if line.valueErr != nil {
				warn(line.number, "%s: %v", line.key, line.valueErr)
				continue
			}
			if line.isBool != (key.Kind == Bool) {
				if key.Kind == Bool {
					warn(line.number, "%s expects true or false without quotes", line.key)
				} else {
					warn(line.number, "%s expects a quoted string", line.key)
				}
				continue
			}
			value, err := key.Parse(line.value)
			if err != nil {
				warn(line.number, "%v", err)
				continue
			}
			values[line.key] = value
		}
	}
	return values, warnings
}
