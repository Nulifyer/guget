package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nulifyer/guget/internal/atomicfile"
	editplan "github.com/nulifyer/guget/internal/edit"
	"github.com/nulifyer/guget/internal/packageops"
	"golang.org/x/term"
)

type ExitCode int

const (
	ExitSuccess     ExitCode = 0
	ExitUsage       ExitCode = 1
	ExitWorkspace   ExitCode = 2
	ExitSource      ExitCode = 3
	ExitRefused     ExitCode = 4
	ExitWrite       ExitCode = 5
	ExitRestore     ExitCode = 6
	ExitPartial     ExitCode = 7
	ExitInterrupted ExitCode = 130
)

var cliVerbs = map[string]struct{}{
	"list": {}, "show": {}, "search": {}, "sources": {},
	"add": {}, "update": {}, "remove": {}, "restore": {},
	"completion": {}, "version": {}, "help": {},
}

func isCLIVerb(arg string) bool {
	_, ok := cliVerbs[strings.ToLower(arg)]
	return ok
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

func parseCLIArgs(args []string, valueFlags, boolFlags map[string]bool) (cliArgs, error) {
	parsed := cliArgs{values: make(map[string][]string), bools: make(map[string]bool)}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			parsed.positionals = append(parsed.positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			parsed.positionals = append(parsed.positionals, arg)
			continue
		}
		name, inline, hasInline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			return cliArgs{}, fmt.Errorf("unknown flag %q; use long flags such as --project", arg)
		}
		if boolFlags[name] {
			if hasInline {
				return cliArgs{}, fmt.Errorf("flag --%s does not take a value", name)
			}
			parsed.bools[name] = true
			continue
		}
		if !valueFlags[name] {
			return cliArgs{}, fmt.Errorf("unknown flag --%s", name)
		}
		value := inline
		if !hasInline {
			i++
			if i >= len(args) {
				return cliArgs{}, fmt.Errorf("flag --%s requires a value", name)
			}
			value = args[i]
		}
		parsed.values[name] = append(parsed.values[name], value)
	}
	return parsed, nil
}

var commonValueFlags = map[string]bool{
	"project": true, "framework": true, "source": true, "format": true,
	"output": true, "timeout": true,
}

var commonBoolFlags = map[string]bool{
	"no-color": true, "help": true,
}

func cloneFlags(flags map[string]bool, names ...string) map[string]bool {
	result := make(map[string]bool, len(flags)+len(names))
	for name, value := range flags {
		result[name] = value
	}
	for _, name := range names {
		result[name] = true
	}
	return result
}

type cliRuntime struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	newService func(context.Context, NugetSource) (*NugetService, error)
	runCommand func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error
}

