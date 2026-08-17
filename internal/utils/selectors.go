package utils

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// SelectOne prints a numbered list of options and reads a single choice
// from r, returning the chosen index (0-based). Used by the --interactive
// wizard and by `restore`'s job/backup-file pickers.
func SelectOne(w io.Writer, r *bufio.Reader, prompt string, options []string) (int, error) {
	fmt.Fprintln(w, prompt)
	for i, o := range options {
		fmt.Fprintf(w, "  [%d] %s\n", i+1, o)
	}
	fmt.Fprint(w, "> ")

	line, err := r.ReadString('\n')
	if err != nil {
		return -1, err
	}
	line = strings.TrimSpace(line)
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(options) {
		return -1, fmt.Errorf("enter a number between 1 and %d", len(options))
	}
	return n - 1, nil
}

// Confirm reads a y/n response, defaulting to defaultYes when the user
// just presses enter. Used for "Create X backup jobs? (Y/n)" style
// prompts.
func Confirm(w io.Writer, r *bufio.Reader, prompt string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Fprintf(w, "%s %s ", prompt, suffix)

	line, err := r.ReadString('\n')
	if err != nil {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes, nil
	}
	return line == "y" || line == "yes", nil
}

// RequireTypedConfirmation reads a line and reports whether it exactly
// matches word - used for restore's "type RESTORE to confirm" safety gate.
func RequireTypedConfirmation(w io.Writer, r *bufio.Reader, word string) (bool, error) {
	fmt.Fprintf(w, "Type %q to confirm: ", word)
	line, err := r.ReadString('\n')
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(line) == word, nil
}
