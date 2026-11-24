package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nulifyer/guget/internal/userconfig"
)

var configSubcommands = []string{"path", "show", "get", "set", "unset"}

// configArgCounts is the number of arguments after each subcommand.
var configArgCounts = map[string]int{"path": 0, "show": 0, "get": 1, "set": 2, "unset": 1}

func init() {
	registerCLIVerb(cliVerbSpec{
		Name: "config", Order: 85, Label: "config SUBCOMMAND", Summary: "show or change user settings (path, show, get, set, unset)",
		Description: `Show or change the user settings file.

  path            print the settings file location
  show            list every setting with its value and source (env, file, or default)
  get KEY         print a setting's effective value
  set KEY VALUE   validate VALUE and store it in the settings file
  unset KEY       remove KEY from the settings file

Command-line flags override the environment, which overrides the file. --format, --output, and --no-headers apply to show.`,
		Positionals: []cliPositionalSpec{
			{Name: "SUBCOMMAND", Kind: valueChoice, Choices: configSubcommands, Unknown: "unknown config subcommand %q"},
			{Name: "KEY", Optional: true, Kind: valueChoice, ChoicesFunc: settingNames, After: []string{"get", "set", "unset"}},
			{Name: "VALUE", Optional: true},
		},
		Flags:    joinFlags(outputFlags(), tailFlags()),
		Validate: validateConfig,
		Row:      func(*cliInvocation) any { return configSettingRow{} },
		Run:      (*cliRuntime).runConfig,
	})
}

// configSettingRow holds Value and Default as a bool for boolean keys and a
// string otherwise, so JSON reports booleans as true or false.
type configSettingRow struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Source      string `json:"source"`
	Default     any    `json:"default"`
	Description string `json:"description"`
}

type configDocument struct {
	cliDocHeader
	Path     string             `json:"path"`
	Settings []configSettingRow `json:"settings"`
	Warnings []string           `json:"warnings"`
}

var configColumns = []cliColumn{{"KEY", "key"}, {"VALUE", "value"}, {"SOURCE", "source"}, {"DEFAULT", "default"}, {"DESCRIPTION", "description"}}

// validateConfig checks the subcommand, its arguments, the key, and a new
// value before anything reads or writes the settings file.
func validateConfig(inv *cliInvocation) error {
	args := inv.args
	sub := strings.ToLower(args.positionals[0])
	if sub != "show" && (len(args.values["format"]) > 0 || len(args.values["output"]) > 0 || args.bools["no-headers"]) {
		return errors.New("--format and --output apply only to guget config show")
	}
	rest := args.positionals[1:]
	if len(rest) != configArgCounts[sub] {
		return fmt.Errorf("wrong number of arguments for config %s\n%s", sub, configSynopsis)
	}
	if len(rest) == 0 {
		return nil
	}
	key, ok := settingKey(rest[0])
	if !ok {
		return fmt.Errorf("unknown setting %q (expected one of %s)", rest[0], strings.Join(settingNames(), ", "))
	}
	if sub == "set" {
		if _, err := key.Parse(rest[1]); err != nil {
			return err
		}
	}
	return nil
}

const configSynopsis = "Usage: guget config path | show [--format FORMAT] [--output FILE] | get KEY | set KEY VALUE | unset KEY\n"

func (r *cliRuntime) runConfig(_ context.Context, inv *cliInvocation) ExitCode {
	env := userconfig.Environment{}
	sub := strings.ToLower(inv.args.positionals[0])
	rest := inv.args.positionals[1:]
	switch sub {
	case "path":
		path, err := userconfig.Path(env)
		if err != nil {
			return inv.fail(ExitUsage, "%v", err)
		}
		fmt.Fprintln(r.stdout, path)
		return ExitSuccess
	case "show":
		return r.showConfig(inv, loadSettings(env).Values)
	}
	key, _ := settingKey(rest[0])
	if sub == "get" {
		values := loadSettings(env).Values
		r.emitWarnings(values.Warnings)
		setting, _ := values.Get(key.Name)
		fmt.Fprintln(r.stdout, setting.Value)
		return ExitSuccess
	}
	path, err := userconfig.Path(env)
	if err != nil {
		return inv.fail(ExitWrite, "%v", err)
	}
	if sub == "set" {
		err = userconfig.Set(path, key, rest[1])
	} else {
		err = userconfig.Unset(path, key)
	}
	if err != nil {
		return inv.fail(ExitWrite, "write %s: %v", path, err)
	}
	if setting, _ := loadSettings(env).Values.Get(key.Name); setting.Source == userconfig.SourceEnv {
		r.emitWarnings([]string{fmt.Sprintf("%s is set in the environment and overrides the settings file", setting.EnvVar)})
	}
	return ExitSuccess
}

func (r *cliRuntime) showConfig(inv *cliInvocation, values userconfig.Values) ExitCode {
	doc := &configDocument{cliDocHeader: newDocHeader("config", "ok"), Path: values.Path, Settings: []configSettingRow{}, Warnings: []string{}}
	inv.document = doc
	// The settings file is this command's input; --output must not replace it.
	if code := inv.guardOutputAgainst([]string{values.Path}); code != ExitSuccess {
		return code
	}
	for _, setting := range values.Settings {
		doc.Settings = append(doc.Settings, configSettingRow{
			Key: setting.Key.Name, Value: typedSettingValue(setting.Key, setting.Value), Source: string(setting.Source),
			Default: typedSettingValue(setting.Key, setting.Key.Default), Description: setting.Key.Description,
		})
	}
	doc.Warnings = append(doc.Warnings, redactAll(values.Warnings)...)
	r.emitWarnings(values.Warnings)
	return inv.emit(cliResult{Document: doc, Items: doc.Settings, Columns: configColumns})
}

func typedSettingValue(key userconfig.Key, value string) any {
	if key.Kind == userconfig.Bool {
		return value == "true"
	}
	return value
}

func settingKey(name string) (userconfig.Key, bool) {
	for _, key := range settingKeys() {
		if key.Name == strings.ToLower(name) {
			return key, true
		}
	}
	return userconfig.Key{}, false
}

func settingNames() []string {
	keys := settingKeys()
	names := make([]string, len(keys))
	for i, key := range keys {
		names[i] = key.Name
	}
	return names
}