func runCLICommand(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func dispatchCLI(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) ExitCode {
	runtime := cliRuntime{
		stdin: stdin, stdout: stdout, stderr: stderr,
		newService: NewNugetServiceContext, runCommand: runCLICommand,
	}
	logSetOutput(stderr)
	logSetLevel(LogLevelNone)
	logSetColor(false)

	if len(argv) == 0 {
		fmt.Fprint(stderr, cliUsage())
		return ExitUsage
	}
	verb := strings.ToLower(argv[0])
	if !isCLIVerb(verb) {
		fmt.Fprintf(stderr, "guget: unknown command %q\n\n%s", argv[0], cliUsage())
		return ExitUsage
	}

	var code ExitCode
	switch verb {
	case "help":
		code = runtime.runHelp(argv[1:])
	case "version":
		code = runtime.runVersion(argv[1:])
	case "completion":
		code = runtime.runCompletion(argv[1:])
	case "list", "show":
		code = runtime.runList(ctx, verb, argv[1:])
	case "sources":
		code = runtime.runSources(ctx, argv[1:])
	case "search":
		code = runtime.runSearch(ctx, argv[1:])
	case "add", "update", "remove":
		code = runtime.runMutation(ctx, verb, argv[1:])
	case "restore":
		code = runtime.runRestore(ctx, argv[1:])
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return ExitInterrupted
	}
	return code
}

func cliUsage() string {
	return `Usage: guget <command> [options]

Commands:
  list                 list package references in the workspace
  show PACKAGE         show one package across projects
  sources              show configured NuGet sources and mappings
  search QUERY         search configured NuGet sources
  add PACKAGE          add a package to explicit --file targets
  update PACKAGE       update explicit --file targets or --all
  remove PACKAGE       remove from explicit --file targets or --all
  restore              run dotnet restore for explicit targets or --all
  completion SHELL     emit completion for bash, zsh, or fish
  version              print the Guget version
  help [COMMAND]       show help

Run guget without a command to start the TUI.
`
}

func (r cliRuntime) runHelp(args []string) ExitCode {
	if len(args) > 1 {
		fmt.Fprintln(r.stderr, "guget help accepts at most one command")
		return ExitUsage
	}
	if len(args) == 0 {
		fmt.Fprint(r.stdout, cliUsage())
		return ExitSuccess
	}
	if !isCLIVerb(args[0]) {
		fmt.Fprintf(r.stderr, "guget: unknown command %q\n", args[0])
		return ExitUsage
	}
	if usage := commandUsage(strings.ToLower(args[0])); usage != "" {
		fmt.Fprint(r.stdout, usage)
	} else {
		fmt.Fprint(r.stdout, cliUsage())
	}
	return ExitSuccess
}

func commandUsage(verb string) string {
	switch verb {
	case "list":
		return "Usage: guget list [--project DIR] [--format table|tsv|json|jsonl] [--output FILE]\n"
	case "show":
		return "Usage: guget show PACKAGE [--project DIR] [--format table|tsv|json|jsonl]\n"
	case "sources":
		return "Usage: guget sources [--project DIR] [--format table|tsv|json|jsonl]\n"
	case "search":
		return "Usage: guget search QUERY [--project DIR] [--source NAME] [--format table|tsv|json|jsonl]\n"
	case "add":
		return "Usage: guget add PACKAGE --version VERSION --file PROJECT [--file PROJECT...] [--dry-run] [--restore] [--interactive]\n"
	case "update":
		return "Usage: guget update PACKAGE --version VERSION (--file PROJECT... | --all) [--dry-run] [--restore] [--interactive]\n"
	case "remove":
		return "Usage: guget remove PACKAGE (--file PROJECT... | --all) [--dry-run] [--restore] [--interactive]\n"
	case "restore":
		return "Usage: guget restore (--file PROJECT... | --all) [--project DIR] [--interactive]\n"
	case "completion":
		return "Usage: guget completion bash|zsh|fish\n"
	case "version":
		return "Usage: guget version\n"
	}
	return ""
}

func (r cliRuntime) runVersion(args []string) ExitCode {
	if len(args) != 0 {
		fmt.Fprintln(r.stderr, "guget version does not accept arguments")
		return ExitUsage
	}
	fmt.Fprintln(r.stdout, versionString())
	return ExitSuccess
}

func (r cliRuntime) runCompletion(args []string) ExitCode {
	if len(args) != 1 {
		fmt.Fprint(r.stderr, commandUsage("completion"))
		return ExitUsage
	}
	commands := "list show sources search add update remove restore completion version help"
	switch args[0] {
	case "bash":
		fmt.Fprintf(r.stdout, "complete -W %q guget\n", commands)
	case "zsh":
		fmt.Fprintf(r.stdout, "compdef '_arguments \"1:command:(%s)\"' guget\n", commands)
	case "fish":
		for _, command := range strings.Fields(commands) {
			fmt.Fprintf(r.stdout, "complete -c guget -f -n '__fish_use_subcommand' -a %s\n", command)
		}
	default:
		fmt.Fprint(r.stderr, commandUsage("completion"))
		return ExitUsage
	}
	return ExitSuccess
}

func withTimeout(ctx context.Context, raw string) (context.Context, context.CancelFunc, error) {
	if raw == "" {
		return ctx, func() {}, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return nil, nil, fmt.Errorf("invalid --timeout %q", raw)
	}
	timed, cancel := context.WithTimeout(ctx, duration)
	return timed, cancel, nil
}

func workspaceRoot(args cliArgs) (string, error) {
	root := args.value("project", "")
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(root)
}

type cliPackageRow struct {
	Project             string   `json:"project"`
	ProjectPath         string   `json:"projectPath"`
	Package             string   `json:"package"`
	RequestedExpression string   `json:"requestedExpression"`
	ResolvedVersion     string   `json:"resolvedVersion,omitempty"`
	OwnerPath           string   `json:"ownerPath"`
	ReferenceOwner      string   `json:"referenceOwner"`
	VersionOwner        string   `json:"versionOwner,omitempty"`
	TargetFrameworks    []string `json:"targetFrameworks"`
	Direct              bool     `json:"direct"`
	Implicit            bool     `json:"implicit"`
	EditSupported       bool     `json:"editSupported"`
	EditReason          string   `json:"editReason,omitempty"`
	Locked              bool     `json:"locked"`
}

type listDocument struct {
	SchemaVersion int             `json:"schemaVersion"`
	Command       string          `json:"command"`
	Workspace     string          `json:"workspace"`
	Packages      []cliPackageRow `json:"packages"`
	Warnings      []string        `json:"warnings"`
}

func rowsFromSnapshot(ctx context.Context, snapshot *workspaceSnapshot, onlyPackage string, includeTransitive bool) ([]cliPackageRow, []string) {
	rows := make([]cliPackageRow, 0)
	warnings := make([]string, 0)
	msbuild := packageops.MSBuildInspector{}
	graph := packageops.NuGetGraphInspector{}
	for _, project := range snapshot.ParsedProjects {
		declared := make(map[string]PackageReference)
		for ref := range project.Packages {
			declared[strings.ToLower(ref.Name)] = ref
		}
		evaluated, evalErr := msbuild.InspectProject(ctx, project.FilePath)
		resolved, graphErr := graph.ResolveProject(ctx, project.FilePath, includeTransitive)
		resolvedByKey := make(map[string]packageops.PackageUse)
		for _, use := range resolved {
			resolvedByKey[packageUseKey(use.PackageID, use.TargetFramework, use.Direct)] = use
		}
		if graphErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: resolved graph unavailable or stale: %v", project.FileName, graphErr))
		}
		if evalErr == nil {
			for _, use := range evaluated.PackageUses {
				if onlyPackage != "" && !strings.EqualFold(use.PackageID, onlyPackage) {
					continue
				}
				requested := use.EvaluatedExpression
				ref := declared[strings.ToLower(use.PackageID)]
				if ref.Requested != "" {
					requested = ref.Requested
				}
				resolvedVersion := ""
				if graphUse, ok := resolvedByKey[packageUseKey(use.PackageID, use.TargetFramework, true)]; ok {
					resolvedVersion = graphUse.ResolvedVersion
				}
				rows = append(rows, cliPackageRow{
					Project: project.FileName, ProjectPath: project.FilePath,
					Package: use.PackageID, RequestedExpression: requested, ResolvedVersion: resolvedVersion,
					OwnerPath: use.VersionOwner, ReferenceOwner: use.ReferenceOwner, VersionOwner: use.VersionOwner,
					TargetFrameworks: []string{use.TargetFramework}, Direct: true, Implicit: use.Implicit,
					EditSupported: use.Edit.Supported, EditReason: use.Edit.Reason, Locked: ref.Locked,
				})
			}
		} else {
			warnings = append(warnings, fmt.Sprintf("%s: MSBuild evaluation unavailable; showing declared fallback: %v", project.FileName, evalErr))
			frameworks := make([]string, 0, project.TargetFrameworks.Len())
			for framework := range project.TargetFrameworks {
				frameworks = append(frameworks, framework.String())
			}
			sort.Strings(frameworks)
			for ref := range project.Packages {
				if onlyPackage != "" && !strings.EqualFold(ref.Name, onlyPackage) {
					continue
				}
				requested := ref.Requested
				if requested == "" {
					requested = ref.Version.String()
				}
				owner := project.SourceFileForPackage(ref.Name)
				rows = append(rows, cliPackageRow{
					Project: project.FileName, ProjectPath: project.FilePath, Package: ref.Name,
					RequestedExpression: requested, OwnerPath: owner, ReferenceOwner: owner, VersionOwner: owner,
					TargetFrameworks: frameworks, Direct: true, EditReason: "MSBuild evaluation unavailable", Locked: ref.Locked,
				})
			}
		}
		if includeTransitive && graphErr == nil {
			for _, use := range resolved {
				if use.Direct || onlyPackage != "" && !strings.EqualFold(use.PackageID, onlyPackage) {
					continue
				}
				rows = append(rows, cliPackageRow{
					Project: project.FileName, ProjectPath: project.FilePath, Package: use.PackageID,
					ResolvedVersion: use.ResolvedVersion, TargetFrameworks: []string{use.TargetFramework},
					EditReason: "transitive packages are resolved, not directly editable",
				})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !strings.EqualFold(rows[i].Package, rows[j].Package) {
			return strings.ToLower(rows[i].Package) < strings.ToLower(rows[j].Package)
		}
		return rows[i].ProjectPath < rows[j].ProjectPath
	})
	return rows, warnings
}

func packageUseKey(packageID, framework string, direct bool) string {
	return strings.ToLower(packageID) + "\x00" + strings.ToLower(framework) + fmt.Sprintf("\x00%t", direct)
}

func (r cliRuntime) runList(ctx context.Context, verb string, argv []string) ExitCode {
	values := cloneFlags(commonValueFlags)
	bools := cloneFlags(commonBoolFlags, "outdated", "vulnerable", "deprecated", "direct", "include-transitive")
	args, err := parseCLIArgs(argv, values, bools)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	if args.bools["help"] {
		fmt.Fprint(r.stdout, commandUsage(verb))
		return ExitSuccess
	}
	if verb == "show" && len(args.positionals) != 1 || verb == "list" && len(args.positionals) != 0 {
		fmt.Fprint(r.stderr, commandUsage(verb))
		return ExitUsage
	}
	if args.bools["outdated"] || args.bools["vulnerable"] || args.bools["deprecated"] {
		fmt.Fprintln(r.stderr, "the requested filter requires evaluated restore metadata, which is not available yet")
		return ExitPartial
	}
	ctx, cancel, err := withTimeout(ctx, args.value("timeout", ""))
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	defer cancel()
	root, err := workspaceRoot(args)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitWorkspace
	}
	if err := ctx.Err(); err != nil {
		return ExitInterrupted
	}
	snapshot, err := scanWorkspace(root)
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: %v\n", err)
		return ExitWorkspace
	}
	only := ""
	if verb == "show" {
		only = args.positionals[0]
	}
	rows, warnings := rowsFromSnapshot(ctx, snapshot, only, args.bools["include-transitive"])
	if frameworks := args.values["framework"]; len(frameworks) > 0 {
		filtered := rows[:0]
		for _, row := range rows {
			for _, framework := range frameworks {
				if containsFold(row.TargetFrameworks, framework) {
					filtered = append(filtered, row)
					break
				}
			}
		}
		rows = filtered
	}
	if args.bools["direct"] {
		filtered := rows[:0]
		for _, row := range rows {
			if row.Direct {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	if len(args.values["source"]) > 0 {
		warnings = append(warnings, "--source filtering is unavailable because dotnet package-list JSON does not report restore-source provenance")
	}
	for _, artifact := range snapshot.Unsupported {
		warnings = append(warnings, fmt.Sprintf("%s: %s", artifact.Path, artifact.Reason))
	}
	doc := listDocument{SchemaVersion: 1, Command: verb, Workspace: snapshot.ProjectDir, Packages: rows, Warnings: warnings}
	data, err := renderPackageRows(doc, outputFormat(args, r.stdout))
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	code := r.writeOutput(args.value("output", ""), data)
	if code == ExitSuccess && len(warnings) > 0 {
		return ExitPartial
	}
	return code
}

func outputFormat(args cliArgs, output io.Writer) string {
	if explicit := strings.ToLower(args.value("format", "")); explicit != "" {
		return explicit
	}
	if file, ok := output.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return "table"
	}
	return "tsv"
}

func renderPackageRows(doc listDocument, format string) ([]byte, error) {
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
		for _, row := range doc.Packages {
			if err := encoder.Encode(row); err != nil {
				return nil, err
			}
		}
	case "table":
		writer := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "PROJECT\tPACKAGE\tREQUESTED\tRESOLVED\tVERSION OWNER\tFRAMEWORKS")
		for _, row := range doc.Packages {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", row.Project, row.Package, row.RequestedExpression, row.ResolvedVersion, row.VersionOwner, strings.Join(row.TargetFrameworks, ","))
		}
		if err := writer.Flush(); err != nil {
			return nil, err
		}
	case "tsv":
		fmt.Fprintln(&buffer, "PROJECT\tPACKAGE\tREQUESTED\tRESOLVED\tVERSION_OWNER\tFRAMEWORKS")
		for _, row := range doc.Packages {
			fmt.Fprintf(&buffer, "%s\t%s\t%s\t%s\t%s\t%s\n", row.Project, row.Package, row.RequestedExpression, row.ResolvedVersion, row.VersionOwner, strings.Join(row.TargetFrameworks, ","))
		}
	default:
		return nil, fmt.Errorf("invalid --format %q (expected table, tsv, json, or jsonl)", format)
	}
	return buffer.Bytes(), nil
}

