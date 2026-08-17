package detector

import (
	"os"
	"path/filepath"
	"runtime"
)

// currentGOOS drives every OS-aware decision in this file. It's a var
// (not a direct runtime.GOOS reference at each call site) so tests can
// override it to exercise the Windows path tables without cross-compiling.
var currentGOOS = runtime.GOOS

// notableHostPaths returns the well-known, likely-worth-backing-up
// directories for goos. ScanHostPaths still verifies each one with
// os.Stat before including it in results, but keeping the candidate list
// itself OS-aware avoids ever treating a Windows path as if it were the
// Unix catch-alls (/opt, /home, ...) or vice versa - those directories
// mean something different, or don't exist at all, on the other platform.
func notableHostPaths(goos string) []string {
	switch goos {
	case "windows":
		drive := systemDrive()
		return []string{
			drive + `\nginx\conf`,
			drive + `\ProgramData\letsencrypt`,
			drive + `\ProgramData\docker\config`,
			drive + `\inetpub`,
		}
	default: // linux, darwin, and anything else Unix-like
		return []string{
			"/etc/nginx",
			"/etc/letsencrypt",
			"/opt",
			"/srv",
			"/home",
			"/root",
		}
	}
}

// envScanRootsFor returns the roots Phase 1's ".env file discovery" walks,
// OS-aware for the same reason as notableHostPaths. On Windows this walks
// %SystemDrive%\ProgramData and %SystemDrive%\Users (derived from
// %USERPROFILE% so it works whether dockvault runs as a normal user or an
// elevated/scheduled-task account).
func envScanRootsFor(goos string) []string {
	switch goos {
	case "windows":
		roots := []string{systemDrive() + `\ProgramData`}
		if up := os.Getenv("USERPROFILE"); up != "" {
			roots = append(roots, filepath.Dir(up)) // e.g. C:\Users
		}
		return roots
	default:
		return []string{"/opt", "/srv", "/home", "/root"}
	}
}

func systemDrive() string {
	if d := os.Getenv("SystemDrive"); d != "" {
		return d
	}
	return `C:`
}

// --- test seams -------------------------------------------------------
//
// The functions below exist so tests/platform_test.go (an external test
// package, since tests/ mirrors the spec's flat test-package layout) can
// exercise the OS-aware path tables without needing to cross-compile the
// test binary for Windows. They're intentionally thin.

// SetGOOSForTest overrides currentGOOS for the duration of a test and
// returns a restore function; call it via `defer`.
func SetGOOSForTest(goos string) (restore func()) {
	prev := currentGOOS
	currentGOOS = goos
	return func() { currentGOOS = prev }
}

// NotableHostPathsForTest exposes notableHostPaths to external tests.
func NotableHostPathsForTest(goos string) []string { return notableHostPaths(goos) }

// EnvScanRootsForTest exposes envScanRootsFor to external tests.
func EnvScanRootsForTest(goos string) []string { return envScanRootsFor(goos) }

// skipDirNames are directory names walkForEnvFiles never descends into:
// Linux virtual filesystems that are pointless (and sometimes unsafe) to
// walk, plus Windows profile/system bloat that would otherwise make a
// depth-4 walk of C:\Users extremely slow for no benefit - a .env file
// under someone's AppData or node_modules isn't a dockvault config.
var skipDirNames = map[string]bool{
	"proc": true,
	"sys":  true,

	"AppData":                   true,
	"Application Data":          true,
	"$Recycle.Bin":              true,
	"System Volume Information": true,
	"node_modules":              true,
	".git":                      true,
}
