package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
)

type Level int

const (
	LevelNone Level = iota
	LevelError
	LevelWarn
	LevelInfo
	LevelDebug
	LevelTrace
)

var level = LevelNone
var colorEnabled = false
var outWriter io.Writer // nil = use os.Stdout / os.Stderr per-level defaults
var errWriter io.Writer // nil = use os.Stderr

// SetOutput redirects all log output (including error-level) to w.
// Pass nil to restore the default os.Stdout / os.Stderr behaviour.
// Color codes are suppressed when a custom writer is set.
func SetOutput(w io.Writer) {
	outWriter = w
	errWriter = w
}

func SetLevel(l Level) { level = l }
func SetColor(f bool)  { colorEnabled = f }

// ParseLevel converts a string to a Level. Unrecognised strings return LevelInfo.
func ParseLevel(levelStr string) Level {
	switch strings.ToLower(levelStr) {
	case "none":
		return LevelNone
	case "error", "err":
		return LevelError
	case "warn", "warning":
		return LevelWarn
	case "info":
		return LevelInfo
	case "debug", "dbg":
		return LevelDebug
	case "trace", "trc":
		return LevelTrace
	default:
		return LevelInfo
	}
}

const (
	colorReset  = "\x1b[0m"
	colorRed    = "\x1b[31m"
	colorYellow = "\x1b[33m"
	colorGreen  = "\x1b[32m"
	colorCyan   = "\x1b[36m"
	colorBlue   = "\x1b[34m"
	colorGrey   = "\x1b[90m"
)

func stdOut() io.Writer {
	if outWriter != nil {
		return outWriter
	}
	return os.Stdout
}

func stdErr() io.Writer {
	if errWriter != nil {
		return errWriter
	}
	return os.Stderr
}

// useColor returns true only when color is enabled AND output has not been
// redirected. A custom writer (e.g. the TUI log buffer) receives plain text
// so the TUI can apply its own styling.
func useColor() bool {
	return colorEnabled && outWriter == nil
}

func Trace(format string, v ...interface{}) {
	if level >= LevelTrace {
		if useColor() {
			fmt.Fprintf(stdOut(), "%s[TRACE]%s %s\n", colorGrey, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(stdOut(), "[TRACE] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Debug(format string, v ...interface{}) {
	if level >= LevelDebug {
		if useColor() {
			fmt.Fprintf(stdOut(), "%s[DEBUG]%s %s\n", colorCyan, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(stdOut(), "[DEBUG] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Info(format string, v ...interface{}) {
	if level >= LevelInfo {
		if useColor() {
			fmt.Fprintf(stdOut(), "%s[INFO]%s %s\n", colorGreen, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(stdOut(), "[INFO] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Warn(format string, v ...interface{}) {
	if level >= LevelWarn {
		if useColor() {
			fmt.Fprintf(stdErr(), "%s[WARN]%s %s\n", colorYellow, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(stdErr(), "[WARN] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Error(format string, v ...interface{}) {
	if level >= LevelError {
		if useColor() {
			fmt.Fprintf(stdErr(), "%s[ERROR]%s %s\n", colorRed, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(stdErr(), "[ERROR] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

// Fatal always prints to stderr and exits, regardless of the current log level.
func Fatal(format string, v ...interface{}) {
	fmt.Fprintf(stdErr(), "%s[FATAL]%s %s\n", colorRed, colorReset, fmt.Sprintf(format, v...))
	os.Exit(1)
}
