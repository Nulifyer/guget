package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

// ─────────────────────────────────────────────
// logBuffer — thread-safe in-memory log sink
// ─────────────────────────────────────────────

type logBuffer struct {
	mu    sync.Mutex
	lines []string
	send  func(tea.Msg)
}

func (b *logBuffer) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n\r")
	if line == "" {
		return len(p), nil
	}
	b.mu.Lock()
	b.lines = append(b.lines, line)
	send := b.send
	b.mu.Unlock()
	if send != nil {
		// Use a goroutine so callers on the Bubbletea event loop goroutine
		// (e.g. log calls inside Update) don't deadlock p.Send's channel.
		go send(logLineMsg{line: line})
	} else {
		// Before the TUI starts, mirror to stderr so fatal errors are visible.
		fmt.Fprintln(os.Stderr, line)
	}
	return len(p), nil
}

func (b *logBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := make([]string, len(b.lines))
	copy(cp, b.lines)
	return cp
}

// ─────────────────────────────────────────────
// CLI flags
// ─────────────────────────────────────────────

const (
	Flag_NoColor    = "no-color"
	Flag_Verbosity  = "verbosity"
	Flag_ProjectDir = "project"
	Flag_Version    = "version"
	Flag_LogFile    = "log-file"
	Flag_Theme      = "theme"
)

type BuiltFlags struct {
	NoColor    bool
	Verbosity  string
	ProjectDir string
	Version    bool
	LogFile    string
	Theme      string
}

func BuildFlags(flags map[string]IParsedFlag) BuiltFlags {
	return BuiltFlags{
		NoColor:    GetFlag[bool](flags, Flag_NoColor),
		Verbosity:  GetFlag[string](flags, Flag_Verbosity),
		ProjectDir: GetFlag[string](flags, Flag_ProjectDir),
		Version:    GetFlag[bool](flags, Flag_Version),
		LogFile:    GetFlag[string](flags, Flag_LogFile),
		Theme:      GetFlag[string](flags, Flag_Theme),
	}
}

// initCLI registers CLI flags, parses os.Args, and returns the resolved flag values.
// Named initCLI (not Init) to avoid confusion with Model.Init() in the same package.
func initCLI() BuiltFlags {
	logSetLevel(LogLevelWarn)
	// Allow LOG_LEVEL env var to override the pre-parse default; --verbose will
	// override it again after flags are parsed below.
	if envLogLevel := os.Getenv("LOG_LEVEL"); envLogLevel != "" {
		logSetLevel(logParseLevel(envLogLevel))
	}

	RegisterFlag(Flag[bool]{
		Name:        Flag_Version,
		Aliases:     []string{"-V", "--version"},
		Default:     Optional(false),
		Description: "Print the version and exit",
	})
	RegisterFlag(Flag[bool]{
		Name:        Flag_NoColor,
		Aliases:     []string{"-nc", "--no-color"},
		Default:     Optional(false),
		Description: "Disable colored output in the terminal",
	})
	RegisterFlag(Flag[string]{
		Name:           Flag_Verbosity,
		Aliases:        []string{"-v", "--verbose"},
		Default:        Optional("warn"),
		Description:    "Set the logging verbosity level",
		ExpectedValues: []string{"", "none", "error", "err", "warn", "warning", "info", "debug", "dbg", "trace", "trc"},
	})
	RegisterFlag(Flag[string]{
		Name:    Flag_ProjectDir,
		Aliases: []string{"-p", "--project"},
		DefaultFunc: func() string {
			dir, err := os.Getwd()
			if err != nil {
				logFatal("Couldn't get current working directory")
			}
			return dir
		},
		Description: "Set the target project directory (defaults to current working directory)",
	})
	RegisterFlag(Flag[string]{
		Name:        Flag_LogFile,
		Aliases:     []string{"-lf", "--log-file"},
		Default:     Optional(""),
		Description: "Write all log output to this file (in addition to the TUI log panel)",
	})
	RegisterFlag(Flag[string]{
		Name:           Flag_Theme,
		Aliases:        []string{"-t", "--theme"},
		Default:        Optional("auto"),
		Description:    "Color theme",
		ExpectedValues: validThemeNames,
	})

	parsedFlags, _ := ParseFlags()
	builtFlags := BuildFlags(parsedFlags)

	logSetLevel(logParseLevel(builtFlags.Verbosity))
	logSetColor(!builtFlags.NoColor)

	if builtFlags.Version {
		fmt.Printf("guget %s\n", version)
		os.Exit(0)
	}

	return builtFlags
}

