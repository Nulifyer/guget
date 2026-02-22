package logger

import (
	"fmt"
	stdlog "log"
)

type Level int

const (
	LevelOff Level = iota
	LevelError
	LevelWarn
	LevelInfo
	LevelDebug
	LevelTrace
)

var level = LevelOff
var colorEnabled = true

func SetLevel(l Level) { level = l }
func SetColor(f bool)  { colorEnabled = f }

const (
	colorReset  = "\x1b[0m"
	colorRed    = "\x1b[31m"
	colorYellow = "\x1b[33m"
	colorGreen  = "\x1b[32m"
	colorCyan   = "\x1b[36m"
	colorBlue   = "\x1b[34m"
	colorGrey   = "\x1b[90m"
)

func colored(prefix, color, format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	stdlog.Printf("%s%s%s %s", color, prefix, colorReset, msg)
}

func Trace(format string, v ...interface{}) {
	if level >= LevelTrace {
		colored("[TRACE]", colorGrey, format, v...)
	}
}

func Debug(format string, v ...interface{}) {
	if level >= LevelDebug {
		colored("[DEBUG]", colorCyan, format, v...)
	}
}

func Info(format string, v ...interface{}) {
	if level >= LevelInfo {
		colored("[INFO]", colorGreen, format, v...)
	}
}

func Warn(format string, v ...interface{}) {
	if level >= LevelWarn {
		colored("[WARN]", colorYellow, format, v...)
	}
}

func Error(format string, v ...interface{}) {
	if level >= LevelError {
		colored("[ERROR]", colorRed, format, v...)
	}
}

func Fatal(v ...interface{}) {
	stdlog.Fatal(v...)
}
