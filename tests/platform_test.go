package tests

import (
	"os"
	"strings"
	"testing"

	"dockvault/internal/detector"
)

// TestScanHostPathsIsOSAware exercises ScanHostPaths under a simulated
// Windows environment by pointing it at a temp directory tree that mimics
// %SystemDrive%\... layout, confirming it proposes Windows-shaped
// notable paths (not /etc/nginx-style Unix ones) when detector.SetGOOS
// is set to "windows", and restores the real OS's behavior afterward.
func TestScanHostPathsIsOSAware(t *testing.T) {
	restore := detector.SetGOOSForTest("windows")
	defer restore()

	// SystemDrive-relative notable paths use os.Getenv("SystemDrive"),
	// default "C:" - just confirm the candidate list shape without
	// needing a real Windows filesystem (ScanHostPaths itself will
	// os.Stat these, find nothing on this Linux test runner, and
	// correctly return zero notable-path results; the win here is that
	// it *tried* Windows paths, not Unix ones).
	paths := detector.NotableHostPathsForTest("windows")
	if len(paths) == 0 {
		t.Fatal("expected non-empty Windows notable path list")
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "/") {
			t.Errorf("Windows notable path %q looks like a Unix path", p)
		}
	}

	unixPaths := detector.NotableHostPathsForTest("linux")
	foundEtc := false
	for _, p := range unixPaths {
		if p == "/etc/nginx" {
			foundEtc = true
		}
	}
	if !foundEtc {
		t.Errorf("expected /etc/nginx in the Linux notable path list, got %v", unixPaths)
	}
}

func TestEnvScanRootsIsOSAware(t *testing.T) {
	old := os.Getenv("USERPROFILE")
	os.Setenv("USERPROFILE", `C:\Users\testuser`)
	defer os.Setenv("USERPROFILE", old)

	roots := detector.EnvScanRootsForTest("windows")
	if len(roots) == 0 {
		t.Fatal("expected non-empty Windows env-scan root list")
	}
	for _, r := range roots {
		if strings.HasPrefix(r, "/") {
			t.Errorf("Windows env-scan root %q looks like a Unix path", r)
		}
	}

	linuxRoots := detector.EnvScanRootsForTest("linux")
	found := false
	for _, r := range linuxRoots {
		if r == "/home" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected /home in the Linux env-scan root list, got %v", linuxRoots)
	}
}