func (r cliRuntime) writeOutput(path string, data []byte) ExitCode {
	if path == "" || path == "-" {
		if _, err := r.stdout.Write(data); err != nil {
			fmt.Fprintf(r.stderr, "guget: write output: %v\n", err)
			return ExitWrite
		}
		return ExitSuccess
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: output path: %v\n", err)
		return ExitWrite
	}
	if err := atomicfile.WriteFile(abs, data, 0o644); err != nil {
		fmt.Fprintf(r.stderr, "guget: write %s: %v\n", abs, err)
		return ExitWrite
	}
	return ExitSuccess
}

type sourceRow struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Capability string   `json:"capability"`
	Mapped     bool     `json:"mapped"`
	Patterns   []string `json:"patterns"`
}

type sourcesDocument struct {
	SchemaVersion int         `json:"schemaVersion"`
	Command       string      `json:"command"`
	Workspace     string      `json:"workspace"`
	Sources       []sourceRow `json:"sources"`
	Warnings      []string    `json:"warnings"`
}

func (r cliRuntime) runSources(ctx context.Context, argv []string) ExitCode {
	args, err := parseCLIArgs(argv, commonValueFlags, commonBoolFlags)
	if err != nil || len(args.positionals) != 0 {
		if err != nil {
			fmt.Fprintln(r.stderr, err)
		} else {
			fmt.Fprint(r.stderr, commandUsage("sources"))
		}
		return ExitUsage
	}
	if args.bools["help"] {
		fmt.Fprint(r.stdout, commandUsage("sources"))
		return ExitSuccess
	}
	ctx, cancel, err := withTimeout(ctx, args.value("timeout", ""))
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	defer cancel()
	root, err := workspaceRoot(args)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitWorkspace
	}
	if err := ctx.Err(); err != nil {
		return ExitInterrupted
	}
	snapshot, err := scanWorkspace(root)
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: %v\n", err)
		return ExitWorkspace
	}
	doc := sourcesDocument{SchemaVersion: 1, Command: "sources", Workspace: snapshot.ProjectDir, Warnings: []string{}}
	for _, source := range snapshot.Sources {
		capability := "metadata"
		if !strings.HasPrefix(strings.ToLower(source.URL), "http://") && !strings.HasPrefix(strings.ToLower(source.URL), "https://") {
			capability = "restore-only"
		}
		row := sourceRow{Name: source.Name, URL: source.URL, Capability: capability}
		if snapshot.SourceMapping != nil {
			row.Patterns = append([]string(nil), snapshot.SourceMapping.Entries[source.Name]...)
			row.Mapped = len(row.Patterns) > 0
			sort.Strings(row.Patterns)
		}
		doc.Sources = append(doc.Sources, row)
	}
	sort.Slice(doc.Sources, func(i, j int) bool {
		return strings.ToLower(doc.Sources[i].Name) < strings.ToLower(doc.Sources[j].Name)
	})
	data, err := renderSources(doc, outputFormat(args, r.stdout))
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	return r.writeOutput(args.value("output", ""), data)
}

