package tests

import (
	"os"
	"strings"
	"testing"
	"time"

	"dockvault/internal/workspace"
)

func TestLock_BlocksASecondHolder(t *testing.T) {
	ws := &workspace.Workspace{Root: t.TempDir()}

	unlock, err := ws.Lock()
	if err != nil {
		t.Fatalf("first Lock() returned error: %v", err)
	}
	defer unlock()

	if _, err := ws.Lock(); err == nil {
		t.Fatal("expected a second Lock() to fail while the first is still held")
	} else if !strings.Contains(err.Error(), "already using this workspace") {
		t.Errorf("Lock() error = %q, want it to explain another process holds the lock", err.Error())
	}
}

func TestLock_UnlockAllowsReacquiring(t *testing.T) {
	ws := &workspace.Workspace{Root: t.TempDir()}

	unlock, err := ws.Lock()
	if err != nil {
		t.Fatalf("first Lock() returned error: %v", err)
	}
	unlock()

	unlock2, err := ws.Lock()
	if err != nil {
		t.Fatalf("Lock() after unlock() returned error: %v", err)
	}
	unlock2()
}

func TestLock_StealsAnOldStaleLockFromADeadPID(t *testing.T) {
	ws := &workspace.Workspace{Root: t.TempDir()}
	if err := os.MkdirAll(ws.Root, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	lockPath := ws.Root + "/.dockvault.lock"
	// A PID essentially guaranteed not to be a running process, written
	// with a modification time far older than the staleness threshold.
	if err := os.WriteFile(lockPath, []byte("999999999\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatalf("Chtimes returned error: %v", err)
	}

	unlock, err := ws.Lock()
	if err != nil {
		t.Fatalf("Lock() should have reclaimed the stale lock, got error: %v", err)
	}
	unlock()
}

func TestLock_RefusesAFreshUnattributableLock(t *testing.T) {
	ws := &workspace.Workspace{Root: t.TempDir()}
	if err := os.MkdirAll(ws.Root, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	lockPath := ws.Root + "/.dockvault.lock"
	// Freshly written, unparseable PID content - shouldn't be stolen just
	// because the PID can't be checked; must wait for staleLockAge.
	if err := os.WriteFile(lockPath, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, err := ws.Lock(); err == nil {
		t.Fatal("expected Lock() to refuse a fresh lock file it can't verify is stale")
	}
}
