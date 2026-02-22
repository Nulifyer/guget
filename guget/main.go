package main

import (
	"fmt"
	"os"
	"strings"

	// tea "github.com/charmbracelet/bubbletea"
	"arger"
	"logger"
)

// -------------------------------
// Flag definitions and parsing
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

// -------------------------------
// Main
// --------------------------------
func main() {
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

	builtFlags := BuildFlags(arger.Parse())
	fmt.Printf("%v", builtFlags)

	// configure logger
	switch strings.ToLower(builtFlags.Verbosity) {
	case "none":
		logger.SetLevel(logger.LevelNone)
	case "error", "err":
		logger.SetLevel(logger.LevelError)
	case "", "warn", "warning":
		logger.SetLevel(logger.LevelWarn)
	case "info":
		logger.SetLevel(logger.LevelInfo)
	case "debug", "dbg":
		logger.SetLevel(logger.LevelDebug)
	case "trace", "trc":
		logger.SetLevel(logger.LevelTrace)
	default:
		logger.Warn("Invalid verbosity level '%s', defaulting to 'warn'", builtFlags.Verbosity)
		logger.SetLevel(logger.LevelWarn)
	}

	// test logging
	logger.Trace("This is a trace message")
	logger.Debug("This is a debug message")
	logger.Info("This is an info message")
	logger.Warn("This is a warning message")
	logger.Error("This is an error message")
}
