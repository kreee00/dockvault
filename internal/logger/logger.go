// Package logger provides dockvault's structured, timestamped logging:
// colorized to stdout, plain text to a log file, per the spec's "Logging"
// note ("Both to stdout (colorized) and files.").
package logger

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type Level int

const (
	LevelInfo Level = iota
	LevelSuccess
	LevelWarn
	LevelError
)

// Logger writes to stdout (colorized, when the terminal supports it) and,
// if Out is set, to a log file simultaneously. Safe for concurrent use
// from multiple goroutines (cli/backup.go's runJobs logs from up to
// --parallel of them at once) - mu serializes each line the same way the
// standard library's log.Logger does, so two jobs' log lines can't
// interleave into a garbled line. (Empirically, a *os.File opened
// O_APPEND with fmt.Fprint of a single pre-formatted string didn't
// actually interleave under `go test -race` with 8 concurrent writers -
// this is nonetheless not part of io.Writer's documented contract, so
// the mutex is cheap insurance, not a fix for an observed bug.)
type Logger struct {
	Verbose bool
	// File, if non-nil, receives a plain-text (uncolored) copy of every
	// line, e.g. logs/master_YYYY-MM-DD.log.
	File io.Writer

	mu sync.Mutex
}

// New returns a Logger. If logFile is non-empty, it's opened
// (append-or-create) and used as the plain-text log destination. Mode
// 0600, not 0644: a failed job's error text can carry sensitive detail
// (e.g. the exact command that failed), so the log shouldn't default to
// being readable by every local user - matches .env's existing 0600.
func New(logFile string) (*Logger, error) {
	l := &Logger{}
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("opening log file: %w", err)
		}
		// O_CREATE only applies the mode when the file is newly created -
		// chmod unconditionally so a log file left at 0644 by an older
		// dockvault version gets tightened too, not just brand-new ones.
		_ = os.Chmod(logFile, 0o600)
		l.File = f
	}
	return l, nil
}

func (l *Logger) log(level Level, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02T15:04:05Z07:00")
	plain := fmt.Sprintf("[%s] %s %s\n", ts, tag(level), msg)

	out := os.Stdout
	if level == LevelError {
		out = os.Stderr
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprint(out, colorize(level, plain))
	if l.File != nil {
		fmt.Fprint(l.File, plain)
	}
}

func tag(level Level) string {
	switch level {
	case LevelSuccess:
		return "OK"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

// colorize wraps plain in an ANSI color matching level, when stdout looks
// like a terminal. Kept local (rather than importing internal/utils) to
// avoid a needless cross-package dependency for four escape codes.
func colorize(level Level, plain string) string {
	if !isTerminal() {
		return plain
	}
	code := "\033[36m" // info: cyan
	switch level {
	case LevelSuccess:
		code = "\033[32m" // green
	case LevelWarn:
		code = "\033[33m" // yellow
	case LevelError:
		code = "\033[31m" // red
	}
	return code + plain + "\033[0m"
}

func isTerminal() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func (l *Logger) Info(format string, args ...interface{})    { l.log(LevelInfo, format, args...) }
func (l *Logger) Success(format string, args ...interface{}) { l.log(LevelSuccess, format, args...) }
func (l *Logger) Warn(format string, args ...interface{})    { l.log(LevelWarn, format, args...) }
func (l *Logger) Error(format string, args ...interface{})   { l.log(LevelError, format, args...) }

// Debug only logs when Verbose is set (dockvault's --verbose flag).
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.Verbose {
		l.log(LevelInfo, format, args...)
	}
}
