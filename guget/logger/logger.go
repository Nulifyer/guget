package logger

import (
	"fmt"
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

func SetLevel(l Level) { level = l }
func SetColor(f bool)  { colorEnabled = f }
func ParseLevel(levelStr string) Level {
	switch strings.ToLower(levelStr) {
	case "none":
		return LevelNone
	case "error", "err":
		return LevelError
	case "", "warn", "warning":
		return LevelWarn
	case "info":
		return LevelInfo
	case "debug", "dbg":
		return LevelDebug
	case "trace", "trc":
		return LevelTrace
	default:
		return LevelWarn
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

func Trace(format string, v ...interface{}) {
	if level >= LevelTrace {
		if colorEnabled {
			fmt.Fprintf(os.Stdout, "%s[TRACE]%s %s\n", colorGrey, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(os.Stdout, "[TRACE] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Debug(format string, v ...interface{}) {
	if level >= LevelDebug {
		if colorEnabled {
			fmt.Fprintf(os.Stdout, "%s[DEBUG]%s %s\n", colorCyan, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(os.Stdout, "[DEBUG] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Info(format string, v ...interface{}) {
	if level >= LevelInfo {
		if colorEnabled {
			fmt.Fprintf(os.Stdout, "%s[INFO]%s %s\n", colorGreen, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(os.Stdout, "[INFO] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Warn(format string, v ...interface{}) {
	if level >= LevelWarn {
		if colorEnabled {
			fmt.Fprintf(os.Stderr, "%s[WARN]%s %s\n", colorYellow, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(os.Stderr, "[WARN] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Error(format string, v ...interface{}) {
	if level >= LevelError {
		if colorEnabled {
			fmt.Fprintf(os.Stderr, "%s[ERROR]%s %s\n", colorRed, colorReset, fmt.Sprintf(format, v...))
		} else {
			fmt.Fprintf(os.Stderr, "[ERROR] %s\n", fmt.Sprintf(format, v...))
		}
	}
}

func Fatal(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s[FATAL]%s %s\n", colorRed, colorReset, fmt.Sprintf(format, v...))
	os.Exit(1)
}
