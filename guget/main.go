package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

var version = "dev"

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

const (
	Flag_NoColor    = "no-color"
	Flag_Verbosity  = "verbosity"
	Flag_ProjectDir = "project"
	Flag_Version    = "version"
	Flag_LogFile    = "log-file"
	Flag_Theme      = "theme"
	Flag_SortBy     = "sort-by"
)

type BuiltFlags struct {
	NoColor    bool
	Verbosity  string
	ProjectDir string
	Version    bool
	LogFile    string
	Theme      string
	SortBy     string
}

func BuildFlags(flags map[string]IParsedFlag) BuiltFlags {
	return BuiltFlags{
		NoColor:    GetFlag[bool](flags, Flag_NoColor),
		Verbosity:  GetFlag[string](flags, Flag_Verbosity),
		ProjectDir: GetFlag[string](flags, Flag_ProjectDir),
		Version:    GetFlag[bool](flags, Flag_Version),
		LogFile:    GetFlag[string](flags, Flag_LogFile),
		Theme:      GetFlag[string](flags, Flag_Theme),
		SortBy:     GetFlag[string](flags, Flag_SortBy),
	}
}

// initCLI registers CLI flags, parses os.Args, and returns the resolved flag values.
// Named initCLI (not Init) to avoid confusion with App.Init() in the same package.
func initCLI() BuiltFlags {
	rebuildStyles()
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
	RegisterFlag(Flag[string]{
		Name:        Flag_SortBy,
		Aliases:     []string{"-o", "--sort-by"},
		Default:     Optional("status:asc"),
		Description: "Initial sort order (status, name, source, current, available) with optional :asc or :desc",
		Parser: func(s string) (string, error) {
			name, dir, _ := strings.Cut(s, ":")
			switch strings.ToLower(name) {
			case "", "status", "name", "source", "current", "available":
				switch dir {
				case "", "asc", "desc":
					return s, nil
				default:
					return "", fmt.Errorf("invalid sort dir: %s", dir)
				}
			default:
				return "", fmt.Errorf("invalid sort mode: %s", name)
			}
		},
	})

	parsedFlags, _ := ParseFlags()
	builtFlags := BuildFlags(parsedFlags)

	logSetLevel(logParseLevel(builtFlags.Verbosity))
	logSetColor(!builtFlags.NoColor)

	return builtFlags
}

type nugetResult struct {
	pkg    *PackageInfo
	source string
	err    error
}

func main() {
	builtFlags := initCLI()
	initTheme(builtFlags.Theme, builtFlags.NoColor)

	if builtFlags.Version {
		fmt.Printf("guget %s\n", version)
		os.Exit(0)
	}

	// Capture all startup logs for the TUI log panel.
	buf := &logBuffer{}
	if builtFlags.LogFile != "" {
		f, err := os.Create(builtFlags.LogFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open log file %q: %v\n", builtFlags.LogFile, err)
			os.Exit(1)
		}
		defer f.Close()
		logSetOutput(io.MultiWriter(buf, f))
	} else {
		logSetOutput(buf)
	}

	fullProjectPath, err := filepath.Abs(builtFlags.ProjectDir)
	if err != nil {
		logFatal("Couldn't get absolute path for project directory: %v", err)
	}
	logInfo("Starting guget with project directory: %s", fullProjectPath)

	snapshot, err := loadWorkspace(fullProjectPath)
	if err != nil {
		logFatal("Error loading workspace: %v", err)
	}

	m := NewApp(fullProjectPath, snapshot, buf.Lines(), builtFlags)

	p := tea.NewProgram(m)

	// Wire up live log forwarding to the TUI now that the program exists.
	buf.mu.Lock()
	buf.send = p.Send
	buf.mu.Unlock()
	m.SetSender(p.Send)
	m.startInitialLoad()
	stopWatcher := watchWorkspaceFiles(fullProjectPath, p.Send)
	defer stopWatcher()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

// enrichFromNugetOrg merges vulnerability and metadata from nuget.org into
// a PackageInfo fetched from a private feed.
func enrichFromNugetOrg(info, nugetInfo *PackageInfo) {
	// Build a version→vulnerabilities map from nuget.org data.
	nugetVulns := make(map[string][]PackageVulnerability, len(nugetInfo.Versions))
	for _, v := range nugetInfo.Versions {
		if len(v.Vulnerabilities) > 0 {
			nugetVulns[v.SemVer.String()] = v.Vulnerabilities
		}
	}

	for i := range info.Versions {
		key := info.Versions[i].SemVer.String()
		if len(info.Versions[i].Vulnerabilities) == 0 {
			if vulns, ok := nugetVulns[key]; ok {
				info.Versions[i].Vulnerabilities = vulns
			}
		}
	}

	if info.ProjectURL == "" {
		info.ProjectURL = nugetInfo.ProjectURL
	}
	if info.RepositoryURL == "" {
		info.RepositoryType = nugetInfo.RepositoryType
		info.RepositoryURL = nugetInfo.RepositoryURL
	}
}