// ─────────────────────────────────────────────
// nugetResult (shared between main and tui)
// ─────────────────────────────────────────────

type nugetResult struct {
	pkg    *PackageInfo
	source string
	err    error
}

// ─────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────

func main() {
	builtFlags := initCLI()
	initTheme(builtFlags.Theme, builtFlags.NoColor)

	// Redirect logger to an in-memory buffer immediately so that all startup
	// logs are captured for the TUI log panel. Before p.Send is wired up,
	// Write also mirrors to stderr so fatal errors are visible.
	buf := &logBuffer{}
	if builtFlags.LogFile != "" {
		f, err := os.Create(builtFlags.LogFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open log file %q: %v\n", builtFlags.LogFile, err)
			os.Exit(1)
		}
		logSetOutput(io.MultiWriter(buf, f))
	} else {
		logSetOutput(buf)
	}

	fullProjectPath, err := filepath.Abs(builtFlags.ProjectDir)
	if err != nil {
		logFatal("Couldn't get absolute path for project directory: %v", err)
	}
	logInfo("Starting guget with project directory: %s", fullProjectPath)

	// find + parse projects
	projectFiles, err := FindProjectFiles(fullProjectPath)
	if err != nil {
		logFatal("Error finding projects: %v", err)
	}
	logInfo("Found %d project(s)", len(projectFiles))

	var parsedProjects []*ParsedProject
	for _, file := range projectFiles {
		project, err := ParseCsproj(file)
		if err != nil {
			logFatal("Error parsing project %s: %v", file, err)
		}
		parsedProjects = append(parsedProjects, project)
	}

	if len(parsedProjects) == 0 {
		logWarn("No .csproj or .fsproj files found in: %s", fullProjectPath)
		os.Exit(1)
	}

	// detect nuget sources
	sources := DetectSources(fullProjectPath)
	logInfo("Detected %d NuGet source(s)", len(sources))

	var nugetServices []*NugetService
	for _, src := range sources {
		svc, err := NewNugetService(src)
		if err != nil {
			logWarn("Failed to initialise NuGet source [%s]: %v", src.Name, err)
			continue
		}
		nugetServices = append(nugetServices, svc)
	}
	if len(nugetServices) == 0 {
		logFatal("No reachable NuGet sources found")
	}

	// Count distinct packages so the TUI can track loading progress.
	distinctPackages := NewSet[string]()
	for _, project := range parsedProjects {
		for pkg := range project.Packages {
			distinctPackages.Add(pkg.Name)
		}
	}

	m := NewModel(parsedProjects, nugetServices, sources, buf.Lines(), distinctPackages.Len())

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
	)

	// Wire up live log forwarding to the TUI now that the program exists.
	buf.mu.Lock()
	buf.send = p.Send
	buf.mu.Unlock()

	// Restore terminal on SIGINT / SIGTERM so the alt-screen and cursor
	// are always cleaned up even if the user kills the process externally.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		p.Kill()
	}()

	// Fetch package metadata in parallel; send a packageReadyMsg to the TUI
	// as each one resolves so the loading screen shows live progress.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.Kill()
				panic(r)
			}
		}()
		// Always have a nuget.org service available for supplementary lookups,
		// even if the user's nuget.config doesn't include nuget.org as a source.
		var nugetOrgSvc *NugetService
		for _, svc := range nugetServices {
			if strings.EqualFold(svc.SourceName(), "nuget.org") {
				nugetOrgSvc = svc
				break
			}
		}
		if nugetOrgSvc == nil {
			svc, err := NewNugetService(NugetSource{Name: "nuget.org", URL: defaultNugetSource})
			if err == nil {
				nugetOrgSvc = svc
			}
		}

		var wg sync.WaitGroup
		for name := range distinctPackages {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				var info *PackageInfo
				var sourceName string
				var lastErr error
				for _, svc := range nugetServices {
					info, lastErr = svc.SearchExact(name)
					if lastErr == nil {
						sourceName = svc.SourceName()
						break
					}
					logDebug("Source [%s] failed for %s: %v", svc.SourceName(), name, lastErr)
				}

				// Enrich from nuget.org when the winning source is a private feed.
				// Merges vulnerabilities, downloads, verification, and ProjectURL.
				if info != nil && !strings.EqualFold(sourceName, "nuget.org") && nugetOrgSvc != nil {
					if nugetInfo, err := nugetOrgSvc.SearchExact(name); err == nil {
						info.NugetOrgURL = "https://www.nuget.org/packages/" + nugetInfo.ID
						enrichFromNugetOrg(info, nugetInfo)
					}
				}

				p.Send(packageReadyMsg{
					name:   name,
					result: nugetResult{pkg: info, source: sourceName, err: lastErr},
				})
			}(name)
		}
		wg.Wait()
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

