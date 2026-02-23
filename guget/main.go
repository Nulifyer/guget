package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	// tea "github.com/charmbracelet/bubbletea"
	"arger"
	"logger"
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

// -------------------------------
// Main
// --------------------------------
func main() {
	builtFlags := Init()

	// get full path
	fullProjectPath, err := filepath.Abs(builtFlags.ProjectDir)
	if err != nil {
		logger.Fatal("Couldn't get absolute path for project directory: %v", err)
	}
	logger.Info("Starting GoNugetTui with project directory: %s", fullProjectPath)

	// find projects
	projectFiles, err := FindProjectFiles(fullProjectPath)
	if err != nil {
		logger.Fatal("Error finding projects: %v", err)
	}
	logger.Info("Found %d project(s)", len(projectFiles))

	// parse projects
	var parsedProjects []*ParsedProject
	for _, file := range projectFiles {
		project, err := ParseCsproj(file)
		if err != nil {
			logger.Fatal("Error parsing project %s: %v", file, err)
		}
		parsedProjects = append(parsedProjects, project)
	}

	// detect nuget sources from the project root
	sources := DetectSources(fullProjectPath)
	logger.Info("Detected %d NuGet source(s):", len(sources))
	for _, src := range sources {
		logger.Info("  [%s] %s", src.Name, src.URL)
	}

	// build nuget services for each source
	var nugetServices []*NugetService
	for _, src := range sources {
		svc, err := NewNugetService(src)
		if err != nil {
			logger.Warn("Failed to initialise NuGet source [%s] %s: %v", src.Name, src.URL, err)
			continue
		}
		nugetServices = append(nugetServices, svc)
	}
	if len(nugetServices) == 0 {
		logger.Fatal("No reachable NuGet sources found")
	}

	// collect distinct package names across all projects
	distinctPackages := NewSet[string]()
	for _, project := range parsedProjects {
		for pkg := range project.Packages {
			distinctPackages.Add(pkg.Name)
		}
	}
	logger.Info("Fetching info for %d distinct package(s)...", distinctPackages.Len())

	// fetch package info in parallel
	type nugetResult struct {
		pkg    *PackageInfo
		source string
		err    error
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

	// print results per project
	for _, project := range parsedProjects {
		// collect framework display strings
		var fwStrs []string
		for fw := range project.TargetFrameworks {
			fwStrs = append(fwStrs, fw.String())
		}

		fmt.Printf("\n📦 %s\n", project.FileName)
		fmt.Printf("   Frameworks: %s\n", strings.Join(fwStrs, ", "))
		fmt.Println(strings.Repeat("─", 80))

		for pkg := range project.Packages {
			res := results[pkg.Name]
			if res.err != nil {
				fmt.Printf("   %-40s ❌ %v\n", pkg.Name, res.err)
				continue
			}

			current := pkg.Version.String()

			// find latest compatible version across all project frameworks
			latestCompatible := res.pkg.LatestStableForFramework(project.TargetFrameworks)

			// overall latest stable regardless of framework
			latestStable := res.pkg.LatestStable()

			compStr := versionDisplay(latestCompatible, pkg.Version)
			stableStr := versionDisplay(latestStable, pkg.Version)

			fmt.Printf("   %-40s current: %-12s  compatible: %-20s  latest: %-20s  [%s]\n",
				pkg.Name, current, compStr, stableStr, res.source)
		}
	}

	logger.Trace("done")
}

// versionDisplay formats a version for output, showing upgrade arrow or checkmark.
func versionDisplay(v *PackageVersion, current SemVer) string {
	if v == nil {
		return "<none>"
	}
	ver := v.SemVer.String()
	if v.SemVer.IsNewerThan(current) {
		return fmt.Sprintf("⬆  %s", ver)
	}
	return fmt.Sprintf("✅ %s", ver)
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
	if err != nil {
		return nil, err
	}
	return projects, nil
}
