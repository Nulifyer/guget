package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/nulifyer/guget/internal/atomicfile"
	"github.com/nulifyer/guget/internal/packageops"
	"github.com/nulifyer/guget/internal/projectedit"
	"github.com/nulifyer/guget/internal/selfupdate"
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
	ExitTimeout     ExitCode = 124
	ExitInterrupted ExitCode = 130
)

// Built-in verbs. Each spec is the single owner of its command's contract.
func init() {
	registerCLIVerb(cliVerbSpec{
		Name: "list", Order: 10, Summary: "list package references in the workspace",
		Description: "List package references in every discovered project, one row per project, package, and target framework.",
		Flags: joinFlags(workspaceFlags(), []cliFlagSpec{
			{Name: "framework", Value: "TFM", Repeatable: true, Description: "only rows for this target framework"},
			{Name: "direct", Description: "only direct references"},
			{Name: "include-transitive", Description: "add restored transitive packages"},
			{Name: "outdated", Description: "only rows with an update target under the update-target policy; fetches metadata"},
			{Name: "vulnerable", Description: "only rows whose version has known vulnerabilities; fetches metadata"},
			{Name: "deprecated", Description: "only rows whose version is deprecated; fetches metadata"},
			{Name: "metadata", Description: "fetch package metadata from the sources to fill the update, vulnerability, and deprecation fields of every row"},
			flagUpdateTarget,
		}, outputFlags(), []cliFlagSpec{timeoutFlag("")}, tailFlags()),
		Notes: []string{
			"--outdated, --vulnerable, and --deprecated combine as any of: a row is listed when it has any selected problem. Without --timeout, fetching metadata stops after 30s.",
			"Exit status 7 means some data could not be produced, for example when MSBuild evaluation, restore metadata, or some package metadata is missing; warnings say what. 3 means every source failed.",
		},
		Row: func(*cliInvocation) any { return cliPackageRow{} },
		Run: (*cliRuntime).runList,
	})
	registerCLIVerb(cliVerbSpec{
		Name: "show", Order: 20, Summary: "show one package across projects",
		Description: "Show every reference to PACKAGE in the workspace. An unreferenced package is not an error: the result is empty and a warning says so.",
		Positionals: []cliPositionalSpec{{Name: "PACKAGE"}},
		Flags: joinFlags(workspaceFlags(), []cliFlagSpec{
			{Name: "framework", Value: "TFM", Repeatable: true, Description: "only rows for this target framework"},
			{Name: "direct", Description: "only direct references"},
			{Name: "include-transitive", Description: "add restored transitive packages"},
		}, outputFlags(), []cliFlagSpec{timeoutFlag("")}, tailFlags()),
		Row: func(*cliInvocation) any { return cliPackageRow{} },
		Run: (*cliRuntime).runList,
	})
	registerCLIVerb(cliVerbSpec{
		Name: "sources", Order: 30, Summary: "show configured NuGet sources and mappings",
		Description: "Show the NuGet sources and package source mapping patterns that apply to the workspace. Credentials in source URLs are redacted.",
		Flags:       joinFlags(workspaceFlags(), outputFlags(), []cliFlagSpec{timeoutFlag("")}, tailFlags()),
		Row:         func(*cliInvocation) any { return sourceRow{} },
		Run:         (*cliRuntime).runSources,
	})
	registerCLIVerb(cliVerbSpec{
		Name: "search", Order: 40, Summary: "search configured NuGet sources",
		Description: "Search the workspace's HTTP sources for QUERY. Local and UNC sources support restore only and are skipped with a warning.",
		Positionals: []cliPositionalSpec{{Name: "QUERY"}},
		Flags: joinFlags(workspaceFlags(), []cliFlagSpec{
			{Name: "source", Value: "NAME", Repeatable: true, Description: "search only this configured source; see guget sources"},
		}, outputFlags(), []cliFlagSpec{timeoutFlag("30s")}, tailFlags()),
		Notes: []string{"Exit status 7 means some sources failed and the results come from the rest; 3 means every searched source failed."},
		Row:   func(*cliInvocation) any { return searchRow{} },
		Run:   (*cliRuntime).runSearch,
	})
	registerCLIVerb(mutationVerbSpec("add", 50))
	registerCLIVerb(mutationVerbSpec("update", 60))
	registerCLIVerb(mutationVerbSpec("remove", 70))
	registerCLIVerb(cliVerbSpec{
		Name: "restore", Order: 80, Summary: "run dotnet restore for explicit targets or --all",
		Description: "Run dotnet restore for each target project. A failed project does not stop the others; the command exits 6 if any failed.",
		Flags: joinFlags(workspaceFlags(), []cliFlagSpec{
			{Name: "file", Value: "PROJECT", Kind: valueFile, Repeatable: true, Description: "restore this discovered project, relative to --project"},
			{Name: "all", Description: "restore every discovered project"},
			{Name: "interactive", Description: "let dotnet prompt for feed authentication"},
		}, outputFlags(), []cliFlagSpec{timeoutFlag("")}, tailFlags()),
		Validate: func(inv *cliInvocation) error {
			if inv.args.bools["all"] == (len(inv.args.values["file"]) > 0) {
				return errors.New("choose exactly one restore scope: repeatable --file or --all")
			}
			return nil
		},
		Row: func(*cliInvocation) any { return restoreRow{} },
		Run: (*cliRuntime).runRestore,
	})
	registerCLIVerb(cliVerbSpec{
		Name: "completion", Order: 90, Summary: "emit completion for bash, zsh, or fish",
		Description: "Print a shell completion script. Completion covers commands, each command's flags, and flag values with fixed choices.\n\n  bash: source <(guget completion bash)\n  zsh:  source <(guget completion zsh)\n  fish: guget completion fish | source",
		Positionals: []cliPositionalSpec{{Name: "SHELL", Kind: valueChoice, Choices: completionShells}},
		Flags:       tailFlags(),
		Run:         (*cliRuntime).runCompletion,
	})
	registerCLIVerb(cliVerbSpec{
		Name: "self-update", Order: 100, Summary: "update the Guget executable (--check to inspect only)",
		Description: "Replace this Guget executable with the latest stable release.",
		Flags:       joinFlags([]cliFlagSpec{{Name: "check", Description: "report whether a newer release exists without installing it"}}, tailFlags()),
		Run: func(r *cliRuntime, ctx context.Context, inv *cliInvocation) ExitCode {
			return r.runSelfUpdateWith(ctx, inv, selfupdate.New(version, nil), os.Executable)
		},
	})
	registerCLIVerb(cliVerbSpec{
		Name: "version", Order: 110, Summary: "print the Guget version",
		Flags: tailFlags(),
		Run: func(r *cliRuntime, _ context.Context, _ *cliInvocation) ExitCode {
			fmt.Fprintln(r.stdout, versionString())
			return ExitSuccess
		},
	})
	registerCLIVerb(cliVerbSpec{
		Name: "help", Order: 120, Summary: "show help for Guget or one command",
		Positionals: []cliPositionalSpec{{Name: "COMMAND", Optional: true, Kind: valueChoice, ChoicesFunc: cliVerbNames}},
		Flags:       tailFlags(),
		Run:         (*cliRuntime).runHelp,
	})
}

