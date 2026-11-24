package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/nulifyer/guget/internal/userconfig"
)

const configSynopsis = "Usage: guget config path | show [--format table|tsv|json|jsonl] [--output FILE] | get KEY | set KEY VALUE | unset KEY\n"

const configSubcommands = "path show get set unset"

func configUsage() string {
	return configSynopsis + `
Subcommands:
  path            print the settings file location
  show            list every setting with its value and source (env, file, or default)
  get KEY         print a setting's effective value
  set KEY VALUE   validate VALUE and store it in the settings file
  unset KEY       remove KEY from the settings file

Command-line flags override the environment, which overrides the file.
Run guget config show to list the keys.
`
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
	SchemaVersion int                `json:"schemaVersion"`
	Command       string             `json:"command"`
	Path          string             `json:"path"`
	Settings      []configSettingRow `json:"settings"`
	Warnings      []string           `json:"warnings"`
}

func (r cliRuntime) runConfig(argv []string) ExitCode {
	args, err := parseCLIArgs(argv, map[string]bool{"format": true, "output": true}, map[string]bool{"help": true, "no-color": true})
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	if args.bools["help"] {
		fmt.Fprint(r.stdout, configUsage())
		return ExitSuccess
	}
	if len(args.positionals) == 0 {
		fmt.Fprint(r.stderr, configUsage())
		return ExitUsage
	}
	sub, rest := strings.ToLower(args.positionals[0]), args.positionals[1:]
	if sub != "show" && (len(args.values["format"]) > 0 || len(args.values["output"]) > 0) {
		fmt.Fprintln(r.stderr, "guget: --format and --output apply only to guget config show")
		return ExitUsage
	}
	wantArgs := map[string]int{"path": 0, "show": 0, "get": 1, "set": 2, "unset": 1}
	count, known := wantArgs[sub]
	if !known {
		fmt.Fprintf(r.stderr, "guget: unknown config subcommand %q\n\n%s", args.positionals[0], configUsage())
		return ExitUsage
	}
	if len(rest) != count {
		fmt.Fprint(r.stderr, configSynopsis)
		return ExitUsage
	}

	env := userconfig.Environment{}
	switch sub {
	case "path":
		path, err := userconfig.Path(env)
		if err != nil {
			fmt.Fprintf(r.stderr, "guget: %v\n", err)
			return ExitUsage
		}
		fmt.Fprintln(r.stdout, path)
		return ExitSuccess
	case "show":
		return r.showConfig(args, loadSettings(env).Values)
	}

	key, ok := settingKey(rest[0])
	if !ok {
		fmt.Fprintf(r.stderr, "guget: unknown setting %q (expected one of %s)\n", rest[0], strings.Join(settingNames(), ", "))
		return ExitUsage
	}
	if sub == "get" {
		values := loadSettings(env).Values
		r.emitWarnings(values.Warnings)
		setting, _ := values.Get(key.Name)
		fmt.Fprintln(r.stdout, setting.Value)
		return ExitSuccess
	}
	if sub == "set" {
		if _, err := key.Parse(rest[1]); err != nil {
			fmt.Fprintf(r.stderr, "guget: %v\n", err)
			return ExitUsage
		}
	}
	path, err := userconfig.Path(env)
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: %v\n", err)
		return ExitWrite
	}
	if sub == "set" {
		err = userconfig.Set(path, key, rest[1])
	} else {
		err = userconfig.Unset(path, key)
	}
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: write %s: %v\n", path, err)
		return ExitWrite
	}
	if setting, _ := loadSettings(env).Values.Get(key.Name); setting.Source == userconfig.SourceEnv {
		r.emitWarnings([]string{fmt.Sprintf("%s is set in the environment and overrides the settings file", setting.EnvVar)})
	}
	return ExitSuccess
}

