package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"arger"
	"logger"

	tea "github.com/charmbracelet/bubbletea"
)

// -------------------------------
// Setup & CLI Flags
// --------------------------------
const (
	Flag_NoColor    = "no-color"
	Flag_Verbosity  = "verbosity"
	Flag_ProjectDir = "project"
)

type BuiltFlags struct {
	NoColor    bool
	Verbosity  string
	ProjectDir string
}

func BuildFlags(flags map[string]arger.IParsedFlag) BuiltFlags {
	return BuiltFlags{
		NoColor:    arger.Get[bool](flags, Flag_NoColor),
		Verbosity:  arger.Get[string](flags, Flag_Verbosity),
		ProjectDir: arger.Get[string](flags, Flag_ProjectDir),
	}
}

func Init() BuiltFlags {
	logger.SetColor(false)
	env_log_level := os.Getenv("LOG_LEVEL")
	if env_log_level != "" {
		logger.SetLevel(logger.ParseLevel(env_log_level))
	}

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
		fmt.Println("No .csproj or .fsproj files found in", fullProjectPath)
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

	// Redirect logger to a temp file so output doesn't corrupt the TUI.
	// The path is printed before the alt-screen takes over so the user can tail it.
	if level := logger.ParseLevel(builtFlags.Verbosity); level > logger.LevelNone {
		if logFile, err := os.CreateTemp("", "guget-*.log"); err == nil {
			fmt.Fprintf(os.Stderr, "Logging to %s\n", logFile.Name())
			logger.SetOutput(logFile)
			defer logFile.Close()
		}
	}

	// launch TUI — fetching happens as a background cmd
	m := NewModel(parsedProjects, nugetServices, builtFlags.NoColor)

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

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
	var projects []string
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".csproj" || filepath.Ext(path) == ".fsproj") {
			projects = append(projects, path)
		}
		return nil
	})
	return projects, err
}