func mutationVerbSpec(verb string, order int) cliVerbSpec {
	spec := cliVerbSpec{Name: verb, Order: order, Positionals: []cliPositionalSpec{{Name: "PACKAGE"}}}
	scope := []cliFlagSpec{{Name: "file", Value: "PROJECT", Kind: valueFile, Repeatable: true, Description: "edit this discovered project, relative to --project"}}
	selectors := []cliFlagSpec{
		{Name: "version", Value: "VERSION", Description: "the version or NuGet version range to write"},
	}
	var extra []cliFlagSpec
	switch verb {
	case "add":
		spec.Summary = "add a package to explicit --file targets"
		spec.Description = "Add PACKAGE to each --file project. In a central package management workspace the version goes to Directory.Packages.props."
		selectors = append(selectors,
			cliFlagSpec{Name: "latest-compatible", Description: "select the newest compatible version (not available for add; exits 4)"},
			cliFlagSpec{Name: "latest-stable", Description: "select the newest stable version (not available for add; exits 4)"})
	case "update":
		spec.Summary = "update explicit --file targets or --all"
		spec.Description = "Update the version of PACKAGE where it is owned. --all covers only projects that reference PACKAGE. Exact pins such as [1.2.3] are skipped by --all and refused for --file unless --include-pinned is given."
		selectors = append(selectors,
			cliFlagSpec{Name: "latest", Description: "write each project's update target under the update-target policy; a project with nothing newer is left unchanged"},
			cliFlagSpec{Name: "latest-compatible", Description: "alias of --latest --update-target compatible-stable"},
			cliFlagSpec{Name: "latest-stable", Description: "alias of --latest --update-target stable, the version dotnet package update chooses"},
			flagUpdateTarget)
		spec.Notes = append(spec.Notes, "Without --timeout, fetching metadata for --latest stops after 30s. Exit status 3 means no source supplied PACKAGE; 7 means some sources failed and the choice comes from the rest.")
		scope = append(scope, cliFlagSpec{Name: "all", Description: "every discovered project that references PACKAGE"})
		extra = append(extra, cliFlagSpec{Name: "include-pinned", Description: "also update exact [x.y.z] pins"})
	case "remove":
		spec.Summary = "remove from explicit --file targets or --all"
		spec.Description = "Remove the PACKAGE reference from each target project. A shared central version stays in place."
		scope = append(scope, cliFlagSpec{Name: "all", Description: "every discovered project that references PACKAGE"})
		selectors = nil
	}
	spec.Flags = joinFlags(workspaceFlags(), scope, selectors, extra, []cliFlagSpec{
		{Name: "dry-run", Description: "print the planned changes without writing files"},
		{Name: "restore", Description: "run dotnet restore for the targets after a successful edit"},
		{Name: "interactive", Description: "let dotnet restore prompt for feed authentication; requires --restore"},
	}, outputFlags(), []cliFlagSpec{timeoutFlag("")}, tailFlags())
	spec.Notes = append([]string{"A dry run prints one row per planned file change. Applying prints the same rows as a table or TSV, and one result document for json, jsonl, and templates."}, spec.Notes...)
	spec.Validate = func(inv *cliInvocation) error { return validateMutation(verb, inv) }
	spec.Row = func(inv *cliInvocation) any {
		if inv.args.bools["dry-run"] {
			return planRow{}
		}
		return planDocument{}
	}
	spec.Run = func(r *cliRuntime, ctx context.Context, inv *cliInvocation) ExitCode {
		return r.runMutation(ctx, verb, inv)
	}
	return spec
}

func validateMutation(verb string, inv *cliInvocation) error {
	args := inv.args
	files := args.values["file"]
	if verb == "add" && len(files) == 0 {
		return errors.New("guget add requires one or more explicit --file targets")
	}
	if args.bools["all"] == (len(files) > 0) {
		return errors.New("choose exactly one mutation scope: repeatable --file or --all")
	}
	if args.bools["interactive"] && !args.bools["restore"] {
		return errors.New("--interactive requires --restore for mutation commands")
	}
	if verb == "remove" {
		return nil
	}
	selectors := 0
	for _, set := range []bool{args.value("version", "") != "", args.bools["latest"], args.bools["latest-compatible"], args.bools["latest-stable"]} {
		if set {
			selectors++
		}
	}
	if selectors != 1 {
		if verb == "update" {
			return errors.New("choose exactly one version selector: --version, --latest, --latest-compatible, or --latest-stable")
		}
		return errors.New("choose exactly one version selector: --version, --latest-compatible, or --latest-stable")
	}
	if args.value("update-target", "") != "" && !args.bools["latest"] {
		return errors.New("--update-target applies only to --latest; --latest-compatible and --latest-stable name their own target")
	}
	if version := args.value("version", ""); version != "" {
		if err := projectedit.ValidateVersion(version); err != nil {
			return fmt.Errorf("invalid --version: %v", err)
		}
	}
	return nil
}

func cliScanOptions(args cliArgs) workspaceScanOptions {
	return workspaceScanOptions{IncludeIgnored: args.bools[includeIgnoredFlag]}
}

type cliRuntime struct {
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	newService func(context.Context, NugetSource) (*NugetService, error)
	runCommand func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error
	// runner runs dotnet for MSBuild evaluation and package graphs; nil runs
	// the real SDK. Tests inject a fake.
	runner packageops.ProcessRunner
}

func runCLICommand(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func dispatchCLI(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) ExitCode {
	runtime := &cliRuntime{stdin: stdin, stdout: stdout, stderr: stderr}
	return runtime.dispatch(ctx, argv)
}

// dispatch runs one verb: help, argument validation, the verb itself, then the
// shared exit mapping for timeouts and interrupts and the JSON error document.
func (r *cliRuntime) dispatch(ctx context.Context, argv []string) ExitCode {
	if r.newService == nil {
		r.newService = NewNugetServiceContext
	}
	if r.runCommand == nil {
		r.runCommand = runCLICommand
	}
	if r.stdin == nil {
		r.stdin = strings.NewReader("")
	}
	logSetOutput(r.stderr)
	logSetLevel(LogLevelNone)
	logSetColor(false)

	if len(argv) == 0 {
		fmt.Fprint(r.stderr, cliUsage())
		return ExitUsage
	}
	spec := lookupCLIVerb(argv[0])
	if spec == nil {
		fmt.Fprintf(r.stderr, "guget: unknown command %q\n\n%s", argv[0], cliUsage())
		return ExitUsage
	}
	if wantsHelp(argv[1:]) {
		fmt.Fprint(r.stdout, verbHelp(spec))
		return ExitSuccess
	}
	inv, err := r.prepareInvocation(spec, argv[1:])
	if err != nil {
		return r.usageError(spec, err)
	}
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if inv.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, inv.timeout)
	}
	defer cancel()
	code := ExitInterrupted
	if ctx.Err() == nil {
		code = spec.Run(r, runCtx, inv)
	}
	return r.finish(ctx, runCtx, inv, code)
}

func (r *cliRuntime) usageError(spec *cliVerbSpec, err error) ExitCode {
	fmt.Fprintf(r.stderr, "guget %s: %v\nRun \"guget %s --help\" for usage.\n", spec.Name, err, spec.Name)
	return ExitUsage
}