func (r cliRuntime) showConfig(args cliArgs, values userconfig.Values) ExitCode {
	doc := configDocument{SchemaVersion: 1, Command: "config", Path: values.Path, Settings: []configSettingRow{}, Warnings: values.Warnings}
	for _, setting := range values.Settings {
		doc.Settings = append(doc.Settings, configSettingRow{
			Key: setting.Key.Name, Value: typedSettingValue(setting.Key, setting.Value), Source: string(setting.Source),
			Default: typedSettingValue(setting.Key, setting.Key.Default), Description: setting.Key.Description,
		})
	}
	format := outputFormat(args, r.stdout)
	data, err := renderConfig(doc, format)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	if format != "json" {
		r.emitWarnings(doc.Warnings)
	}
	return r.writeOutput(args.value("output", ""), data)
}

func typedSettingValue(key userconfig.Key, value string) any {
	if key.Kind == userconfig.Bool {
		return value == "true"
	}
	return value
}

func renderConfig(doc configDocument, format string) ([]byte, error) {
	var buffer bytes.Buffer
	switch format {
	case "json":
		encoder := json.NewEncoder(&buffer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(doc); err != nil {
			return nil, err
		}
	case "jsonl":
		encoder := json.NewEncoder(&buffer)
		for _, row := range doc.Settings {
			if err := encoder.Encode(row); err != nil {
				return nil, err
			}
		}
	case "table":
		writer := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "KEY\tVALUE\tSOURCE\tDEFAULT\tDESCRIPTION")
		for _, row := range doc.Settings {
			fmt.Fprintf(writer, "%s\t%v\t%s\t%v\t%s\n", row.Key, row.Value, row.Source, row.Default, row.Description)
		}
		if err := writer.Flush(); err != nil {
			return nil, err
		}
	case "tsv":
		fmt.Fprintln(&buffer, "KEY\tVALUE\tSOURCE\tDEFAULT\tDESCRIPTION")
		for _, row := range doc.Settings {
			fmt.Fprintf(&buffer, "%s\t%v\t%s\t%v\t%s\n", row.Key, row.Value, row.Source, row.Default, row.Description)
		}
	default:
		return nil, fmt.Errorf("invalid --format %q (expected table, tsv, json, or jsonl)", format)
	}
	return buffer.Bytes(), nil
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

// bashCompletion completes commands, then config subcommands and keys.
func bashCompletion(commands string) string {
	return fmt.Sprintf(`_guget() {
  local cur=${COMP_WORDS[COMP_CWORD]} words=%q
  if [ "$COMP_CWORD" -ge 2 ] && [ "${COMP_WORDS[1]}" = config ]; then
    case $COMP_CWORD in
      2) words=%q ;;
      3) case ${COMP_WORDS[2]} in get|set|unset) words=%q ;; *) words= ;; esac ;;
      *) words= ;;
    esac
  fi
  COMPREPLY=($(compgen -W "$words" -- "$cur"))
}
complete -F _guget guget
`, commands, configSubcommands, strings.Join(settingNames(), " "))
}

func zshCompletion(commands string) string {
	return fmt.Sprintf(`_guget() {
  if (( CURRENT > 2 )) && [[ ${words[2]} == config ]]; then
    if (( CURRENT == 3 )); then
      compadd %s
    elif (( CURRENT == 4 )) && [[ ${words[3]} == (get|set|unset) ]]; then
      compadd %s
    fi
    return
  fi
  _arguments "1:command:(%s)"
}
compdef _guget guget
`, configSubcommands, strings.Join(settingNames(), " "), commands)
}

func fishConfigCompletion() string {
	return fmt.Sprintf(`complete -c guget -f -n '__fish_seen_subcommand_from config; and not __fish_seen_subcommand_from %[1]s' -a '%[1]s'
complete -c guget -f -n '__fish_seen_subcommand_from config; and __fish_seen_subcommand_from get set unset' -a '%[2]s'
`, configSubcommands, strings.Join(settingNames(), " "))
}
