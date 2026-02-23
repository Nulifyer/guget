package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"arger"
	"logger"

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
		// (e.g. logger calls inside Update) don't deadlock p.Send's channel.
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

// -------------------------------
// Setup & CLI Flags
// --------------------------------
const (
	Flag_NoColor    = "no-color"
	Flag_Verbosity  = "verbosity"
	Flag_ProjectDir = "project"
	Flag_Version    = "version"
)

type BuiltFlags struct {
	NoColor    bool
	Verbosity  string
	ProjectDir string
	Version    bool
}

func BuildFlags(flags map[string]arger.IParsedFlag) BuiltFlags {
	return BuiltFlags{
		NoColor:    arger.Get[bool](flags, Flag_NoColor),
		Verbosity:  arger.Get[string](flags, Flag_Verbosity),
		ProjectDir: arger.Get[string](flags, Flag_ProjectDir),
		Version:    arger.Get[bool](flags, Flag_Version),
	}
}

func Init() BuiltFlags {
	logger.SetColor(false)
	logger.SetLevel(logger.LevelWarn)
	env_log_level := os.Getenv("LOG_LEVEL")
	if env_log_level != "" {
		logger.SetLevel(logger.ParseLevel(env_log_level))
	}

	arger.RegisterFlag(arger.Flag[bool]{
		Name:        Flag_Version,
		Aliases:     []string{"-V", "--version"},
		Default:     arger.Optional(false),
		Description: "Print the version and exit",
	})
	arger.RegisterFlag(arger.Flag[bool]{
		Name:        Flag_NoColor,
		Aliases:     []string{"-nc", "--no-color"},
		Default:     arger.Optional(false),
		Description: "Disable colored output in the terminal",
	})
	arger.RegisterFlag(arger.Flag[string]{
		Name:           Flag_Verbosity,
		Aliases:        []string{"-v", "--verbose"},
		Default:        arger.Optional("warn"),
		Description:    "Set the logging verbosity level",
		ExpectedValues: []string{"", "none", "error", "err", "warn", "warning", "info", "debug", "dbg", "trace", "trc"},
	})
	arger.RegisterFlag(arger.Flag[string]{
		Name:    Flag_ProjectDir,
		Aliases: []string{"-p", "--project"},
		DefaultFunc: func() string {
			dir, err := os.Getwd()
			if err != nil {
				logger.Fatal("Couldn't get current working directory")
			}
			return dir
		},
		Description: "Set the target project directory (defaults to current working directory)",
	})

	parsedFlags, _ := arger.Parse()
	builtFlags := BuildFlags(parsedFlags)

	logger.SetLevel(logger.ParseLevel(builtFlags.Verbosity))
	logger.SetColor(!builtFlags.NoColor)

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

// -------------------------------
// Main
// --------------------------------
func main() {
	builtFlags := Init()

	// Redirect logger to an in-memory buffer immediately so that all startup
	// logs are captured for the TUI log panel. Before p.Send is wired up,
	// Write also mirrors to stderr so fatal errors are still visible.
	buf := &logBuffer{}
	logger.SetOutput(buf)

	fullProjectPath, err := filepath.Abs(builtFlags.ProjectDir)
	if err != nil {
		logger.Fatal("Couldn't get absolute path for project directory: %v", err)
	}
	logger.Info("Starting GoNugetTui with project directory: %s", fullProjectPath)

	// find + parse projects
	projectFiles, err := FindProjectFiles(fullProjectPath)
	if err != nil {
		logger.Fatal("Error finding projects: %v", err)
	}
	logger.Info("Found %d project(s)", len(projectFiles))

	var parsedProjects []*ParsedProject
	for _, file := range projectFiles {
		project, err := ParseCsproj(file)
		if err != nil {
			logger.Fatal("Error parsing project %s: %v", file, err)
		}
		parsedProjects = append(parsedProjects, project)
	}

	if len(parsedProjects) == 0 {
		logger.Warn("No .csproj or .fsproj files found in: %s", fullProjectPath)
		os.Exit(1)
	}

	// detect nuget sources
	sources := DetectSources(fullProjectPath)
	logger.Info("Detected %d NuGet source(s)", len(sources))

	var nugetServices []*NugetService
	for _, src := range sources {
		svc, err := NewNugetService(src)
		if err != nil {
			logger.Warn("Failed to initialise NuGet source [%s]: %v", src.Name, err)
			continue
		}
		nugetServices = append(nugetServices, svc)
	}
	if len(nugetServices) == 0 {
		logger.Fatal("No reachable NuGet sources found")
	}

	// launch TUI — fetching happens as a background cmd
	m := NewModel(parsedProjects, nugetServices, builtFlags.NoColor, buf.Lines())

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
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

	// fetch packages in background, send results to TUI when done
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.Kill() // restore terminal before the process crashes
				panic(r)
			}
		}()
		distinctPackages := NewSet[string]()
		for _, project := range parsedProjects {
			for pkg := range project.Packages {
				distinctPackages.Add(pkg.Name)
			}
		}

		var mu sync.Mutex
		var wg sync.WaitGroup
		results := make(map[string]nugetResult, distinctPackages.Len())

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
					logger.Debug("Source [%s] failed for %s: %v", svc.SourceName(), name, lastErr)
				}
				mu.Lock()
				results[name] = nugetResult{pkg: info, source: sourceName, err: lastErr}
				mu.Unlock()
			}(name)
		}
		wg.Wait()

		p.Send(resultsReadyMsg{results: results})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

// -------------------------------
// Helper Functions
// --------------------------------
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