// finish maps an aborted run to 130 (interrupt) or 124 (--timeout), then makes
// sure a JSON caller receives a document for every failure.
func (r *cliRuntime) finish(parent, runCtx context.Context, inv *cliInvocation, code ExitCode) ExitCode {
	if code != ExitSuccess && code != ExitUsage {
		switch {
		case parent.Err() != nil:
			code = ExitInterrupted
			inv.failure = "interrupted"
			fmt.Fprintln(r.stderr, "guget: interrupted")
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			code = ExitTimeout
			inv.failure = "timed out after " + inv.timeout.String()
			fmt.Fprintf(r.stderr, "guget: %s\n", inv.failure)
		}
	}
	if code != ExitSuccess && inv.format.kind == formatJSON && inv.spec.hasFlag("format") && !inv.written {
		inv.writeErrorDocument(code)
	}
	return code
}

// cliDocHeader starts every JSON document, so schemaVersion is always first.
type cliDocHeader struct {
	SchemaVersion int     `json:"schemaVersion"`
	Command       string  `json:"command"`
	Status        string  `json:"status"`
	Error         *string `json:"error"`
}

func newDocHeader(command, status string) cliDocHeader {
	return cliDocHeader{SchemaVersion: 1, Command: command, Status: status}
}

func (h *cliDocHeader) header() *cliDocHeader { return h }

type cliDocument interface{ header() *cliDocHeader }

// cliErrorDocument is the result when a command fails before it has a result
// document of its own.
type cliErrorDocument struct {
	cliDocHeader
	Warnings []string `json:"warnings"`
}

func failureStatus(code ExitCode) string {
	switch code {
	case ExitUsage:
		return "invalid"
	case ExitRefused:
		return "refused"
	case ExitPartial:
		return "partial"
	case ExitTimeout:
		return "timed-out"
	case ExitInterrupted:
		return "interrupted"
	default:
		return "failed"
	}
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// fail records a failure for the JSON error document and reports it on stderr.
func (inv *cliInvocation) fail(code ExitCode, format string, args ...any) ExitCode {
	inv.failure = fmt.Sprintf(format, args...)
	fmt.Fprintf(inv.runtime.stderr, "guget: %s\n", redactCredentials(inv.failure))
	return code
}

func (inv *cliInvocation) writeErrorDocument(code ExitCode) {
	document := inv.document
	if document == nil {
		document = &cliErrorDocument{cliDocHeader: newDocHeader(inv.spec.Name, ""), Warnings: []string{}}
	}
	header := document.header()
	header.Status = failureStatus(code)
	message := inv.failure
	if message == "" {
		message = fmt.Sprintf("guget %s failed with exit code %d", inv.spec.Name, code)
	}
	message = redactCredentials(message)
	header.Error = &message
	data, err := renderCLIResult(cliFormat{kind: formatJSON}, false, cliResult{Document: document})
	if err != nil {
		return
	}
	if inv.blocked {
		// The requested output was refused; report the failure on stdout.
		inv.outputPath = ""
	}
	inv.write(data)
}

// emit renders a result in the selected format and writes it.
func (inv *cliInvocation) emit(result cliResult) ExitCode {
	data, err := renderCLIResult(inv.format, inv.noHeaders, result)
	if err != nil {
		return inv.fail(ExitUsage, "%v", err)
	}
	return inv.write(data)
}

// emitAfterWrite renders the result of a command that has already changed
// files, such as an applied edit or a restore. A render failure is then an
// output failure (exit 5), not a usage error: the change is in place.
func (inv *cliInvocation) emitAfterWrite(result cliResult) ExitCode {
	data, err := renderCLIResult(inv.format, inv.noHeaders, result)
	if err != nil {
		return inv.fail(ExitWrite, "render output: %v", err)
	}
	return inv.write(data)
}

func (inv *cliInvocation) write(data []byte) ExitCode {
	inv.written = true
	path := inv.outputPath
	if path == "" || path == "-" {
		if _, err := inv.runtime.stdout.Write(data); err != nil {
			return inv.fail(ExitWrite, "write output: %v", err)
		}
		return ExitSuccess
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return inv.fail(ExitWrite, "output path: %v", err)
	}
	if err := atomicfile.WriteFile(abs, data, 0o644); err != nil {
		return inv.fail(ExitWrite, "write %s: %v", abs, err)
	}
	return ExitSuccess
}

// guardOutput refuses an --output file that would overwrite a workspace input
// or cannot be created. It runs before evaluation, network access, or edits.
func (inv *cliInvocation) guardOutput(snapshot *workspaceSnapshot) ExitCode {
	return inv.guardOutputAgainst(workspaceOutputInputs(snapshot))
}

// guardOutputAgainst refuses an --output file that is one of inputs.
func (inv *cliInvocation) guardOutputAgainst(inputs []string) ExitCode {
	if err := preflightOutputPath(inv.outputPath, inputs); err != nil {
		inv.blocked = true
		return inv.fail(ExitWrite, "output unavailable: %v", err)
	}
	return ExitSuccess
}

func (r *cliRuntime) emitWarnings(warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(r.stderr, "guget: warning: %s\n", redactCredentials(warning))
	}
}

func (r *cliRuntime) runHelp(_ context.Context, inv *cliInvocation) ExitCode {
	if len(inv.args.positionals) == 0 {
		fmt.Fprint(r.stdout, cliUsage())
		return ExitSuccess
	}
	spec := lookupCLIVerb(inv.args.positionals[0])
	if spec == nil {
		fmt.Fprintf(r.stderr, "guget: unknown command %q\n", inv.args.positionals[0])
		return ExitUsage
	}
	fmt.Fprint(r.stdout, verbHelp(spec))
	return ExitSuccess
}

func (r *cliRuntime) runCompletion(_ context.Context, inv *cliInvocation) ExitCode {
	script, ok := completionScript(inv.args.positionals[0])
	if !ok {
		return r.usageError(inv.spec, fmt.Errorf("unsupported shell %q", inv.args.positionals[0]))
	}
	fmt.Fprint(r.stdout, script)
	return ExitSuccess
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

// loadCLIWorkspace resolves --project, scans it, and guards --output. A failed
// step has already been reported when the returned code is not success.
func (r *cliRuntime) loadCLIWorkspace(ctx context.Context, inv *cliInvocation, setWorkspace func(string)) (*workspaceSnapshot, ExitCode) {
	root, err := workspaceRoot(inv.args)
	if err != nil {
		return nil, inv.fail(ExitWorkspace, "%v", err)
	}
	setWorkspace(root)
	snapshot, err := scanWorkspaceWith(root, cliScanOptions(inv.args))
	if ctx.Err() != nil {
		return nil, ExitInterrupted
	}
	if err != nil {
		return nil, inv.fail(ExitWorkspace, "%v", err)
	}
	setWorkspace(snapshot.ProjectDir)
	if code := inv.guardOutput(snapshot); code != ExitSuccess {
		return nil, code
	}
	return snapshot, ExitSuccess
}

// pathDisplay maps canonical file identities to the absolute path form the
// workspace scan used, so one file is reported the same way by every command.
// Symlinks stay unresolved in output; canonical identities stay internal.
type pathDisplay map[string]string

func newPathDisplay(snapshot *workspaceSnapshot) pathDisplay {
	display := make(pathDisplay)
	add := func(path string) {
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		abs = filepath.Clean(abs)
		identity := outputPathIdentity(abs)
		if _, ok := display[identity]; !ok {
			display[identity] = abs
		}
	}
	for _, project := range snapshot.ParsedProjects {
		add(project.FilePath)
	}
	for _, project := range append(append([]*ParsedProject(nil), snapshot.ParsedProjects...), snapshot.PropsProjects...) {
		for _, path := range projectInputPaths(project) {
			add(path)
		}
	}
	for path := range snapshot.InputRevisions {
		add(path)
	}
	return display
}

func (d pathDisplay) show(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	abs = filepath.Clean(abs)
	if display, ok := d[outputPathIdentity(abs)]; ok {
		return display
	}
	return abs
}

func (d pathDisplay) showAll(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		result = append(result, d.show(path))
	}
	return result
}

