package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	lipgloss "github.com/charmbracelet/lipgloss"
)

type LogLevel int

const (
	LogLevelNone LogLevel = iota
	LogLevelError
	LogLevelWarn
	LogLevelInfo
	LogLevelDebug
	LogLevelTrace
)

var (
	logLevel        = LogLevelNone
	logColorEnabled = true
	logOutWriter    io.Writer // nil = os.Stdout / os.Stderr per-level
	logErrWriter    io.Writer // nil = os.Stderr
)

// Log-level styles — default to auto-dark colors at init, rebuilt by rebuildStyles().
var (
	logStyleTrace = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e"))
	logStyleDebug = lipgloss.NewStyle().Foreground(lipgloss.Color("#56d7c2"))
	logStyleInfo  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3fb950"))
	logStyleWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("#d29922"))
	logStyleError = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149"))
	logStyleFatal = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149"))
)

// rebuildLogStyles reassigns log styles from current color vars.
// Called by rebuildStyles() in themes.go.
func rebuildLogStyles() {
	logStyleTrace = lipgloss.NewStyle().Foreground(colorSubtle)
	logStyleDebug = lipgloss.NewStyle().Foreground(colorCyan)
	logStyleInfo = lipgloss.NewStyle().Foreground(colorGreen)
	logStyleWarn = lipgloss.NewStyle().Foreground(colorYellow)
	logStyleError = lipgloss.NewStyle().Foreground(colorRed)
	logStyleFatal = lipgloss.NewStyle().Foreground(colorRed)
}

func logSetOutput(w io.Writer) {
	logOutWriter = w
	logErrWriter = w
}

func logSetLevel(l LogLevel) { logLevel = l }
func logSetColor(f bool)     { logColorEnabled = f }

func logParseLevel(levelStr string) LogLevel {
	switch strings.ToLower(levelStr) {
	case "none":
		return LogLevelNone
	case "error", "err":
		return LogLevelError
	case "warn", "warning":
		return LogLevelWarn
	case "info":
		return LogLevelInfo
	case "debug", "dbg":
		return LogLevelDebug
	case "trace", "trc":
		return LogLevelTrace
	default:
		return LogLevelInfo
	}
}

func logStdOut() io.Writer {
	if logOutWriter != nil {
		return logOutWriter
	}
	return os.Stdout
}

func logStdErr() io.Writer {
	if logErrWriter != nil {
		return logErrWriter
	}
	return os.Stderr
}

// logUseColor returns true only when color is enabled AND output has not been
// redirected. A custom writer (e.g. the TUI log buffer) receives plain text
// so the TUI can apply its own styling.
func logUseColor() bool {
	return logColorEnabled && logOutWriter == nil
}

func logTrace(format string, v ...interface{}) {
	if logLevel >= LogLevelTrace {
		msg := fmt.Sprintf(format, v...)
		if logUseColor() {
			fmt.Fprintf(logStdOut(), "%s %s\n", logStyleTrace.Render("[TRACE]"), msg)
		} else {
			fmt.Fprintf(logStdOut(), "[TRACE] %s\n", msg)
		}
	}
}

func logDebug(format string, v ...interface{}) {
	if logLevel >= LogLevelDebug {
		msg := fmt.Sprintf(format, v...)
		if logUseColor() {
			fmt.Fprintf(logStdOut(), "%s %s\n", logStyleDebug.Render("[DEBUG]"), msg)
		} else {
			fmt.Fprintf(logStdOut(), "[DEBUG] %s\n", msg)
		}
	}
}

func logInfo(format string, v ...interface{}) {
	if logLevel >= LogLevelInfo {
		msg := fmt.Sprintf(format, v...)
		if logUseColor() {
			fmt.Fprintf(logStdOut(), "%s %s\n", logStyleInfo.Render("[INFO]"), msg)
		} else {
			fmt.Fprintf(logStdOut(), "[INFO] %s\n", msg)
		}
	}
}

func logWarn(format string, v ...interface{}) {
	if logLevel >= LogLevelWarn {
		msg := fmt.Sprintf(format, v...)
		if logUseColor() {
			fmt.Fprintf(logStdErr(), "%s %s\n", logStyleWarn.Render("[WARN]"), msg)
		} else {
			fmt.Fprintf(logStdErr(), "[WARN] %s\n", msg)
		}
	}
}

func logError(format string, v ...interface{}) {
	if logLevel >= LogLevelError {
		msg := fmt.Sprintf(format, v...)
		if logUseColor() {
			fmt.Fprintf(logStdErr(), "%s %s\n", logStyleError.Render("[ERROR]"), msg)
		} else {
			fmt.Fprintf(logStdErr(), "[ERROR] %s\n", msg)
		}
	}
}

// logFatal always prints to stderr and exits, regardless of the current log level.
func logFatal(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if logUseColor() {
		fmt.Fprintf(logStdErr(), "%s %s\n", logStyleFatal.Render("[FATAL]"), msg)
	} else {
		fmt.Fprintf(logStdErr(), "[FATAL] %s\n", msg)
	}
	os.Exit(1)
}
