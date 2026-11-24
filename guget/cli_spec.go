package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// cliValueKind tells shell completion what a flag value or positional is.
type cliValueKind int

const (
	valueText   cliValueKind = iota // no completion
	valueFile                       // complete file names
	valueDir                        // complete directories
	valueChoice                     // complete Choices
)

// cliFlagSpec is the single description of one flag. Parsing, help, and every
// shell completion are generated from it, so an accepted flag is always
// documented.
type cliFlagSpec struct {
	Name        string
	Value       string // placeholder such as DIR; empty for a switch
	Description string
	Default     string // shown in help; also the parsed default for --timeout
	Repeatable  bool
	Kind        cliValueKind
	Choices     []string
	Short       string // deliberate alias such as "h" for --help
}

func (f cliFlagSpec) takesValue() bool { return f.Value != "" }

type cliPositionalSpec struct {
	Name     string
	Optional bool
	Kind     cliValueKind
	Choices  []string
	// ChoicesFunc supplies choices known only after every verb registers.
	ChoicesFunc func() []string
	// After limits completion of Choices to when the previous positional is
	// one of these values, such as config keys after get, set, or unset. Such
	// positionals are validated by the verb rather than the parser.
	After []string
	// Unknown formats the error for a value outside Choices, given the value.
	Unknown string
}

func (p cliPositionalSpec) choices() []string {
	if p.ChoicesFunc != nil {
		return p.ChoicesFunc()
	}
	return p.Choices
}

// cliVerbSpec owns one command's contract: synopsis, positionals, flags,
// validation, and the row fields its templates may reference.
type cliVerbSpec struct {
	Name        string
	Order       int    // position in command listings
	Label       string // command listing label; defaults to the name and positionals
	Summary     string
	Description string
	Positionals []cliPositionalSpec
	Flags       []cliFlagSpec
	Notes       []string
	// Validate checks flag combinations before any workspace or network work.
	// Its errors are usage errors.
	Validate func(*cliInvocation) error
	// Row returns a zero row as the command renders it for this invocation;
	// its JSON keys are the fields --format templates may reference.
	Row func(*cliInvocation) any
	Run func(*cliRuntime, context.Context, *cliInvocation) ExitCode
}

func (s *cliVerbSpec) flag(name string) (cliFlagSpec, bool) {
	for _, flag := range s.Flags {
		if flag.Name == name || flag.Short != "" && flag.Short == name {
			return flag, true
		}
	}
	return cliFlagSpec{}, false
}

func (s *cliVerbSpec) hasFlag(name string) bool {
	_, ok := s.flag(name)
	return ok
}

var cliVerbRegistry = map[string]*cliVerbSpec{}

// registerCLIVerb adds a command. Built-in verbs register in cli.go; a new verb
// can register from its own file with an init function.
func registerCLIVerb(spec cliVerbSpec) {
	name := strings.ToLower(spec.Name)
	if _, exists := cliVerbRegistry[name]; exists {
		panic("duplicate CLI verb " + name)
	}
	seen := make(map[string]bool)
	for _, flag := range spec.Flags {
		if seen[flag.Name] || flag.Short != "" && seen[flag.Short] {
			panic("duplicate flag --" + flag.Name + " for CLI verb " + name)
		}
		seen[flag.Name] = true
		if flag.Short != "" {
			seen[flag.Short] = true
		}
	}
	copy := spec
	cliVerbRegistry[name] = &copy
}

func lookupCLIVerb(name string) *cliVerbSpec {
	return cliVerbRegistry[strings.ToLower(name)]
}

func isCLIVerb(arg string) bool {
	return lookupCLIVerb(arg) != nil
}

func cliVerbsInOrder() []*cliVerbSpec {
	specs := make([]*cliVerbSpec, 0, len(cliVerbRegistry))
	for _, spec := range cliVerbRegistry {
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Order != specs[j].Order {
			return specs[i].Order < specs[j].Order
		}
		return specs[i].Name < specs[j].Name
	})
	return specs
}

func cliVerbNames() []string {
	var names []string
	for _, spec := range cliVerbsInOrder() {
		names = append(names, spec.Name)
	}
	return names
}

