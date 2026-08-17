package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"dockvault/internal/backup"
)

// envWebhookKey is the .env key SaveJob/LoadJob use for a job's webhook
// URL, kept alongside credentials in the same file (both are secrets/
// per-job overrides that don't belong in job.json, which is otherwise
// safe to read without special handling).
const envWebhookKey = "DOCKVAULT_WEBHOOK_URL"

// JobConfig is the non-secret, on-disk shape of a job's job.json -
// everything about a job except credentials and its webhook URL, which
// live in the job's .env instead (mode 600).
type JobConfig struct {
	Name string `json:"name"`
	// BackupType is the executor Name() this job runs through - see
	// backup.Job's doc comment on the same field.
	BackupType       string `json:"backup_type"`
	ServiceType      string `json:"service_type"`
	VolumeID         string `json:"volume_id,omitempty"`
	VolumeIdentifier string `json:"volume_identifier,omitempty"`
	HostPath         string `json:"host_path,omitempty"`
	ContainerName    string `json:"container_name,omitempty"`
	GoogleDrivePath  string `json:"google_drive_path"`
	RetentionDays    int    `json:"retention_days"`
	MinFilesToKeep   int    `json:"min_files_to_keep"`
	// LocalBackupDir/LocalRetentionCount - see backup.Job's doc comment
	// on the same two fields. Snapshotted from workspace.Config at
	// generate time, same as RetentionDays/MinFilesToKeep above - not
	// re-read from config.json on every `dockvault backup` run.
	LocalBackupDir      string `json:"local_backup_dir,omitempty"`
	LocalRetentionCount int    `json:"local_retention_count"`
}

// JobConfigPath and EnvPath are the two files that make up a job
// directory.
func (w *Workspace) JobConfigPath(jobName string) string {
	return filepath.Join(w.JobDir(jobName), "job.json")
}
func (w *Workspace) EnvPath(jobName string) string {
	return filepath.Join(w.JobDir(jobName), ".env")
}

// SaveJob writes job's job.json and (if it has credentials or a webhook
// URL) its .env, creating the job directory if needed. The directory and
// job.json are mode 0700/0600, not 0755/0644 - see Workspace.EnsureLayout's
// doc comment for why (job metadata reveals infra topology; there's no
// reason it should be more open than the directory holding it).
func (w *Workspace) SaveJob(job *backup.Job) error {
	if err := os.MkdirAll(w.JobDir(job.Name), 0o700); err != nil {
		return fmt.Errorf("creating job directory for %q: %w", job.Name, err)
	}
	_ = os.Chmod(w.JobDir(job.Name), 0o700)

	cfg := JobConfig{
		Name:                job.Name,
		BackupType:          job.BackupType,
		ServiceType:         job.ServiceType,
		VolumeID:            job.VolumeID,
		VolumeIdentifier:    job.VolumeIdentifier,
		HostPath:            job.HostPath,
		ContainerName:       job.ContainerName,
		GoogleDrivePath:     job.GoogleDrivePath,
		RetentionDays:       job.RetentionDays,
		MinFilesToKeep:      job.MinFilesToKeep,
		LocalBackupDir:      job.LocalBackupDir,
		LocalRetentionCount: job.LocalRetentionCount,
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding job %q: %w", job.Name, err)
	}
	if err := os.WriteFile(w.JobConfigPath(job.Name), data, 0o600); err != nil {
		return fmt.Errorf("writing job.json for %q: %w", job.Name, err)
	}

	env := map[string]string{}
	for k, v := range job.Credentials {
		env[k] = v
	}
	if job.WebhookURL != "" {
		env[envWebhookKey] = job.WebhookURL
	}
	if len(env) == 0 {
		return nil
	}
	return w.saveEnv(job.Name, env)
}

// saveEnv and loadEnv (this job's credentials + webhook URL) are
// platform-dispatched - see env_unix.go (the plaintext .env file used
// here and on macOS) and env_windows.go (Windows Credential Manager, via
// internal/secrets) for their actual implementations.

// LoadJob reads jobName's job.json + .env back into a *backup.Job, ready
// to hand to the matching BackupExecutor/RestoreExecutor.
func (w *Workspace) LoadJob(jobName string) (*backup.Job, error) {
	data, err := os.ReadFile(w.JobConfigPath(jobName))
	if err != nil {
		return nil, fmt.Errorf("loading job %q: %w", jobName, err)
	}
	var cfg JobConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing job %q: %w", jobName, err)
	}

	env, err := w.loadEnv(jobName)
	if err != nil {
		return nil, err
	}
	webhook := env[envWebhookKey]
	creds := make(map[string]string, len(env))
	for k, v := range env {
		if k == envWebhookKey {
			continue
		}
		creds[k] = v
	}

	return &backup.Job{
		Name:                cfg.Name,
		BackupType:          cfg.BackupType,
		ServiceType:         cfg.ServiceType,
		VolumeID:            cfg.VolumeID,
		VolumeIdentifier:    cfg.VolumeIdentifier,
		HostPath:            cfg.HostPath,
		ContainerName:       cfg.ContainerName,
		Credentials:         creds,
		GoogleDrivePath:     cfg.GoogleDrivePath,
		RetentionDays:       cfg.RetentionDays,
		MinFilesToKeep:      cfg.MinFilesToKeep,
		LocalBackupDir:      cfg.LocalBackupDir,
		LocalRetentionCount: cfg.LocalRetentionCount,
		WebhookURL:          webhook,
	}, nil
}

// ListJobNames returns every job directory under Root (one that has a
// job.json), sorted alphabetically.
func (w *Workspace) ListJobNames() ([]string, error) {
	entries, err := os.ReadDir(w.Root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading workspace %q: %w", w.Root, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(w.Root, e.Name(), "job.json")); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// LoadAllJobs loads every job under Root via LoadJob, continuing past a
// single bad job directory rather than failing the whole load (mirrors
// the project's "one container exiting mid-scan shouldn't fail the whole
// scan" log-and-continue convention) - errs is one entry per job that
// failed to load, jobs are the ones that succeeded.
func (w *Workspace) LoadAllJobs() (jobs []*backup.Job, errs []error) {
	names, err := w.ListJobNames()
	if err != nil {
		return nil, []error{err}
	}
	for _, name := range names {
		job, err := w.LoadJob(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, errs
}