func renderSources(doc sourcesDocument, format string) ([]byte, error) {
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
		for _, row := range doc.Sources {
			if err := encoder.Encode(row); err != nil {
				return nil, err
			}
		}
	case "table":
		writer := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tURL\tCAPABILITY\tMAPPED PATTERNS")
		for _, row := range doc.Sources {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", row.Name, row.URL, row.Capability, strings.Join(row.Patterns, ","))
		}
		if err := writer.Flush(); err != nil {
			return nil, err
		}
	case "tsv":
		fmt.Fprintln(&buffer, "NAME\tURL\tCAPABILITY\tMAPPED PATTERNS")
		for _, row := range doc.Sources {
			fmt.Fprintf(&buffer, "%s\t%s\t%s\t%s\n", row.Name, row.URL, row.Capability, strings.Join(row.Patterns, ","))
		}
	default:
		return nil, fmt.Errorf("invalid --format %q (expected table, tsv, json, or jsonl)", format)
	}
	return buffer.Bytes(), nil
}

func (r cliRuntime) runSearch(ctx context.Context, argv []string) ExitCode {
	args, err := parseCLIArgs(argv, commonValueFlags, commonBoolFlags)
	if err != nil || len(args.positionals) != 1 {
		if err != nil {
			fmt.Fprintln(r.stderr, err)
		} else {
			fmt.Fprint(r.stderr, commandUsage("search"))
		}
		return ExitUsage
	}
	if args.bools["help"] {
		fmt.Fprint(r.stdout, commandUsage("search"))
		return ExitSuccess
	}
	ctx, cancel, err := withTimeout(ctx, args.value("timeout", "30s"))
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	defer cancel()
	root, err := workspaceRoot(args)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitWorkspace
	}
	snapshot, err := scanWorkspace(root)
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: %v\n", err)
		return ExitWorkspace
	}
	wantedSources := make(map[string]bool)
	for _, name := range args.values["source"] {
		wantedSources[strings.ToLower(name)] = true
	}
	query := args.positionals[0]
	document := searchDocument{SchemaVersion: 1, Command: "search", Workspace: root, Query: query, Results: []searchRow{}, Warnings: []string{}}
	seen := make(map[string]bool)
	attempted := 0
	failed := 0
	for _, source := range snapshot.Sources {
		if len(wantedSources) > 0 && !wantedSources[strings.ToLower(source.Name)] {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(source.URL), "http://") && !strings.HasPrefix(strings.ToLower(source.URL), "https://") {
			document.Warnings = append(document.Warnings, fmt.Sprintf("source %s is restore-only and cannot be searched", source.Name))
			continue
		}
		attempted++
		service, err := r.newService(ctx, source)
		if err != nil {
			failed++
			document.Warnings = append(document.Warnings, fmt.Sprintf("source %s unavailable: %v", source.Name, err))
			continue
		}
		results, err := service.SearchContext(ctx, query, 50)
		if err != nil {
			failed++
			document.Warnings = append(document.Warnings, fmt.Sprintf("source %s search failed: %v", source.Name, err))
			continue
		}
		for _, result := range results {
			if snapshot.SourceMapping.IsConfigured() {
				allowed := snapshot.SourceMapping.SourcesForPackage(result.ID)
				if !containsFold(allowed, source.Name) {
					continue
				}
			}
			key := strings.ToLower(result.ID)
			if seen[key] {
				continue
			}
			seen[key] = true
			document.Results = append(document.Results, searchRow{Package: result.ID, Version: result.Version, Description: result.Description, Authors: []string(result.Authors), Source: source.Name})
		}
	}
	if len(wantedSources) > 0 && attempted == 0 {
		fmt.Fprintln(r.stderr, "guget: none of the requested --source names are configured metadata sources")
		return ExitSource
	}
	sort.Slice(document.Results, func(i, j int) bool {
		iExact := strings.EqualFold(document.Results[i].Package, query)
		jExact := strings.EqualFold(document.Results[j].Package, query)
		if iExact != jExact {
			return iExact
		}
		return strings.ToLower(document.Results[i].Package) < strings.ToLower(document.Results[j].Package)
	})
	data, err := renderSearch(document, outputFormat(args, r.stdout))
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	if code := r.writeOutput(args.value("output", ""), data); code != ExitSuccess {
		return code
	}
	if attempted > 0 && failed == attempted {
		return ExitSource
	}
	if len(document.Warnings) > 0 {
		return ExitPartial
	}
	return ExitSuccess
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