// Shared flag definitions keep wording identical across verbs.
var (
	flagProject = cliFlagSpec{Name: "project", Value: "DIR", Kind: valueDir,
		Description: "workspace directory to discover projects in", Default: "current directory"}
	flagIncludeIgnored = cliFlagSpec{Name: includeIgnoredFlag,
		Description: "also search directories excluded by .gitignore (bin and obj stay excluded)"}
	flagFormat = cliFlagSpec{Name: "format", Value: "FORMAT", Kind: valueChoice, Choices: []string{"table", "tsv", "json", "jsonl"},
		Description: "output format: " + cliFormatHelp,
		Default:     "table on a terminal, tsv otherwise"}
	flagOutput = cliFlagSpec{Name: "output", Value: "FILE", Kind: valueFile,
		Description: "write the result to FILE instead of stdout (- means stdout); never a workspace input"}
	flagNoHeaders = cliFlagSpec{Name: "no-headers",
		Description: "omit the header row of table, tsv, and table template output"}
	flagNoColor = cliFlagSpec{Name: "no-color",
		Description: "accepted for symmetry with the TUI; no effect on CLI output, which is never colored"}
	flagHelp         = cliFlagSpec{Name: "help", Short: "h", Description: "show this help"}
	flagUpdateTarget = cliFlagSpec{Name: "update-target", Value: "TARGET", Kind: valueChoice, Choices: updateTargets,
		Description: "update-target policy: compatible-stable, compatible, or stable", Default: "the update-target setting"}
)

func timeoutFlag(defaultValue string) cliFlagSpec {
	flag := cliFlagSpec{Name: "timeout", Value: "DURATION",
		Description: "stop after DURATION such as 30s or 2m and exit 124"}
	if defaultValue != "" {
		flag.Default = defaultValue
	} else {
		flag.Default = "none"
	}
	return flag
}

func workspaceFlags() []cliFlagSpec { return []cliFlagSpec{flagProject, flagIncludeIgnored} }

func outputFlags() []cliFlagSpec { return []cliFlagSpec{flagFormat, flagOutput, flagNoHeaders} }

func tailFlags() []cliFlagSpec { return []cliFlagSpec{flagNoColor, flagHelp} }

func joinFlags(groups ...[]cliFlagSpec) []cliFlagSpec {
	var flags []cliFlagSpec
	for _, group := range groups {
		flags = append(flags, group...)
	}
	return flags
}

type cliArgs struct {
	positionals []string
	values      map[string][]string
	bools       map[string]bool
}

func (a cliArgs) value(name, fallback string) string {
	values := a.values[name]
	if len(values) == 0 {
		return fallback
	}
	return values[len(values)-1]
}

