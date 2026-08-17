// Package workspace manages $DOCKVAULT_HOME: resolving its location and
// ensuring the on-disk layout (logs/, per-job directories, config files)
// described in the spec exists.
package workspace

import (
	"os"
	"path/filepath"
)

const (
	// DefaultDirName is used under the user's home directory when neither
	// --home nor $DOCKVAULT_HOME is set.
	DefaultDirName = "dockvault_scripts"

	// EnvVar is the environment variable that overrides the default
	// location, before any --home flag is applied.
	EnvVar = "DOCKVAULT_HOME"
)

// Home resolves $DOCKVAULT_HOME with the documented precedence:
// explicit override (e.g. from --home) > $DOCKVAULT_HOME env var >
// ~/dockvault_scripts.
func Home(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if env := os.Getenv(EnvVar); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DefaultDirName), nil
}

// Workspace is a resolved $DOCKVAULT_HOME with helpers for the fixed
// sub-paths described in the spec's Workspace Layout section.
type Workspace struct {
	Root string
}

// Open resolves Home(override) into a Workspace. It does not touch disk;
// call EnsureLayout to create the directory structure.
func Open(override string) (*Workspace, error) {
	root, err := Home(override)
	if err != nil {
		return nil, err
	}
	return &Workspace{Root: root}, nil
}

func (w *Workspace) LogsDir() string      { return filepath.Join(w.Root, "logs") }
func (w *Workspace) ConfigPath() string   { return filepath.Join(w.Root, "config.json") }
func (w *Workspace) ManifestPath() string { return filepath.Join(w.Root, "manifest.json") }

// JobDir returns the per-job directory (holding job metadata and an
// optional .env). There is no master/orchestrator script - scheduling
// invokes the dockvault binary directly (`dockvault backup --all`), which
// loops over job directories itself.
func (w *Workspace) JobDir(jobName string) string { return filepath.Join(w.Root, jobName) }

// EnsureLayout creates Root and its logs/ subdirectory if they don't
// already exist. It never touches per-job directories - those are created
// by the generator, one at a time, as jobs are created.
//
// Root/logs/ are mode 0700 (owner-only), not the more common 0755: job
// directories hold infra topology (container names, volume names) in
// job.json, and log files can contain backup failure details - neither
// should be readable by every local user by default the way a 0755 tree
// would allow. Existing directories are chmod'd too (not just newly
// created ones - os.MkdirAll only applies the mode on creation), so
// upgrading dockvault also tightens a workspace created by an older
// version; the chmod is best-effort (e.g. a non-owner running dockvault
// against a shared workspace can't chmod it, but can still use it).
func (w *Workspace) EnsureLayout() error {
	if err := os.MkdirAll(w.Root, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(w.Root, 0o700)
	if err := os.MkdirAll(w.LogsDir(), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(w.LogsDir(), 0o700)
	return nil
}

// Exists reports whether Root already exists on disk.
func (w *Workspace) Exists() bool {
	info, err := os.Stat(w.Root)
	return err == nil && info.IsDir()
}