type searchRow struct {
	Package     string   `json:"package"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Authors     []string `json:"authors"`
	Source      string   `json:"source"`
}

type searchDocument struct {
	SchemaVersion int         `json:"schemaVersion"`
	Command       string      `json:"command"`
	Workspace     string      `json:"workspace"`
	Query         string      `json:"query"`
	Results       []searchRow `json:"results"`
	Warnings      []string    `json:"warnings"`
}

func renderSearch(document searchDocument, format string) ([]byte, error) {
	var buffer bytes.Buffer
	switch format {
	case "json":
		encoder := json.NewEncoder(&buffer)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(document); err != nil {
			return nil, err
		}
	case "jsonl":
		encoder := json.NewEncoder(&buffer)
		for _, row := range document.Results {
			if err := encoder.Encode(row); err != nil {
				return nil, err
			}
		}
	case "table":
		writer := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "PACKAGE\tVERSION\tSOURCE\tDESCRIPTION")
		for _, row := range document.Results {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", row.Package, row.Version, row.Source, strings.ReplaceAll(row.Description, "\n", " "))
		}
		if err := writer.Flush(); err != nil {
			return nil, err
		}
	case "tsv":
		fmt.Fprintln(&buffer, "PACKAGE\tVERSION\tSOURCE\tDESCRIPTION")
		for _, row := range document.Results {
			description := strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(row.Description)
			fmt.Fprintf(&buffer, "%s\t%s\t%s\t%s\n", row.Package, row.Version, row.Source, description)
		}
	default:
		return nil, fmt.Errorf("invalid --format %q (expected table, tsv, json, or jsonl)", format)
	}
	return buffer.Bytes(), nil
}

type planRow struct {
	Operation string `json:"operation"`
	Package   string `json:"package"`
	Version   string `json:"version,omitempty"`
	Path      string `json:"path"`
}

type planDocument struct {
	SchemaVersion int       `json:"schemaVersion"`
	Command       string    `json:"command"`
	Workspace     string    `json:"workspace"`
	DryRun        bool      `json:"dryRun"`
	Changes       []planRow `json:"changes"`
	Warnings      []string  `json:"warnings"`
}

func mutationFlags() (map[string]bool, map[string]bool) {
	values := cloneFlags(commonValueFlags, "file", "version")
	bools := cloneFlags(commonBoolFlags, "all", "dry-run", "restore", "interactive", "latest-compatible", "latest-stable")
	return values, bools
}

func (r cliRuntime) runMutation(ctx context.Context, verb string, argv []string) ExitCode {
	values, bools := mutationFlags()
	args, err := parseCLIArgs(argv, values, bools)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	if args.bools["help"] {
		fmt.Fprint(r.stdout, commandUsage(verb))
		return ExitSuccess
	}
	if len(args.positionals) != 1 {
		fmt.Fprint(r.stderr, commandUsage(verb))
		return ExitUsage
	}
	files := args.values["file"]
	if args.bools["all"] == (len(files) > 0) {
		fmt.Fprintln(r.stderr, "choose exactly one mutation scope: repeatable --file or --all")
		return ExitUsage
	}
	if verb == "add" && args.bools["all"] {
		fmt.Fprintln(r.stderr, "guget add requires one or more explicit --file targets")
		return ExitUsage
	}
	if args.bools["interactive"] && !args.bools["restore"] {
		fmt.Fprintln(r.stderr, "--interactive requires --restore for mutation commands")
		return ExitUsage
	}
	selectors := 0
	if args.value("version", "") != "" {
		selectors++
	}
	if args.bools["latest-compatible"] {
		selectors++
	}
	if args.bools["latest-stable"] {
		selectors++
	}
	if verb != "remove" && selectors != 1 {
		fmt.Fprintln(r.stderr, "choose exactly one version selector: --version, --latest-compatible, or --latest-stable")
		return ExitUsage
	}
	if selectors > 0 && verb == "remove" {
		fmt.Fprintln(r.stderr, "guget remove does not accept a version selector")
		return ExitUsage
	}
	if args.bools["latest-compatible"] || args.bools["latest-stable"] {
		fmt.Fprintln(r.stderr, "latest-version selection requires evaluated framework compatibility and is not available yet")
		return ExitRefused
	}
	ctx, cancel, err := withTimeout(ctx, args.value("timeout", ""))
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	defer cancel()

	root, err := workspaceRoot(args)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitWorkspace
	}
	snapshot, err := scanWorkspace(root)
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: %v\n", err)
		return ExitWorkspace
	}
	targets, err := mutationTargets(snapshot, root, files, args.bools["all"])
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: %v\n", err)
		return ExitUsage
	}
	packageID := args.positionals[0]
	requestedVersion := args.value("version", "")
	plan, rows, err := buildMutationPlan(verb, packageID, requestedVersion, targets)
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: edit refused: %v\n", err)
		return ExitRefused
	}
	doc := planDocument{SchemaVersion: 1, Command: verb, Workspace: root, DryRun: args.bools["dry-run"], Changes: rows, Warnings: []string{}}
	if args.bools["dry-run"] {
		data, err := renderPlan(doc, outputFormat(args, r.stdout))
		if err != nil {
			fmt.Fprintln(r.stderr, err)
			return ExitUsage
		}
		return r.writeOutput(args.value("output", ""), data)
	}
	if err := ctx.Err(); err != nil {
		return ExitInterrupted
	}
	result, err := plan.Apply()
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: apply failed: %v\n", err)
		return ExitWrite
	}
	for _, path := range result.Changed {
		fmt.Fprintf(r.stderr, "%s: changed %s\n", verb, path)
	}
	if len(result.Changed) == 0 {
		fmt.Fprintln(r.stderr, "no changes needed")
	}
	if args.bools["restore"] && len(result.Changed) > 0 {
		if code := r.restoreProjects(ctx, targets, args.bools["interactive"]); code != ExitSuccess {
			return code
		}
	}
	return ExitSuccess
}

func mutationTargets(snapshot *workspaceSnapshot, root string, files []string, all bool) ([]*ParsedProject, error) {
	byPath := make(map[string]*ParsedProject, len(snapshot.ParsedProjects))
	for _, project := range snapshot.ParsedProjects {
		abs, err := filepath.Abs(project.FilePath)
		if err != nil {
			return nil, err
		}
		byPath[filepath.Clean(abs)] = project
	}
	if all {
		return append([]*ParsedProject(nil), snapshot.ParsedProjects...), nil
	}
	var targets []*ParsedProject
	seen := make(map[string]bool)
	for _, raw := range files {
		path := raw
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		abs = filepath.Clean(abs)
		project, ok := byPath[abs]
		if !ok {
			return nil, fmt.Errorf("--file target is not a discovered project in this workspace: %s", raw)
		}
		if !seen[abs] {
			targets = append(targets, project)
			seen[abs] = true
		}
	}
	return targets, nil
}

func buildMutationPlan(verb, packageID, version string, targets []*ParsedProject) (editplan.Plan, []planRow, error) {
	var changes []editplan.Change
	var rows []planRow
	seenPaths := make(map[string]bool)
	packageSeen := false
	appendChange := func(change editplan.Change, operation, targetVersion string) {
		abs, _ := filepath.Abs(change.Path)
		if seenPaths[abs] {
			return
		}
		seenPaths[abs] = true
		changes = append(changes, change)
		rows = append(rows, planRow{Operation: operation, Package: packageID, Version: targetVersion, Path: abs})
	}

	for _, project := range targets {
		var installed *PackageReference
		for ref := range project.Packages {
			if strings.EqualFold(ref.Name, packageID) {
				copy := ref
				installed = &copy
				packageSeen = true
				break
			}
		}
		switch verb {
		case "add":
			if installed != nil {
				continue
			}
			if central := projectCPMTarget(project); central != "" {
				centralAbs, _ := filepath.Abs(central)
				defined, err := fileDefinesPackage(centralAbs, packageID)
				if err != nil {
					return editplan.Plan{}, nil, err
				}
				if !defined && !seenPaths[centralAbs] {
					change, err := PlanAddPackageVersion(centralAbs, packageID, version)
					if err != nil {
						return editplan.Plan{}, nil, err
					}
					appendChange(change, "add central version", version)
				}
				change, err := PlanAddPackageReference(project.FilePath, packageID, "")
				if err != nil {
					return editplan.Plan{}, nil, err
				}
				appendChange(change, "add reference", "")
			} else {
				change, err := PlanAddPackageReference(project.FilePath, packageID, version)
				if err != nil {
					return editplan.Plan{}, nil, err
				}
				appendChange(change, "add reference", version)
			}
		case "update":
			if installed == nil {
				continue
			}
			owner := project.SourceFileForPackage(packageID)
			ownerAbs, _ := filepath.Abs(owner)
			if seenPaths[ownerAbs] {
				continue
			}
			change, err := PlanUpdatePackageVersion(owner, packageID, version)
			if err != nil {
				return editplan.Plan{}, nil, err
			}
			appendChange(change, "update version", version)
		case "remove":
			if installed == nil {
				continue
			}
			local, err := fileHasPackageReference(project.FilePath, packageID)
			if err != nil {
				return editplan.Plan{}, nil, err
			}
			if !local {
				return editplan.Plan{}, nil, fmt.Errorf("package %q is inherited by %s from %s; no project-owned reference can be removed safely", packageID, project.FileName, project.SourceFileForPackage(packageID))
			}
			change, err := PlanRemovePackageReference(project.FilePath, packageID)
			if err != nil {
				return editplan.Plan{}, nil, err
			}
			appendChange(change, "remove reference", "")
		}
	}
	if verb != "add" && !packageSeen {
		return editplan.Plan{}, nil, fmt.Errorf("package %q is not installed in the selected scope", packageID)
	}
	plan, err := editplan.NewPlan(changes...)
	if err != nil {
		return editplan.Plan{}, nil, err
	}
	if plan.Len() == 0 {
		rows = []planRow{}
	}
	return plan, rows, nil
}

func projectCPMTarget(project *ParsedProject) string {
	for _, target := range project.AddTargets {
		if target.Kind == AddTargetCPM {
			return target.FilePath
		}
	}
	return ""
}

func fileHasPackageReference(path, packageID string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	elements, err := scanPackageElements(data)
	if err != nil {
		return false, err
	}
	for _, element := range elements {
		if strings.EqualFold(element.tag, "PackageReference") && strings.EqualFold(element.name, packageID) {
			return true, nil
		}
	}
	return false, nil
}

func fileDefinesPackage(path, packageID string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	elements, err := scanPackageElements(data)
	if err != nil {
		return false, err
	}
	for _, element := range elements {
		if strings.EqualFold(element.name, packageID) {
			return true, nil
		}
	}
	return false, nil
}

func renderPlan(doc planDocument, format string) ([]byte, error) {
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
		for _, row := range doc.Changes {
			if err := encoder.Encode(row); err != nil {
				return nil, err
			}
		}
	case "table":
		writer := tabwriter.NewWriter(&buffer, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "OPERATION\tPACKAGE\tVERSION\tPATH")
		for _, row := range doc.Changes {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", row.Operation, row.Package, row.Version, row.Path)
		}
		if err := writer.Flush(); err != nil {
			return nil, err
		}
	case "tsv":
		fmt.Fprintln(&buffer, "OPERATION\tPACKAGE\tVERSION\tPATH")
		for _, row := range doc.Changes {
			fmt.Fprintf(&buffer, "%s\t%s\t%s\t%s\n", row.Operation, row.Package, row.Version, row.Path)
		}
	default:
		return nil, fmt.Errorf("invalid --format %q (expected table, tsv, json, or jsonl)", format)
	}
	return buffer.Bytes(), nil
}

func (r cliRuntime) runRestore(ctx context.Context, argv []string) ExitCode {
	values := cloneFlags(commonValueFlags, "file")
	bools := cloneFlags(commonBoolFlags, "all", "interactive")
	args, err := parseCLIArgs(argv, values, bools)
	if err != nil || len(args.positionals) != 0 {
		if err != nil {
			fmt.Fprintln(r.stderr, err)
		} else {
			fmt.Fprint(r.stderr, commandUsage("restore"))
		}
		return ExitUsage
	}
	if args.bools["help"] {
		fmt.Fprint(r.stdout, commandUsage("restore"))
		return ExitSuccess
	}
	ctx, cancel, err := withTimeout(ctx, args.value("timeout", ""))
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitUsage
	}
	defer cancel()
	files := args.values["file"]
	if args.bools["all"] == (len(files) > 0) {
		fmt.Fprintln(r.stderr, "choose exactly one restore scope: repeatable --file or --all")
		return ExitUsage
	}
	root, err := workspaceRoot(args)
	if err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitWorkspace
	}
	snapshot, err := scanWorkspace(root)
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: %v\n", err)
		return ExitWorkspace
	}
	targets, err := mutationTargets(snapshot, root, files, args.bools["all"])
	if err != nil {
		fmt.Fprintf(r.stderr, "guget: %v\n", err)
		return ExitUsage
	}
	return r.restoreProjects(ctx, targets, args.bools["interactive"])
}

func (r cliRuntime) restoreProjects(ctx context.Context, projects []*ParsedProject, interactive bool) ExitCode {
	runCommand := r.runCommand
	if runCommand == nil {
		runCommand = runCLICommand
	}
	for _, project := range projects {
		args := []string{"restore", project.FilePath}
		if interactive {
			args = append(args, "--interactive")
		}
		if err := runCommand(ctx, "dotnet", args, r.stdin, r.stderr, r.stderr); err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return ExitInterrupted
			}
			fmt.Fprintf(r.stderr, "guget: restore %s: %v\n", project.FilePath, err)
			return ExitRestore
		}
	}
	return ExitSuccess
}
