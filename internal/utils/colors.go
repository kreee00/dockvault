// Package utils holds small cross-cutting helpers shared across
// dockvault's CLI, generator, and backup packages: ANSI colors, input
// validation, interactive pickers, and webhook notifications.
package utils

import (
	"os"
)

// ANSI escape codes. NoColor disables all of them (see ColorsEnabled).
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorBold   = "\033[1m"
)

// ColorsEnabled reports whether stdout is a terminal that should receive
// ANSI escapes. Respects the NO_COLOR convention.
func ColorsEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func wrap(code, s string) string {
	if !ColorsEnabled() {
		return s
	}
	return code + s + colorReset
}

func Red(s string) string    { return wrap(colorRed, s) }
func Green(s string) string  { return wrap(colorGreen, s) }
func Yellow(s string) string { return wrap(colorYellow, s) }
func Blue(s string) string   { return wrap(colorBlue, s) }
func Bold(s string) string   { return wrap(colorBold, s) }

// Check and Cross are the ✓/✗ indicators the spec's generated scripts and
// CLI output both use.
func Check() string { return Green("\u2713") }
func Cross() string { return Red("\u2717") }
