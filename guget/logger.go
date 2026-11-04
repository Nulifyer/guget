package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	style "github.com/nulifyer/guget/internal/ui/style"
)

var logStartTime = time.Now()

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

var (
	logSecretsMu      sync.RWMutex
	logSecrets        = make(map[string]struct{})
	urlUserInfo       = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)
	sensitiveKV       = regexp.MustCompile(`(?i)(password|passwd|token|api[_-]?key|sig)=([^&\s]+)`)
	authorizationLine = regexp.MustCompile(`(?i)(authorization\s*[:=]\s*)[^\r\n]+`)
)

func logRegisterSecret(secret string) {
	if len(secret) < 4 {
		return
	}
	logSecretsMu.Lock()
	logSecrets[secret] = struct{}{}
	logSecretsMu.Unlock()
}

func redactLogMessage(message string) string {
	message = urlUserInfo.ReplaceAllString(message, `${1}[REDACTED]@`)
	message = sensitiveKV.ReplaceAllString(message, `${1}=[REDACTED]`)
	message = authorizationLine.ReplaceAllString(message, `${1}[REDACTED]`)
	logSecretsMu.RLock()
	for secret := range logSecrets {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
	}
	logSecretsMu.RUnlock()
	return message
}

// Log-level styles — default to auto-dark colors at init, rebuilt by rebuildStyles().
var (
	logStyleTrace = style.NewStyle().Foreground(style.Color("#8b949e"))
	logStyleDebug = style.NewStyle().Foreground(style.Color("#56d7c2"))
	logStyleInfo  = style.NewStyle().Foreground(style.Color("#3fb950"))
	logStyleWarn  = style.NewStyle().Foreground(style.Color("#d29922"))
	logStyleError = style.NewStyle().Foreground(style.Color("#f85149"))
	logStyleFatal = style.NewStyle().Foreground(style.Color("#f85149"))
)

// rebuildLogStyles reassigns log styles from current color vars.
// Called by rebuildStyles() in themes.go.
func rebuildLogStyles() {
	logStyleTrace = style.NewStyle().Foreground(colorSubtle)
	logStyleDebug = style.NewStyle().Foreground(colorCyan)
	logStyleInfo = style.NewStyle().Foreground(colorGreen)
	logStyleWarn = style.NewStyle().Foreground(colorYellow)
	logStyleError = style.NewStyle().Foreground(colorRed)
	logStyleFatal = style.NewStyle().Foreground(colorRed)
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

// logTimestamp returns a relative timestamp like "+0.123s" showing seconds
// since process start. This keeps log lines compact while making it easy to
// see how long each step takes.
func logTimestamp() string {
	return fmt.Sprintf("+%.3fs", time.Since(logStartTime).Seconds())
}

func logTrace(format string, v ...interface{}) {
	if logLevel >= LogLevelTrace {
		ts := logTimestamp()
		msg := redactLogMessage(fmt.Sprintf(format, v...))
		if logUseColor() {
			fmt.Fprintf(logStdOut(), "%s %s %s\n", logStyleTrace.Render("[TRACE]"), logStyleTrace.Render(ts), msg)
		} else {
			fmt.Fprintf(logStdOut(), "[TRACE] %s %s\n", ts, msg)
		}
	}
}

func logDebug(format string, v ...interface{}) {
	if logLevel >= LogLevelDebug {
		ts := logTimestamp()
		msg := redactLogMessage(fmt.Sprintf(format, v...))
		if logUseColor() {
			fmt.Fprintf(logStdOut(), "%s %s %s\n", logStyleDebug.Render("[DEBUG]"), logStyleDebug.Render(ts), msg)
		} else {
			fmt.Fprintf(logStdOut(), "[DEBUG] %s %s\n", ts, msg)
		}
	}
}

func logInfo(format string, v ...interface{}) {
	if logLevel >= LogLevelInfo {
		ts := logTimestamp()
		msg := redactLogMessage(fmt.Sprintf(format, v...))
		if logUseColor() {
			fmt.Fprintf(logStdOut(), "%s %s %s\n", logStyleInfo.Render("[INFO]"), logStyleInfo.Render(ts), msg)
		} else {
			fmt.Fprintf(logStdOut(), "[INFO] %s %s\n", ts, msg)
		}
	}
}

func logWarn(format string, v ...interface{}) {
	if logLevel >= LogLevelWarn {
		ts := logTimestamp()
		msg := redactLogMessage(fmt.Sprintf(format, v...))
		if logUseColor() {
			fmt.Fprintf(logStdErr(), "%s %s %s\n", logStyleWarn.Render("[WARN]"), logStyleWarn.Render(ts), msg)
		} else {
			fmt.Fprintf(logStdErr(), "[WARN] %s %s\n", ts, msg)
		}
	}
}

func logError(format string, v ...interface{}) {
	if logLevel >= LogLevelError {
		ts := logTimestamp()
		msg := redactLogMessage(fmt.Sprintf(format, v...))
		if logUseColor() {
			fmt.Fprintf(logStdErr(), "%s %s %s\n", logStyleError.Render("[ERROR]"), logStyleError.Render(ts), msg)
		} else {
			fmt.Fprintf(logStdErr(), "[ERROR] %s %s\n", ts, msg)
		}
	}
}

// logFatal always prints to stderr and exits, regardless of the current log level.
func logFatal(format string, v ...interface{}) {
	ts := logTimestamp()
	msg := redactLogMessage(fmt.Sprintf(format, v...))
	if logUseColor() {
		fmt.Fprintf(logStdErr(), "%s %s %s\n", logStyleFatal.Render("[FATAL]"), logStyleFatal.Render(ts), msg)
	} else {
		fmt.Fprintf(logStdErr(), "[FATAL] %s %s\n", ts, msg)
	}
	os.Exit(1)
}
