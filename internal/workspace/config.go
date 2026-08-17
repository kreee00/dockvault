package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is the schema of $DOCKVAULT_HOME/config.json: global defaults
// applied to every job unless overridden per-job.
type Config struct {
	RcloneRemote string `json:"rclone_remote"`
	// Environment is the <environment> segment of every job's remote path
	// (/<environment>/<hostname>/<service_type>/<job_name>/), e.g. "stage"
	// or "prod". A --environment CLI flag, when set, overrides this.
	Environment    string `json:"environment"`
	RetentionDays  int    `json:"retention_days"`
	MinFilesToKeep int    `json:"min_files_to_keep"`
	WebhookURL     string `json:"webhook_url,omitempty"`
	ParallelJobs   int    `json:"parallel_jobs"`
	// LocalBackupDir is where a durable local copy of each job's backups
	// is retained - see backup.Job's doc comment on the same field for
	// why this exists. Left empty here (resolved to a real path by
	// LoadConfig, the only place that has the workspace root in scope);
	// an operator who wants real 3-2-1 independence, not just protection
	// against Drive-side failures, should point this at a separate
	// disk/mount rather than leaving it under $DOCKVAULT_HOME.
	LocalBackupDir string `json:"local_backup_dir,omitempty"`
	// LocalRetentionCount <= 0 disables local retention entirely -
	// mirrors RetentionDays <= 0 disabling remote pruning. Deliberately
	// not a separate bool: Config round-trips through JSON in a way that
	// can't otherwise distinguish "local_backup_dir was never set" from
	// "explicitly set to empty to disable it".
	LocalRetentionCount int `json:"local_retention_count"`
}

// DefaultConfig returns the documented defaults: retention_days=30 (remote,
// ~2-3-1 off-site window), min_files_to_keep=30, rclone remote
// "dockvault_backup", environment "stage", 3-way parallelism, 14 local
// backup copies retained per job (~14 days at the standard daily cadence).
func DefaultConfig() Config {
	return Config{
		RcloneRemote:        "dockvault_backup",
		Environment:         "stage",
		RetentionDays:       30,
		MinFilesToKeep:      30,
		ParallelJobs:        3,
		LocalRetentionCount: 14,
	}
}

// LoadConfig reads and parses config.json from the workspace. If the file
// doesn't exist, it returns DefaultConfig() with no error, so first-run
// callers don't need a separate existence check.
func (w *Workspace) LoadConfig() (Config, error) {
	data, err := os.ReadFile(w.ConfigPath())
	if os.IsNotExist(err) {
		cfg := DefaultConfig()
		cfg.LocalBackupDir = filepath.Join(w.Root, "local_backups")
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	// LocalBackupDir has no config.json-independent default the way the
	// int/string fields above do, since it needs the workspace root -
	// only known here, not inside DefaultConfig() itself. A config.json
	// that never mentioned local_backup_dir resolves to this; one that
	// explicitly sets it is left untouched.
	if cfg.LocalBackupDir == "" {
		cfg.LocalBackupDir = filepath.Join(w.Root, "local_backups")
	}
	return cfg, nil
}

// SaveConfig writes cfg to config.json, pretty-printed, creating the
// workspace directory first if needed.
func (w *Workspace) SaveConfig(cfg Config) error {
	if err := w.EnsureLayout(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// Mode 0600: WebhookURL is effectively a bearer secret for many
	// webhook providers (e.g. a Slack incoming-webhook URL - anyone with
	// it can post as that integration), so config.json shouldn't default
	// to world-readable any more than .env does.
	return os.WriteFile(w.ConfigPath(), data, 0o600)
}