// cliPackageRow is one (project, package, target framework) reference. Every
// key is always present; unknown values are null.
type cliPackageRow struct {
	Project             string  `json:"project"`
	ProjectPath         string  `json:"projectPath"`
	Package             string  `json:"package"`
	RequestedExpression *string `json:"requestedExpression"`
	ResolvedVersion     *string `json:"resolvedVersion"`
	// OwnerPath is a documented alias of VersionOwner kept for schema 1.
	OwnerPath        *string  `json:"ownerPath"`
	ReferenceOwner   *string  `json:"referenceOwner"`
	VersionOwner     *string  `json:"versionOwner"`
	TargetFramework  *string  `json:"targetFramework"`
	TargetFrameworks []string `json:"targetFrameworks"`
	Direct           bool     `json:"direct"`
	Implicit         bool     `json:"implicit"`
	EditSupported    bool     `json:"editSupported"`
	EditReason       *string  `json:"editReason"`
	Locked           bool     `json:"locked"`
	// Global marks a GlobalPackageReference, which NuGet references with
	// PrivateAssets=All in every project importing its file.
	Global        bool    `json:"global"`
	PrivateAssets *string `json:"privateAssets"`
	// Download marks a restore-only PackageDownload, which is not a
	// dependency and which guget does not edit.
	Download bool `json:"download"`
	// Assessment fields (update_target.go). They are null (or []) until
	// package metadata is fetched (--metadata, --outdated, --vulnerable,
	// --deprecated), and stay so for PackageDownload rows and packages whose
	// metadata could not be fetched.
	UpdateTarget              *string       `json:"updateTarget"`
	UpdateTargetCompatibility *string       `json:"updateTargetCompatibility"`
	LatestStable              *string       `json:"latestStable"`
	LatestVersion             *string       `json:"latestVersion"`
	Vulnerable                *bool         `json:"vulnerable"`
	Advisories                []cliAdvisory `json:"advisories"`
	Deprecated                *bool         `json:"deprecated"`
	DeprecationReasons        []string      `json:"deprecationReasons"`
	AlternatePackage          *string       `json:"alternatePackage"`
}

// cliAdvisory is one known vulnerability of the row's version.
type cliAdvisory struct {
	Severity string `json:"severity"`
	URL      string `json:"url"`
}

const downloadEditReason = "PackageDownload is restore-only and is not a dependency; guget does not edit it"

type listDocument struct {
	cliDocHeader
	Workspace string `json:"workspace"`
	// UpdateTarget is the update-target policy the assessment fields use, or
	// null when package metadata was not fetched.
	UpdateTarget *string         `json:"updateTarget"`
	Packages     []cliPackageRow `json:"packages"`
	Warnings     []string        `json:"warnings"`
}

// assessedPackageColumns are the table and TSV columns once metadata is fetched.
var assessedPackageColumns = []cliColumn{
	{"PROJECT", "project"}, {"PACKAGE", "package"}, {"REQUESTED", "requestedExpression"},
	{"RESOLVED", "resolvedVersion"}, {"UPDATE TARGET", "updateTarget"}, {"LATEST STABLE", "latestStable"},
	{"LATEST", "latestVersion"}, {"VULNERABLE", "vulnerable"}, {"DEPRECATED", "deprecated"},
}

var packageColumns = []cliColumn{
	{"PROJECT", "project"}, {"PACKAGE", "package"}, {"REQUESTED", "requestedExpression"},
	{"RESOLVED", "resolvedVersion"}, {"VERSION OWNER", "versionOwner"}, {"FRAMEWORKS", "targetFrameworks"},
}

func newPackageRow(project *ParsedProject, packageID, framework string) cliPackageRow {
	frameworks := []string{}
	if framework != "" {
		frameworks = []string{framework}
	}
	return cliPackageRow{
		Project: project.FileName, ProjectPath: project.FilePath, Package: packageID,
		TargetFramework: stringPtr(framework), TargetFrameworks: frameworks,
		Advisories: []cliAdvisory{}, DeprecationReasons: []string{},
	}
}

func (row *cliPackageRow) setOwners(reference, version string) {
	row.ReferenceOwner = stringPtr(reference)
	row.VersionOwner = stringPtr(version)
	row.OwnerPath = row.VersionOwner
}

