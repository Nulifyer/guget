package main

import (
	"fmt"
	"strings"

	"github.com/nulifyer/guget/internal/userconfig"
)

// Setting names in the user settings file.
const (
	settingTheme        = "theme"
	settingSort         = "sort"
	settingMouse        = "mouse"
	settingUpdateCheck  = "update-check"
	settingVerbosity    = "verbosity"
	settingColor        = "color"
	settingUpdateTarget = "update-target"
)

var verbosityLevels = []string{"none", "error", "err", "warn", "warning", "info", "debug", "dbg", "trace", "trc"}

var updateTargets = []string{"compatible-stable", "compatible", "stable"}

// oneOf returns a validator that accepts allowed values in any case and
// normalizes them to lower case.
func oneOf(allowed []string) func(string) (string, error) {
	return func(value string) (string, error) {
		lower := strings.ToLower(strings.TrimSpace(value))
		for _, candidate := range allowed {
			if lower == candidate {
				return lower, nil
			}
		}
		return "", fmt.Errorf("expected one of %s", strings.Join(allowed, ", "))
	}
}

// settingKeys defines every user setting. Flags override these values; see
// registerCLIFlags.
func settingKeys() []userconfig.Key {
	return []userconfig.Key{
		{
			Name: settingTheme, Default: "auto", Validate: oneOf(validThemeNames),
			Description: "TUI color theme (--theme)",
		},
		{
			Name: settingSort, Default: "status:asc", Validate: parseSortBy,
			Description: "initial TUI package sort: status, name, source, requested, or available, with optional :asc or :desc (--sort-by)",
		},
		{
			Name: settingMouse, Kind: userconfig.Bool, Default: "true",
			Description: "mouse navigation in the TUI (false is --no-mouse)",
		},
		{
			Name: settingUpdateCheck, Kind: userconfig.Bool, Default: "true",
			Description: "automatic TUI checks for new Guget releases (false is --no-update-check)",
			Aliases: []userconfig.EnvAlias{{Name: "GUGET_NO_UPDATE_CHECK", Map: func(value string) (string, bool) {
				return "false", value == "1"
			}}},
		},
		{
			Name: settingVerbosity, Default: "warn", Validate: oneOf(verbosityLevels),
			Description: "TUI log level: none, error, warn, info, debug, or trace (--verbose)",
			Aliases:     []userconfig.EnvAlias{{Name: "LOG_LEVEL", Map: func(value string) (string, bool) { return value, true }}},
		},
		{
			Name: settingColor, Kind: userconfig.Bool, Default: "true",
			Description: "colored TUI output (false is --no-color)",
			// https://no-color.org: any non-empty value disables color.
			Aliases: []userconfig.EnvAlias{{Name: "NO_COLOR", Map: func(string) (string, bool) { return "false", true }}},
		},
		{
			Name: settingUpdateTarget, Default: "compatible-stable", Validate: oneOf(updateTargets),
			Description: "versions package updates aim for: compatible-stable, compatible, or stable",
		},
	}
}

// Settings are the effective user settings before command-line flags apply.
type Settings struct {
	Theme        string
	Sort         string
	Mouse        bool
	UpdateCheck  bool
	Verbosity    string
	Color        bool
	UpdateTarget string

	Values userconfig.Values
}

// loadSettings reads settings from env and the settings file it locates.
func loadSettings(env userconfig.Environment) Settings {
	values := userconfig.Load(settingKeys(), env)
	get := func(name string) string {
		setting, _ := values.Get(name)
		return setting.Value
	}
	return Settings{
		Theme:        get(settingTheme),
		Sort:         get(settingSort),
		Mouse:        get(settingMouse) == "true",
		UpdateCheck:  get(settingUpdateCheck) == "true",
		Verbosity:    get(settingVerbosity),
		Color:        get(settingColor) == "true",
		UpdateTarget: get(settingUpdateTarget),
		Values:       values,
	}
}

// effectiveSettings returns the settings of the current process environment
// and the user's settings file. It reads the file on every call.
func effectiveSettings() Settings {
	return loadSettings(userconfig.Environment{})
}
