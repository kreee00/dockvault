package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// lockFileName is the exclusive-create lock file that prevents two
// dockvault invocations (backup/restore/generate) from running against
// the same workspace concurrently. Without it, two overlapping runs can:
// lose updates racing on manifest.json's read-modify-write cycle; garble
// the same day's log file; and, worst case, run two backups/restores
// against the very same Docker volume or container at once (e.g. a
// restore that wipes a volume while a backup is mid-tar of it).
const lockFileName = ".dockvault.lock"

// staleLockAge is how old an unremoved lock file must be before a new
// run is allowed to steal it (e.g. left behind by a crash that skipped
// the deferred unlock). This is a portable-everywhere fallback, paired
// with a best-effort PID-liveness check (processAlive) where the OS
// supports it - on platforms where that check can't tell us anything
// (notably Windows - os.Process.Signal there only supports os.Kill/
// os.Interrupt), staleLockAge is what eventually reclaims the lock.
const staleLockAge = 24 * time.Hour

func (w *Workspace) lockPath() string { return filepath.Join(w.Root, lockFileName) }

// Lock acquires an exclusive lock on the workspace, returning an unlock
// func to call (typically via defer) once the caller is done. If another
// live dockvault process already holds the lock, it returns a clear error
// naming that process's PID rather than corrupting shared state.
func (w *Workspace) Lock() (unlock func(), err error) {
	path := w.lockPath()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return nil, fmt.Errorf("creating lock file %s: %w", path, err)
		}
		if stealErr := w.stealStaleLock(path); stealErr != nil {
			return nil, stealErr
		}
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("acquiring lock %s: %w", path, err)
		}
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Close()

	return func() { os.Remove(path) }, nil
}

// stealStaleLock decides whether an existing lock file is safe to remove
// (so Lock can retry): it isn't if the recorded PID is still a live
// process, or if the lock hasn't reached staleLockAge yet.
func (w *Workspace) stealStaleLock(path string) error {
	info, statErr := os.Stat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return nil // it disappeared already (the other run just finished) - fine
		}
		return fmt.Errorf("checking lock file %s: %w", path, statErr)
	}

	if pid, ok := readLockPID(path); ok && processAlive(pid) {
		return fmt.Errorf("another dockvault process (pid %d) is already using this workspace (%s) - wait for it to finish, or remove the lock file yourself if you're certain it's gone", pid, path)
	}

	if age := time.Since(info.ModTime()); age < staleLockAge {
		return fmt.Errorf("lock file %s exists from a process that doesn't appear to be running, but is only %s old (younger than the %s staleness threshold) - remove it manually if you're sure no other dockvault process is using this workspace", path, age.Round(time.Second), staleLockAge)
	}

	return os.Remove(path)
}

func readLockPID(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// processAlive is a best-effort liveness check. It errs toward "can't
// tell" (returns false, letting staleLockAge decide) rather than risking
// a false "still alive" that would make a genuinely stale lock
// unremovable.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal(syscall.Signal(0)) is the standard Unix "is this PID alive"
	// probe (no signal is actually delivered). On Windows, os.Process.Signal
	// only supports os.Kill/os.Interrupt and returns an error for
	// anything else, so this always reports false there - staleLockAge is
	// the real reclamation mechanism on that platform.
	return proc.Signal(syscall.Signal(0)) == nil
}