// packageRows inspects each project with MSBuild and the restored package
// graph, falling back to declared references when evaluation is unavailable.
// partial reports that some requested data could not be produced.
func (r *cliRuntime) packageRows(ctx context.Context, snapshot *workspaceSnapshot, onlyPackage string, includeTransitive bool) (rows []cliPackageRow, warnings []string, partial bool) {
	rows = make([]cliPackageRow, 0)
	warnings = make([]string, 0)
	paths := newPathDisplay(snapshot)
	msbuild := packageops.MSBuildInspector{Runner: r.runner}
	graph := packageops.NuGetGraphInspector{Runner: r.runner}
	matches := func(id string) bool { return onlyPackage == "" || strings.EqualFold(id, onlyPackage) }
	for _, project := range snapshot.ParsedProjects {
		if ctx.Err() != nil {
			return rows, warnings, true
		}
		before := len(rows)
		evaluated, evalErr := msbuild.InspectProject(ctx, project.FilePath)
		resolved, graphErr := graph.ResolveProject(ctx, project.FilePath, includeTransitive)
		resolvedByKey := make(map[string]packageops.PackageUse)
		for _, use := range resolved {
			resolvedByKey[packageUseKey(use.PackageID, use.TargetFramework, use.Direct)] = use
		}
		if evalErr == nil {
			for _, use := range evaluated.PackageUses {
				if !matches(use.PackageID) {
					continue
				}
				resolvedVersion := ""
				if graphUse, ok := resolvedByKey[packageUseKey(use.PackageID, use.TargetFramework, true)]; ok {
					resolvedVersion = graphUse.ResolvedVersion
				}
				if graphErr == nil && resolvedVersion == "" && !use.Implicit {
					partial = true
					warnings = append(warnings, fmt.Sprintf("%s: resolved version for %s (%s) is missing; restore metadata is incomplete", project.FileName, use.PackageID, use.TargetFramework))
				}
				row := newPackageRow(project, use.PackageID, use.TargetFramework)
				row.RequestedExpression = stringPtr(use.EvaluatedExpression)
				row.ResolvedVersion = stringPtr(resolvedVersion)
				row.setOwners(paths.show(use.ReferenceOwner), paths.show(use.VersionOwner))
				row.Direct, row.Implicit = true, use.Implicit
				row.EditSupported, row.EditReason = use.Edit.Supported, stringPtr(use.Edit.Reason)
				row.Locked = isExactLock(use.EvaluatedExpression)
				row.Global, row.PrivateAssets = use.Global, stringPtr(use.PrivateAssets)
				rows = append(rows, row)
			}
			for _, download := range evaluated.PackageDownloads {
				if !matches(download.PackageID) {
					continue
				}
				row := newPackageRow(project, download.PackageID, download.TargetFramework)
				row.RequestedExpression = stringPtr(download.Version)
				row.setOwners(paths.show(download.Owner), paths.show(download.Owner))
				row.EditReason = stringPtr(downloadEditReason)
				row.Locked, row.Download = isExactLock(download.Version), true
				rows = append(rows, row)
			}
		} else {
			frameworks := make([]string, 0, project.TargetFrameworks.Len())
			for framework := range project.TargetFrameworks {
				frameworks = append(frameworks, framework.String())
			}
			sort.Strings(frameworks)
			if len(frameworks) == 0 {
				frameworks = []string{""}
			}
			declared := 0
			for ref := range project.Packages {
				if !matches(ref.Name) {
					continue
				}
				declared++
				requested := ref.Requested
				if requested == "" {
					requested = ref.Version.String()
				}
				owner := paths.show(project.SourceFileForPackage(ref.Name))
				referenceOwner := paths.show(project.ReferenceFileForPackage(ref.Name))
				for _, framework := range frameworks {
					row := newPackageRow(project, ref.Name, framework)
					row.RequestedExpression = stringPtr(requested)
					if graphUse, ok := resolvedByKey[packageUseKey(ref.Name, framework, true)]; ok {
						row.ResolvedVersion = stringPtr(graphUse.ResolvedVersion)
					}
					row.setOwners(referenceOwner, owner)
					row.Direct = true
					row.EditReason = stringPtr("MSBuild evaluation unavailable")
					row.Locked = ref.Locked
					row.Global, row.PrivateAssets = ref.Global, stringPtr(ref.PrivateAssets)
					rows = append(rows, row)
				}
			}
			for _, download := range project.Downloads {
				if !matches(download.Name) {
					continue
				}
				for _, framework := range frameworks {
					row := newPackageRow(project, download.Name, framework)
					row.RequestedExpression = stringPtr(download.Requested)
					row.setOwners(paths.show(download.Owner), paths.show(download.Owner))
					row.EditReason = stringPtr(downloadEditReason)
					row.Locked, row.Download = isExactLock(download.Requested), true
					rows = append(rows, row)
				}
			}
			if onlyPackage == "" || declared > 0 {
				partial = true
				warnings = append(warnings, fmt.Sprintf("%s: MSBuild evaluation unavailable; showing declared fallback: %v", project.FileName, evalErr))
			}
		}
		if graphErr != nil && (len(rows) > before || includeTransitive && onlyPackage == "") {
			partial = true
			warnings = append(warnings, fmt.Sprintf("%s: resolved graph unavailable or stale: %v", project.FileName, graphErr))
		}
		if includeTransitive && graphErr == nil {
			for _, use := range resolved {
				if use.Direct || !matches(use.PackageID) {
					continue
				}
				row := newPackageRow(project, use.PackageID, use.TargetFramework)
				row.ResolvedVersion = stringPtr(use.ResolvedVersion)
				row.EditReason = stringPtr("transitive packages are resolved, not directly editable")
				rows = append(rows, row)
			}
		}
	}
	sortPackageRows(rows)
	return rows, warnings, partial
}

// sortPackageRows orders rows by package ID (case-folded), project path, and
// target framework, with direct references before transitive ones.
func sortPackageRows(rows []cliPackageRow) {
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if !strings.EqualFold(a.Package, b.Package) {
			return strings.ToLower(a.Package) < strings.ToLower(b.Package)
		}
		if a.ProjectPath != b.ProjectPath {
			return a.ProjectPath < b.ProjectPath
		}
		if fa, fb := strings.ToLower(text(a.TargetFramework)), strings.ToLower(text(b.TargetFramework)); fa != fb {
			return fa < fb
		}
		if a.Direct != b.Direct {
			return a.Direct
		}
		return a.Package < b.Package
	})
}

func packageUseKey(packageID, framework string, direct bool) string {
	return strings.ToLower(packageID) + "\x00" + strings.ToLower(framework) + fmt.Sprintf("\x00%t", direct)
}

func (r *cliRuntime) runList(ctx context.Context, inv *cliInvocation) ExitCode {
	verb := inv.spec.Name
	args := inv.args
	doc := &listDocument{cliDocHeader: newDocHeader(verb, "ok"), Packages: []cliPackageRow{}, Warnings: []string{}}
	inv.document = doc
	filter := listFilter{outdated: args.bools["outdated"], vulnerable: args.bools["vulnerable"], deprecated: args.bools["deprecated"]}
	snapshot, code := r.loadCLIWorkspace(ctx, inv, func(root string) { doc.Workspace = root })
	if code != ExitSuccess {
		return code
	}
	only := ""
	if verb == "show" {
		only = args.positionals[0]
	}
	rows, warnings, partial := r.packageRows(ctx, snapshot, only, args.bools["include-transitive"])
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
	if verb == "show" && len(rows) == 0 {
		warnings = append(warnings, fmt.Sprintf("package %s is not referenced in this workspace", only))
	}
	if filter.active() || args.bools["metadata"] {
		policy := cliUpdateTarget(args)
		doc.UpdateTarget = stringPtr(string(policy))
		var requests []metadataRequest
		index := make(map[string]int)
		for _, row := range rows {
			if !assessableRow(row) {
				continue
			}
			key := strings.ToLower(row.Package)
			i, seen := index[key]
			if !seen {
				i = len(requests)
				index[key] = i
				requests = append(requests, metadataRequest{ID: row.Package})
			}
			if !slices.Contains(requests[i].Projects, row.ProjectPath) {
				requests[i].Projects = append(requests[i].Projects, row.ProjectPath)
			}
		}
		metaCtx, cancel := metadataContext(ctx, inv)
		metadata := r.fetchCLIMetadata(metaCtx, snapshot, requests)
		timedOut := metadataTimedOut(ctx, metaCtx)
		cancel()
		if ctx.Err() != nil {
			return ExitInterrupted
		}
		if timedOut {
			return inv.fail(ExitTimeout, "timed out after %s fetching package metadata; pass --timeout to allow longer", defaultMetadataTimeout)
		}
		warnings = append(warnings, metadata.warnings...)
		if metadata.allSourcesFailed() {
			doc.Warnings = redactAll(warnings)
			r.emitWarnings(warnings)
			return inv.fail(ExitSource, "every source failed to supply package metadata")
		}
		partial = partial || metadata.failed > 0 || metadata.incomplete
		assessListRows(rows, snapshot, metadata, policy)
		if filter.active() {
			filtered := rows[:0]
			for _, row := range rows {
				if filter.matches(row) {
					filtered = append(filtered, row)
				}
			}
			rows = filtered
		}
	}
	for _, artifact := range snapshot.Unsupported {
		warnings = append(warnings, fmt.Sprintf("%s: %s", artifact.Path, artifact.Reason))
	}
	doc.Packages, doc.Warnings = rows, redactAll(warnings)
	if partial {
		doc.Status = "partial"
	}
	if ctx.Err() != nil {
		return ExitInterrupted
	}
	r.emitWarnings(warnings)
	columns := packageColumns
	if doc.UpdateTarget != nil {
		columns = assessedPackageColumns
	}
	code = inv.emit(cliResult{Document: doc, Items: doc.Packages, Columns: columns})
	if code == ExitSuccess && partial {
		return ExitPartial
	}
	return code
}