// wantsHelp reports a --help or -h before any "--" terminator. Help wins over
// every other argument so it behaves the same for every verb.
func wantsHelp(argv []string) bool {
	for _, arg := range argv {
		if arg == "--" {
			return false
		}
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

// parseVerbArgs parses long flags (--name VALUE or --name=VALUE) anywhere among
// the positionals. A repeated single-value flag keeps its last value.
func parseVerbArgs(spec *cliVerbSpec, argv []string) (cliArgs, error) {
	parsed := cliArgs{values: make(map[string][]string), bools: make(map[string]bool)}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			parsed.positionals = append(parsed.positionals, argv[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			parsed.positionals = append(parsed.positionals, arg)
			continue
		}
		var name, inline string
		var hasInline bool
		if strings.HasPrefix(arg, "--") {
			name, inline, hasInline = strings.Cut(arg[2:], "=")
		} else {
			name = arg[1:]
			flag, ok := spec.flag(name)
			if !ok || flag.Short != name {
				return cliArgs{}, fmt.Errorf("unknown flag %q for guget %s; use long flags such as --project", arg, spec.Name)
			}
		}
		flag, ok := spec.flag(name)
		if !ok || strings.HasPrefix(arg, "--") && flag.Name != name {
			return cliArgs{}, fmt.Errorf("unknown flag --%s for guget %s", name, spec.Name)
		}
		if !flag.takesValue() {
			if hasInline {
				return cliArgs{}, fmt.Errorf("flag --%s does not take a value", flag.Name)
			}
			parsed.bools[flag.Name] = true
			continue
		}
		value := inline
		if !hasInline {
			i++
			if i >= len(argv) {
				return cliArgs{}, fmt.Errorf("flag --%s requires a value", flag.Name)
			}
			value = argv[i]
		}
		if flag.Kind == valueChoice && flag.Name != "format" && !containsFold(flag.Choices, value) {
			return cliArgs{}, fmt.Errorf("invalid --%s %q (expected %s)", flag.Name, value, strings.Join(flag.Choices, ", "))
		}
		parsed.values[flag.Name] = append(parsed.values[flag.Name], value)
	}
	minArgs, maxArgs := 0, len(spec.Positionals)
	for _, positional := range spec.Positionals {
		if !positional.Optional {
			minArgs++
		}
	}
	if len(parsed.positionals) < minArgs || len(parsed.positionals) > maxArgs {
		if maxArgs == 0 {
			return cliArgs{}, fmt.Errorf("guget %s does not accept arguments; got %q", spec.Name, strings.Join(parsed.positionals, " "))
		}
		return cliArgs{}, fmt.Errorf("wrong number of arguments\n%s", verbSynopsis(spec))
	}
	for i, value := range parsed.positionals {
		if positional := spec.Positionals[i]; positional.Kind == valueChoice && len(positional.After) == 0 && !containsFold(positional.choices(), value) {
			if positional.Unknown != "" {
				return cliArgs{}, fmt.Errorf(positional.Unknown, value)
			}
			return cliArgs{}, fmt.Errorf("invalid %s %q (expected %s)", positional.Name, value, strings.Join(positional.choices(), ", "))
		}
	}
	return parsed, nil
}

// cliInvocation is one parsed command. It records whether a result was
// written so failures can still produce one JSON document.
type cliInvocation struct {
	spec       *cliVerbSpec
	args       cliArgs
	format     cliFormat
	noHeaders  bool
	outputPath string
	timeout    time.Duration

	runtime  *cliRuntime
	document cliDocument
	failure  string
	written  bool // a result was written or a write was attempted
	blocked  bool // the --output destination was refused
}

// prepareInvocation validates everything that needs no workspace: flags,
// positionals, --format (including template fields), --timeout, and the verb's
// flag combinations.
func (r *cliRuntime) prepareInvocation(spec *cliVerbSpec, argv []string) (*cliInvocation, error) {
	args, err := parseVerbArgs(spec, argv)
	if err != nil {
		return nil, err
	}
	inv := &cliInvocation{spec: spec, args: args, runtime: r, noHeaders: args.bools["no-headers"], outputPath: args.value("output", "")}
	if spec.hasFlag("format") {
		if raw := args.value("format", ""); raw != "" {
			inv.format, err = parseCLIFormat(raw)
			if err != nil {
				return nil, err
			}
		} else {
			inv.format = defaultCLIFormat(r.stdout, inv.outputPath)
		}
	}
	if flag, ok := spec.flag("timeout"); ok {
		raw := args.value("timeout", "")
		if raw == "" && flag.Default != "none" {
			raw = flag.Default
		}
		if raw != "" {
			duration, err := time.ParseDuration(raw)
			if err != nil || duration <= 0 {
				return nil, fmt.Errorf("invalid --timeout %q (expected a positive duration such as 30s)", raw)
			}
			inv.timeout = duration
		}
	}
	if spec.Validate != nil {
		if err := spec.Validate(inv); err != nil {
			return nil, err
		}
	}
	if inv.format.isTemplate() && spec.Row != nil {
		row := spec.Row(inv)
		if err := inv.format.validateTemplateFields(cliRowKeys(row)); err != nil {
			return nil, err
		}
		if err := inv.format.validateTemplateSample(row); err != nil {
			return nil, err
		}
	}
	return inv, nil
}

func verbSynopsis(spec *cliVerbSpec) string {
	var b strings.Builder
	b.WriteString("Usage: guget ")
	b.WriteString(spec.Name)
	for _, positional := range spec.Positionals {
		if positional.Optional {
			fmt.Fprintf(&b, " [%s]", positional.Name)
		} else {
			fmt.Fprintf(&b, " %s", positional.Name)
		}
	}
	if len(spec.Flags) > 0 {
		b.WriteString(" [options]")
	}
	b.WriteString("\n")
	return b.String()
}

// verbHelp renders `guget VERB --help` and `guget help VERB`.
func verbHelp(spec *cliVerbSpec) string {
	var b strings.Builder
	b.WriteString(verbSynopsis(spec))
	b.WriteString("\n")
	description := spec.Description
	if description == "" {
		description = strings.ToUpper(spec.Summary[:1]) + spec.Summary[1:] + "."
	}
	b.WriteString(wrapHelp(description, 0))
	if len(spec.Flags) > 0 {
		b.WriteString("\nOptions:\n")
		width := 0
		labels := make([]string, len(spec.Flags))
		for i, flag := range spec.Flags {
			labels[i] = flagLabel(flag)
			width = max(width, len(labels[i]))
		}
		for i, flag := range spec.Flags {
			text := flag.Description
			if flag.Repeatable {
				text += " (repeatable)"
			}
			if flag.Default != "" && flag.Default != "none" {
				text += " (default: " + flag.Default + ")"
			}
			lines := wrapText(text, max(30, 78-width-4))
			for j, line := range lines {
				label := ""
				if j == 0 {
					label = labels[i]
				}
				fmt.Fprintf(&b, "  %-*s  %s\n", width, label, line)
			}
		}
	}
	for _, note := range spec.Notes {
		b.WriteString("\n")
		b.WriteString(wrapHelp(note, 0))
	}
	if spec.hasFlag("format") && spec.Row != nil {
		b.WriteString("\n")
		b.WriteString(wrapHelp("Templates: --format '{{.package}}' prints one line per row, and a 'table ' prefix adds an aligned header row. Type \\t for a tab. Template fields are the JSON keys of one row; null prints as empty text. Funcs: json, join, lower, upper.", 0))
		rowKeys := func(dryRun bool) []string {
			return cliRowKeys(spec.Row(&cliInvocation{args: cliArgs{bools: map[string]bool{"dry-run": dryRun}, values: map[string][]string{}}}))
		}
		applyKeys, dryRunKeys := rowKeys(false), rowKeys(true)
		b.WriteString("\n")
		if strings.Join(applyKeys, ",") == strings.Join(dryRunKeys, ",") {
			b.WriteString(wrapHelp("Fields: "+strings.Join(applyKeys, ", ")+".", 0))
		} else {
			b.WriteString(wrapHelp("Fields with --dry-run: "+strings.Join(dryRunKeys, ", ")+".", 0))
			b.WriteString(wrapHelp("Fields when applying: "+strings.Join(applyKeys, ", ")+".", 0))
		}
	}
	return b.String()
}

func flagLabel(flag cliFlagSpec) string {
	label := "--" + flag.Name
	if flag.takesValue() {
		label += " " + flag.Value
	}
	if flag.Short != "" {
		label = "-" + flag.Short + ", " + label
	}
	return label
}

func wrapHelp(text string, indent int) string {
	var b strings.Builder
	for _, paragraph := range strings.Split(text, "\n") {
		if strings.TrimSpace(paragraph) == "" {
			b.WriteString("\n")
			continue
		}
		if strings.HasPrefix(paragraph, "  ") {
			b.WriteString(paragraph + "\n")
			continue
		}
		for _, line := range wrapText(paragraph, 78-indent) {
			b.WriteString(strings.Repeat(" ", indent) + line + "\n")
		}
	}
	return b.String()
}

// cliUsage is `guget help`: every command, then a short note on the TUI.
func cliUsage() string {
	var b strings.Builder
	b.WriteString("Usage: guget <command> [options]\n       guget [TUI options] [DIRECTORY]\n\nCommands:\n")
	specs := cliVerbsInOrder()
	width := 0
	labels := make([]string, len(specs))
	for i, spec := range specs {
		label := spec.Name
		for _, positional := range spec.Positionals {
			if positional.Optional {
				label += " [" + positional.Name + "]"
			} else {
				label += " " + positional.Name
			}
		}
		if spec.Label != "" {
			label = spec.Label
		}
		labels[i] = label
		width = max(width, len(label))
	}
	for i, spec := range specs {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, labels[i], spec.Summary)
	}
	b.WriteString(`
Run "guget help COMMAND" or "guget COMMAND --help" for a command's options.
Options such as --project, --include-ignored, and --no-color may also appear
before the command.

Without a command, guget starts the interactive TUI in DIRECTORY or the current
directory. TUI options include --project, --theme, --sort-by, --no-mouse,
--no-color, --log-file, --verbose, --no-update-check, and --include-ignored.
Run "guget --help" for the full TUI option list.
`)
	return b.String()
}

// tuiUsageEpilogue ends `guget --help` with the CLI commands.
func tuiUsageEpilogue() string {
	return wrapHelp("Commands: "+strings.Join(cliVerbNames(), ", ")+". Run \"guget help\" for the command-line interface or \"guget help COMMAND\" for one command.", 0)
}

// cliGlobalValueFlags lists long flags that take a separate value when they
// appear before a verb, so the value is not mistaken for the verb.
func cliGlobalValueFlags() map[string]bool {
	flags := map[string]bool{
		Flag_Verbosity: true, "verbose": true, Flag_LogFile: true, Flag_Theme: true, Flag_SortBy: true,
	}
	for _, spec := range cliVerbRegistry {
		for _, flag := range spec.Flags {
			if flag.takesValue() {
				flags[flag.Name] = true
			}
		}
	}
	// Before a verb, --version is the TUI's switch, not add/update's value flag.
	delete(flags, Flag_Version)
	return flags
}

// routeCLIArgs moves long flags that precede a verb to after it, so
// `guget --project . list` runs the list verb. It reports false when the
// arguments start the TUI instead, such as `guget`, `guget DIR`, or flags
// without a verb.
func routeCLIArgs(args []string) ([]string, bool) {
	valueFlags := cliGlobalValueFlags()
	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "--" || !strings.HasPrefix(arg, "--") {
			break
		}
		name, _, inline := strings.Cut(arg[2:], "=")
		i++
		if !inline && valueFlags[name] && i < len(args) {
			i++
		}
	}
	if i >= len(args) || !isCLIVerb(args[i]) {
		return nil, false
	}
	routed := make([]string, 0, len(args))
	routed = append(routed, args[i])
	routed = append(routed, args[:i]...)
	routed = append(routed, args[i+1:]...)
	return routed, true
}