// ─────────────────────────────────────────────
// Helper Functions
// ─────────────────────────────────────────────

// enrichFromNugetOrg merges vulnerability, download, verification, and
// ProjectURL data from a nuget.org lookup into a PackageInfo that was
// originally fetched from a private feed.
func enrichFromNugetOrg(info, nugetInfo *PackageInfo) {
	// Build a version→vulnerabilities map from nuget.org data.
	nugetVulns := make(map[string][]PackageVulnerability, len(nugetInfo.Versions))
	for _, v := range nugetInfo.Versions {
		if len(v.Vulnerabilities) > 0 {
			nugetVulns[v.SemVer.String()] = v.Vulnerabilities
		}
	}

	// Build a version→downloads map from nuget.org data.
	nugetDL := make(map[string]int, len(nugetInfo.Versions))
	for _, v := range nugetInfo.Versions {
		if v.Downloads > 0 {
			nugetDL[v.SemVer.String()] = v.Downloads
		}
	}

	for i := range info.Versions {
		key := info.Versions[i].SemVer.String()
		if len(info.Versions[i].Vulnerabilities) == 0 {
			if vulns, ok := nugetVulns[key]; ok {
				info.Versions[i].Vulnerabilities = vulns
			}
		}
		if info.Versions[i].Downloads == 0 {
			if dl, ok := nugetDL[key]; ok {
				info.Versions[i].Downloads = dl
			}
		}
	}

	if info.TotalDownloads == 0 {
		info.TotalDownloads = nugetInfo.TotalDownloads
	}
	if !info.Verified {
		info.Verified = nugetInfo.Verified
	}
	if info.ProjectURL == "" {
		info.ProjectURL = nugetInfo.ProjectURL
	}
}

func FindProjectFiles(rootDir string) ([]string, error) {
	ignoreDirs := []string{
		// Node / front-end
		"node_modules", "bower_components", "dist", "build", ".next",

		// .NET / typical build outputs
		"bin", "obj", "packages", ".nuget",

		// Version control / metadata
		".git", ".hg", ".svn", ".gitlab", ".github",

		// IDE / editor dirs
		".vs", ".idea", ".vscode",

		// Python / virtualenvs
		".venv", "venv", "env",

		// Java / other build caches
		".gradle", "target",

		// General caches / temp / vendor
		".cache", "tmp", "temp", "vendor", "coverage",

		// Static/web folders that commonly contain lots of assets
		"wwwroot", "public", "www",

		// Other common folders
		"out",
	}

	ignore := make(map[string]struct{}, len(ignoreDirs))
	for _, d := range ignoreDirs {
		ignore[strings.ToLower(d)] = struct{}{}
	}

	var projects []string
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// If this is a directory and its name is in the ignore set, skip it entirely.
		if d.IsDir() {
			if _, ok := ignore[strings.ToLower(d.Name())]; ok {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext == ".csproj" || ext == ".fsproj" {
			projects = append(projects, path)
		}
		return nil
	})

	return projects, err
}