func redactAll(values []string) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = redactCredentials(value)
	}
	return result
}

type sourceRow struct {
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	Capability string   `json:"capability"`
	Mapped     bool     `json:"mapped"`
	Patterns   []string `json:"patterns"`
}

type sourcesDocument struct {
	cliDocHeader
	Workspace string      `json:"workspace"`
	Sources   []sourceRow `json:"sources"`
	Warnings  []string    `json:"warnings"`
}

var sourceColumns = []cliColumn{{"NAME", "name"}, {"URL", "url"}, {"CAPABILITY", "capability"}, {"MAPPED PATTERNS", "patterns"}}

func (r *cliRuntime) runSources(ctx context.Context, inv *cliInvocation) ExitCode {
	doc := &sourcesDocument{cliDocHeader: newDocHeader("sources", "ok"), Sources: []sourceRow{}, Warnings: []string{}}
	inv.document = doc
	snapshot, code := r.loadCLIWorkspace(ctx, inv, func(root string) { doc.Workspace = root })
	if code != ExitSuccess {
		return code
	}
	for _, source := range snapshot.Sources {
		capability := "metadata"
		if !source.IsHTTP() {
			capability = "restore-only"
		}
		row := sourceRow{Name: source.Name, URL: redactURL(source.URL), Capability: capability, Patterns: []string{}}
		if patterns := snapshot.SourceMapping.Patterns(source); len(patterns) > 0 {
			row.Patterns = patterns
		}
		row.Mapped = len(row.Patterns) > 0
		doc.Sources = append(doc.Sources, row)
	}
	sort.SliceStable(doc.Sources, func(i, j int) bool {
		return strings.ToLower(doc.Sources[i].Name) < strings.ToLower(doc.Sources[j].Name)
	})
	return inv.emit(cliResult{Document: doc, Items: doc.Sources, Columns: sourceColumns})
}

