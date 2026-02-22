package main

import (
	"strings"

	// tea "github.com/charmbracelet/bubbletea"
	arger "../arger"
	logger "../logger"
)

const (
	Flag_Verbosity = "verbosity"
)

func main() {
	arger.RegisterFlag(arger.Flag{
		Name:     Flag_Verbosity,
		Aliases:  []string{"--verbose", "-v"},
		Default:  "warn",
		Required: false,
		Help:     "Set the logging verbosity level (none, err, warn, info, debug)",
	})

	parsedFlags := arger.Parse()

	switch strings.ToLower(parsedFlags[Flag_Verbosity].AsString()) {
	case "off":
		logger.SetLevel(logger.LevelNone)
	case "error", "err":
		logger.SetLevel(logger.LevelError)
	case "warn", "warning":
		logger.SetLevel(logger.LevelWarn)
	case "info":
		logger.SetLevel(logger.LevelInfo)
	case "debug", "dbg":
		logger.SetLevel(logger.LevelDebug)
	default:
		logger.SetLevel(logger.LevelNone)
	}

	// test logging
	logger.Trace("This is a trace message")
	logger.Debug("This is a debug message")
	logger.Info("This is an info message")
	logger.Warn("This is a warning message")
	logger.Error("This is an error message")
}
