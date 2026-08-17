// Package backup defines the BackupExecutor interface implemented by each
// backup strategy (standard, postgres, mysql, mongodb, redis, n8n,
// hostdir), plus the Job type they all operate on.
package backup

import (
	"context"
	"io"
	"time"

	"dockvault/internal/docker"
	"dockvault/internal/rclone"
)

// Job is one configured backup job: a job directory under
// $DOCKVAULT_HOME/<job-name>/ holding an optional .env (credentials/webhook)
// and job metadata. dockvault executes the backup/restore itself (see
// BackupExecutor/RestoreExecutor) rather than generating a standalone
// script for cron/systemd to run - see DOCKVAULT_CONTEXT.md's
// "Architecture: Native Go Execution, No Generated Scripts" note.
type Job struct {
	Name string
	// BackupType is the executor Name() to run this job through
	// ("standard", "postgres", "mysql", "mongodb", "redis", "n8n",
	// "hostdir") - equal to ServiceType except generic volumes, which use
	// "standard". Set once at generate time by
	// generator.ExecutorNameForServiceType.
	BackupType string
	VolumeID   string // the actual Docker volume name/ID used as the mount source (standard, n8n, and every service-specific type backed by a named volume)
	// VolumeIdentifier is the <volume_identifier> archive-filename
	// component for this job's data source (see ArchiveFileName and
	// internal/detector's VolumeIdentifier/BindMountIdentifier, which
	// compute it): a named volume's name as-is, "vol-<hash8>" for an
	// anonymous volume, or "bind-<target>" for a bind mount. If unset, an
	// executor falls back to VolumeID.
	VolumeIdentifier string
	// HostPath is an absolute host filesystem path to back up directly, no
	// Docker volume involved - only set for BackupType "hostdir".
	HostPath string
	// ContainerName is the target container for `docker exec`/`stop`/
	// `start` - set for every service-specific type (postgres, mysql,
	// mongodb, redis, n8n) and unused otherwise.
	ContainerName string
	ServiceType   string
	Credentials   map[string]string
	// GoogleDrivePath is the remote directory in the standardized form
	// /<environment>/<hostname>/<service_type>/<job_name>/, as produced by
	// internal/detector.RemotePath.
	GoogleDrivePath string
	RetentionDays   int
	MinFilesToKeep  int
	WebhookURL      string
	// LocalBackupDir is the workspace-local directory a durable copy of
	// each backup archive (+ its integrity manifest) is retained under,
	// at LocalBackupDir/Name/ - this is what makes dockvault actually
	// satisfy the 3-2-1 rule's "2 independent media" leg, since without
	// it the only durable copy ever produced is the remote one. LocalRetentionCount
	// <= 0 disables local retention entirely (mirrors RetentionDays <= 0
	// disabling remote pruning). Note: a local copy on the *same host* as
	// the source data protects against Drive-side outages/accidents and
	// speeds up the most recent restore, but gives zero protection against
	// a compromised production host - that requires LocalBackupDir to
	// actually be a separate disk/mount, an operator choice this field
	// doesn't and can't enforce.
	LocalBackupDir      string
	LocalRetentionCount int

	LastBackup       *time.Time
	LastBackupStatus string
}

// DockerClient is the subset of *docker.Client a backup/restore executor
// needs. Seamed as an interface so tests can substitute a mock instead of
// shelling out to a real docker binary (no daemon is available in CI/test
// environments - see DOCKVAULT_CONTEXT.md's Testing notes).
type DockerClient interface {
	// Run starts a throwaway container (`docker run --rm`) with the given
	// mounts and command - used for volume-tar style backups (standard,
	// n8n, hostdir) and restores.
	Run(ctx context.Context, image string, mounts []docker.MountSpec, cmd []string) ([]byte, error)
	// Exec runs cmd inside an already-running container (`docker exec`),
	// optionally piping stdin to it (nil for none) - used by
	// service-specific backups/restores that shell out to a tool already
	// installed in the target container (pg_dump, mysqldump, psql, ...).
	Exec(ctx context.Context, container string, cmd []string, env map[string]string, stdin io.Reader) ([]byte, error)
	// Stop and Start wrap `docker stop`/`docker start` - used by executors
	// that must take a container offline to safely touch its data files
	// (redis, n8n) and by restore's post-restore restart prompt.
	Stop(ctx context.Context, container string) error
	Start(ctx context.Context, container string) error
	// Cp wraps `docker cp` in either direction (src/dst use the
	// "container:path" form on whichever side is in the container) - used
	// by redis's dump.rdb extraction/restore.
	Cp(ctx context.Context, src, dst string) error
}

// RcloneClient is the subset of *rclone.Client a backup/restore executor
// needs to move backup artifacts to/from Google Drive. Seamed as an
// interface for the same testability reason as DockerClient.
type RcloneClient interface {
	Upload(ctx context.Context, localPath, remotePath string) error
	ListFiles(ctx context.Context, remotePath string) ([]rclone.FileInfo, error)
	Download(ctx context.Context, remotePath, localDest string) error
	Prune(ctx context.Context, remotePath string, retentionDays, minFilesToKeep int) ([]string, error)
	// Hashsum returns remotePath's MD5 as reported by the remote backend -
	// used right after upload to confirm the bytes that landed on Drive
	// actually match what was sent, rather than trusting an "upload
	// succeeded" exit code alone. See UploadArchive.
	Hashsum(ctx context.Context, remotePath string) (string, error)
}

// BackupExecutor is implemented once per backup strategy and performs the
// backup directly - there is no separate generated-script path (see
// DOCKVAULT_CONTEXT.md's architecture note above).
type BackupExecutor interface {
	// Name identifies the strategy, e.g. "postgres", "mysql", "standard".
	Name() string
	// Backup executes the backup for job directly.
	Backup(ctx context.Context, job *Job) error
}

// Registry is a lookup of executor Name() -> BackupExecutor, populated by
// internal/generator and internal/cli at startup as each strategy package
// is wired in.
type Registry map[string]BackupExecutor

// NewRegistry builds a Registry from a list of executors, keyed by Name().
func NewRegistry(executors ...BackupExecutor) Registry {
	r := make(Registry, len(executors))
	for _, e := range executors {
		r[e.Name()] = e
	}
	return r
}
