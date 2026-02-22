package main

import (
	"os"
	"path/filepath"

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

	// register flags
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

	// build flags
	parsedFlags, _ := arger.Parse()
	builtFlags := BuildFlags(parsedFlags)

	// configure logger
	logger.SetLevel(logger.ParseLevel(builtFlags.Verbosity))
	logger.SetColor(!builtFlags.NoColor)

	return builtFlags
}

// -------------------------------
// Main
// --------------------------------
func main() {
	builtFlags := Init() // setup flags and logger

	// get full path
	fullProjectPath, err := filepath.Abs(builtFlags.ProjectDir)
	if err != nil {
		logger.Fatal("Couldn't get absolute path for project directory: %v", err)
	}
	logger.Info("Starting GoNugetTui with project directory: %s", fullProjectPath)
}