type searchRow struct {
	Package     string   `json:"package"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Authors     []string `json:"authors"`
	Source      string   `json:"source"`
}

type searchDocument struct {
	cliDocHeader
	Workspace string      `json:"workspace"`
	Query     string      `json:"query"`
	Results   []searchRow `json:"results"`
	Warnings  []string    `json:"warnings"`
}

var searchColumns = []cliColumn{{"PACKAGE", "package"}, {"VERSION", "version"}, {"SOURCE", "source"}, {"DESCRIPTION", "description"}}

func (r *cliRuntime) runSearch(ctx context.Context, inv *cliInvocation) ExitCode {
	query := inv.args.positionals[0]
	doc := &searchDocument{cliDocHeader: newDocHeader("search", "ok"), Query: query, Results: []searchRow{}, Warnings: []string{}}
	inv.document = doc
	snapshot, code := r.loadCLIWorkspace(ctx, inv, func(root string) { doc.Workspace = root })
	if code != ExitSuccess {
		return code
	}
	wantedSources := make(map[string]bool)
	for _, name := range inv.args.values["source"] {
		wantedSources[strings.ToLower(name)] = true
	}
	for _, name := range inv.args.values["source"] {
		found := false
		for _, source := range snapshot.Sources {
			if strings.EqualFold(source.Name, name) {
				found = true
				break
			}
		}
		if !found {
			return inv.fail(ExitUsage, "unknown --source %q; run guget sources to list configured names", name)
		}
	}
	var warnings []string
	seen := make(map[string]bool)
	attempted, failed := 0, 0
	for _, source := range snapshot.Sources {
		if len(wantedSources) > 0 && !wantedSources[strings.ToLower(source.Name)] {
			continue
		}
		if !source.IsHTTP() {
			warnings = append(warnings, fmt.Sprintf("source %s is restore-only and cannot be searched", source.Name))
			continue
		}
		attempted++
		service, err := r.newService(ctx, source)
		if err != nil {
			failed++
			warnings = append(warnings, fmt.Sprintf("source %s unavailable: %v", source.Name, err))
			continue
		}
		results, err := service.SearchContext(ctx, query, 50)
		if err != nil {
			failed++
			warnings = append(warnings, fmt.Sprintf("source %s search failed: %v", source.Name, err))
			continue
		}
		for _, result := range results {
			if !snapshot.SourceMapping.Allows(source.Name, source.URL, result.ID) {
				continue
			}
			key := strings.ToLower(result.ID)
			if seen[key] {
				continue
			}
			seen[key] = true
			authors := append([]string{}, result.Authors...)
			doc.Results = append(doc.Results, searchRow{Package: result.ID, Version: result.Version, Description: result.Description, Authors: authors, Source: source.Name})
		}
	}
	sort.SliceStable(doc.Results, func(i, j int) bool {
		iExact := strings.EqualFold(doc.Results[i].Package, query)
		jExact := strings.EqualFold(doc.Results[j].Package, query)
		if iExact != jExact {
			return iExact
		}
		return strings.ToLower(doc.Results[i].Package) < strings.ToLower(doc.Results[j].Package)
	})
	doc.Warnings = redactAll(warnings)
	r.emitWarnings(warnings)
	if len(wantedSources) > 0 && attempted == 0 {
		return inv.fail(ExitUsage, "none of the requested --source names are HTTP sources that support search")
	}
	if ctx.Err() != nil {
		return ExitInterrupted
	}
	if attempted > 0 && failed == attempted {
		return inv.fail(ExitSource, "every searched source failed")
	}
	partial := failed > 0
	if partial {
		doc.Status = "partial"
	}
	code = inv.emit(cliResult{Document: doc, Items: doc.Results, Columns: searchColumns})
	if code == ExitSuccess && partial {
		return ExitPartial
	}
	return code
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

type planRow struct {
	Operation string `json:"operation"`
	Package   string `json:"package"`
	Version   string `json:"version"`
	Path      string `json:"path"`
}

type planDocument struct {
	cliDocHeader
	Workspace string `json:"workspace"`
	DryRun    bool   `json:"dryRun"`
	// UpdateTarget is the update-target policy --latest used, or null.
	UpdateTarget *string   `json:"updateTarget"`
	Changes      []planRow `json:"changes"`
	Skipped      int       `json:"skipped"`
	Changed      []string  `json:"changed"`
	Restore      string    `json:"restore"`
	RolledBack   []string  `json:"rolledBack"`
	Conflicting  []string  `json:"conflicting"`
	Unrecovered  []string  `json:"unrecovered"`
	Warnings     []string  `json:"warnings"`
}

var planColumns = []cliColumn{{"OPERATION", "operation"}, {"PACKAGE", "package"}, {"VERSION", "version"}, {"PATH", "path"}}

func (r *cliRuntime) runMutation(ctx context.Context, verb string, inv *cliInvocation) ExitCode {
	args := inv.args
	dryRun := args.bools["dry-run"]
	doc := &planDocument{
		cliDocHeader: newDocHeader(verb, "dry-run"), DryRun: dryRun,
		Changes: []planRow{}, Changed: []string{}, Restore: "not-requested",
		RolledBack: []string{}, Conflicting: []string{}, Unrecovered: []string{}, Warnings: []string{},
	}
	if !dryRun {
		doc.Status = "unchanged"
	}
	inv.document = doc
	latest := verb == "update" && (args.bools["latest"] || args.bools["latest-compatible"] || args.bools["latest-stable"])
	if verb == "add" && (args.bools["latest-compatible"] || args.bools["latest-stable"]) {
		return inv.fail(ExitRefused, "add needs an explicit --version; latest-version selection is available for update")
	}
	snapshot, code := r.loadCLIWorkspace(ctx, inv, func(root string) { doc.Workspace = root })
	if code != ExitSuccess {
		return code
	}
	packageID := args.positionals[0]
	targets, err := mutationTargets(snapshot, doc.Workspace, args.values["file"], args.bools["all"], packageID)
	if err != nil {
		return inv.fail(ExitUsage, "%v", err)
	}
	options := mutationOptions{ctx: ctx, runner: r.runner}
	if verb == "update" && !args.bools["include-pinned"] {
		options.skipLocked = args.bools["all"]
		options.refuseLocked = !args.bools["all"]
	}
	partialMetadata := false
	if latest {
		policy := cliUpdateTarget(args)
		doc.UpdateTarget = stringPtr(string(policy))
		metaCtx, cancel := metadataContext(ctx, inv)
		request := metadataRequest{ID: packageID}
		for _, target := range targets {
			request.Projects = append(request.Projects, target.FilePath)
		}
		metadata := r.fetchCLIMetadata(metaCtx, snapshot, []metadataRequest{request})
		timedOut := metadataTimedOut(ctx, metaCtx)
		cancel()
		if ctx.Err() != nil {
			return ExitInterrupted
		}
		if timedOut {
			return inv.fail(ExitTimeout, "timed out after %s fetching package metadata; pass --timeout to allow longer", defaultMetadataTimeout)
		}
		doc.Warnings = redactAll(metadata.warnings)
		r.emitWarnings(metadata.warnings)
		// Each project updates from what its own NuGet configuration allows,
		// as restore and dotnet package update resolve each project alone.
		infos := make(map[*ParsedProject]*PackageInfo, len(targets))
		for _, target := range targets {
			info := metadata.forProject(packageID, target.FilePath).Info
			if info == nil && referencesPackage(target, packageID) {
				if len(targets) == 1 {
					return inv.fail(ExitSource, "no source supplied metadata for %s", packageID)
				}
				return inv.fail(ExitSource, "no source that %s may use supplied metadata for %s", target.FileName, packageID)
			}
			infos[target] = info
		}
		partialMetadata = metadata.incomplete
		options.selectVersion = latestVersionSelector(snapshot, func(project *ParsedProject) *PackageInfo {
			if info, ok := infos[project]; ok {
				return info
			}
			return metadata.forProject(packageID, project.FilePath).Info
		}, policy)
	}
	plan, rows, skipped, err := planPackageMutation(verb, packageID, args.value("version", ""), mutationTargetsFor(targets), options)
	if err != nil {
		var evaluation evaluationError
		var pinned pinnedPackageError
		switch {
		case ctx.Err() != nil:
			return ExitInterrupted
		case errors.As(err, &evaluation):
			return inv.fail(ExitWorkspace, "%v", err)
		case errors.As(err, &pinned):
			return inv.fail(ExitRefused, "edit refused: %v; pass --include-pinned to update it", err)
		default:
			return inv.fail(ExitRefused, "edit refused: %v", err)
		}
	}
	paths := newPathDisplay(snapshot)
	for i := range rows {
		rows[i].Path = paths.show(rows[i].Path)
	}
	doc.Changes, doc.Skipped = rows, skipped
	if dryRun {
		if args.bools["restore"] {
			doc.Restore = "skipped-dry-run"
		}
		code := inv.emit(cliResult{Document: doc, Items: doc.Changes, Columns: planColumns})
		if code == ExitSuccess && partialMetadata {
			return ExitPartial
		}
		return code
	}
	if ctx.Err() != nil {
		return ExitInterrupted
	}
	applyResult := cliResult{Document: doc, Items: []*planDocument{doc}, Rows: &doc.Changes, Columns: planColumns}
	result, err := plan.ApplyContext(ctx)
	if err != nil {
		doc.Status = "failed"
		doc.Changed = append(doc.Changed, paths.showAll(result.Changed)...)
		doc.RolledBack = append(doc.RolledBack, paths.showAll(result.RolledBack)...)
		doc.Conflicting = append(doc.Conflicting, paths.showAll(result.Conflicting)...)
		doc.Unrecovered = append(doc.Unrecovered, paths.showAll(result.Unrecovered)...)
		code := inv.fail(ExitWrite, "apply failed: %v", err)
		message := redactCredentials(inv.failure)
		doc.Error = &message
		if len(result.Unrecovered) > 0 {
			fmt.Fprintf(r.stderr, "guget: inspect and recover these files before retrying: %s\n", strings.Join(doc.Unrecovered, ", "))
		}
		if len(result.Conflicting) > 0 {
			fmt.Fprintf(r.stderr, "guget: external edits were preserved; inspect these conflicts before retrying: %s\n", strings.Join(doc.Conflicting, ", "))
		}
		inv.emitAfterWrite(applyResult)
		if ctx.Err() != nil {
			return ExitInterrupted
		}
		return code
	}
	doc.Changed = append(doc.Changed, paths.showAll(result.Changed)...)
	for _, path := range doc.Changed {
		fmt.Fprintf(r.stderr, "%s: changed %s\n", verb, path)
	}
	if len(doc.Changed) == 0 {
		fmt.Fprintln(r.stderr, "no changes needed")
	} else {
		doc.Status = "changed"
	}
	code = ExitSuccess
	if args.bools["restore"] {
		doc.Restore = "skipped"
		if len(doc.Changed) > 0 {
			var restoreRows []restoreRow
			steps := planRestore(snapshot.ParsedProjects, targets, result.Changed)
			describeRestorePlan(r.stderr, steps)
			restoreRows, code = r.restoreProjects(ctx, steps, args.bools["interactive"])
			doc.Restore = "succeeded"
			if code != ExitSuccess {
				doc.Restore = "failed"
				inv.failure = restoreFailure(restoreRows)
				message := inv.failure
				doc.Error = &message
			}
		}
	}
	if outputCode := inv.emitAfterWrite(applyResult); outputCode != ExitSuccess {
		fmt.Fprintln(r.stderr, "guget: package changes were applied; result output failed, inspect files before retrying")
		return outputCode
	}
	if code == ExitSuccess && partialMetadata {
		return ExitPartial
	}
	return code
}

// referencesPackage reports whether the project declares packageID.
func referencesPackage(project *ParsedProject, packageID string) bool {
	for ref := range project.Packages {
		if strings.EqualFold(ref.Name, packageID) {
			return true
		}
	}
	return false
}

func mutationTargetsFor(projects []*ParsedProject) []mutationTarget {
	targets := make([]mutationTarget, 0, len(projects))
	for _, project := range projects {
		targets = append(targets, mutationTarget{project: project})
	}
	return targets
}

// mutationTargets resolves --file paths to discovered projects. With --all it
// returns only the projects that reference packageID, as the TUI does, so an
// unrelated project that needs MSBuild evaluation cannot abort the plan. An
// empty packageID selects every project.
func mutationTargets(snapshot *workspaceSnapshot, root string, files []string, all bool, packageID string) ([]*ParsedProject, error) {
	if all {
		if packageID == "" {
			return append([]*ParsedProject(nil), snapshot.ParsedProjects...), nil
		}
		var targets []*ParsedProject
		for _, target := range declaredPackageTargets(snapshot.ParsedProjects, packageID) {
			targets = append(targets, target.project)
		}
		return targets, nil
	}
	byPath := make(map[string]*ParsedProject, len(snapshot.ParsedProjects))
	for _, project := range snapshot.ParsedProjects {
		abs, err := filepath.Abs(project.FilePath)
		if err != nil {
			return nil, err
		}
		byPath[filepath.Clean(abs)] = project
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

type restoreRow struct {
	Project     string  `json:"project"`
	ProjectPath string  `json:"projectPath"`
	Status      string  `json:"status"`
	Error       *string `json:"error"`
}

type restoreDocument struct {
	cliDocHeader
	Workspace string       `json:"workspace"`
	Projects  []restoreRow `json:"projects"`
	Warnings  []string     `json:"warnings"`
}

var restoreColumns = []cliColumn{{"PROJECT", "project"}, {"STATUS", "status"}, {"PATH", "projectPath"}, {"ERROR", "error"}}

func (r *cliRuntime) runRestore(ctx context.Context, inv *cliInvocation) ExitCode {
	doc := &restoreDocument{cliDocHeader: newDocHeader("restore", "ok"), Projects: []restoreRow{}, Warnings: []string{}}
	inv.document = doc
	snapshot, code := r.loadCLIWorkspace(ctx, inv, func(root string) { doc.Workspace = root })
	if code != ExitSuccess {
		return code
	}
	targets, err := mutationTargets(snapshot, doc.Workspace, inv.args.values["file"], inv.args.bools["all"], "")
	if err != nil {
		return inv.fail(ExitUsage, "%v", err)
	}
	rows, code := r.restoreProjects(ctx, planRestore(snapshot.ParsedProjects, targets, nil), inv.args.bools["interactive"])
	doc.Projects = rows
	if code == ExitInterrupted || ctx.Err() != nil {
		return ExitInterrupted
	}
	if code != ExitSuccess {
		doc.Status = "failed"
		inv.failure = restoreFailure(rows)
		message := inv.failure
		doc.Error = &message
	}
	if outputCode := inv.emitAfterWrite(cliResult{Document: doc, Items: doc.Projects, Columns: restoreColumns}); outputCode != ExitSuccess {
		return outputCode
	}
	return code
}

func restoreFailure(rows []restoreRow) string {
	failed := 0
	for _, row := range rows {
		if row.Status == "failed" {
			failed++
		}
	}
	return fmt.Sprintf("restore failed for %d of %d project(s)", failed, len(rows))
}

// restoreProjects restores every project in order. A failing project does not
// stop the rest; the result is ExitRestore when any failed. An interrupt or
// timeout stops at the current project.
// The steps come from planRestore, which adds projects an edit affects and
// --force-evaluate for locked lock files.
func (r *cliRuntime) restoreProjects(ctx context.Context, steps []restoreStep, interactive bool) ([]restoreRow, ExitCode) {
	runCommand := r.runCommand
	if runCommand == nil {
		runCommand = runCLICommand
	}
	stdin := r.stdin
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	rows := make([]restoreRow, 0, len(steps))
	code := ExitSuccess
	for _, step := range steps {
		project := step.Project
		if ctx.Err() != nil {
			return rows, ExitInterrupted
		}
		row := restoreRow{Project: project.FileName, ProjectPath: project.FilePath, Status: "succeeded"}
		if row.Project == "" {
			row.Project = filepath.Base(project.FilePath)
		}
		args := append([]string(nil), step.Args...)
		if interactive {
			args = append(args, "--interactive")
		}
		if err := runCommand(ctx, "dotnet", args, stdin, r.stderr, r.stderr); err != nil {
			row.Status = "failed"
			row.Error = stringPtr(redactCredentials(err.Error()))
			rows = append(rows, row)
			if ctx.Err() != nil {
				return rows, ExitInterrupted
			}
			fmt.Fprintf(r.stderr, "guget: restore %s: %v\n", project.FilePath, redactCredentials(err.Error()))
			code = ExitRestore
			continue
		}
		rows = append(rows, row)
	}
	return rows, code
}

// workspaceOutputInputs lists the files an --output path must never replace.
// workspaceOutputInputs lists the files an --output file must not replace:
// the workspace inputs, each project's lock file under any name NuGet may
// use, the solution files found, and global.json in the workspace or project
// directories or above them. Paths need not exist.
func workspaceOutputInputs(snapshot *workspaceSnapshot) []string {
	inputs := workspaceInputPaths(snapshot.ProjectDir, snapshot)
	inputs = append(inputs, snapshot.SolutionFiles...)
	dirs := []string{snapshot.ProjectDir}
	for _, project := range snapshot.ParsedProjects {
		inputs = append(inputs, project.FilePath)
		dir := filepath.Dir(project.FilePath)
		dirs = append(dirs, dir)
		if project.LockFile.Path != "" {
			inputs = append(inputs, project.LockFile.Path)
		}
		name := strings.TrimSuffix(filepath.Base(project.FilePath), filepath.Ext(project.FilePath))
		inputs = append(inputs,
			filepath.Join(dir, "packages.lock.json"),
			filepath.Join(dir, "packages."+name+".lock.json"),
			filepath.Join(dir, "packages."+strings.ReplaceAll(name, " ", "_")+".lock.json"))
	}
	seen := make(map[string]bool)
	for _, start := range dirs {
		for dir := start; dir != "" && !seen[dir]; dir = filepath.Dir(dir) {
			seen[dir] = true
			inputs = append(inputs, filepath.Join(dir, "global.json"))
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	return inputs
}

// preflightOutputPath refuses an --output path that is one of inputs, is not a
// regular file, or sits in a directory where Guget cannot create files.
func preflightOutputPath(path string, inputs []string) error {
	if path == "" || path == "-" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	existing, statErr := os.Stat(abs)
	if statErr == nil && !existing.Mode().IsRegular() {
		return fmt.Errorf("output is not a regular file: %s", abs)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	for _, source := range inputs {
		if source == "" {
			continue
		}
		sourceAbs, _ := filepath.Abs(source)
		info, err := os.Stat(sourceAbs)
		if outputPathIdentity(sourceAbs) == outputPathIdentity(abs) || existing != nil && err == nil && os.SameFile(existing, info) {
			return fmt.Errorf("output would overwrite a workspace input: %s", abs)
		}
	}
	temporary, err := os.CreateTemp(filepath.Dir(abs), ".guget-output-check-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	closeErr := temporary.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func outputPathIdentity(path string) string {
	if canonical, err := filepath.EvalSymlinks(path); err == nil {
		return canonical
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(parent, filepath.Base(path))
	}
	return filepath.Clean(path)
}
