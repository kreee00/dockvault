// Package restore defines the RestoreExecutor interface implemented by
// each service's restore strategy, mirroring internal/backup's executors.
package restore

import (
	"context"
	"fmt"
	"time"

	"dockvault/internal/backup"
)

// BackupFile is one remote backup artifact listed from Google Drive.
type BackupFile struct {
	Name     string
	Size     int64
	Modified time.Time
	Path     string // remote path on Google Drive
}

// ListRemoteBackups is the ListRemoteBackups implementation shared by
// every restore type: list files under job.GoogleDrivePath via rclone
// (already newest-first - see rclone.Client.ListFiles), converting each
// rclone.FileInfo into a BackupFile.
func ListRemoteBackups(ctx context.Context, rc backup.RcloneClient, job *backup.Job) ([]BackupFile, error) {
	files, err := rc.ListFiles(ctx, job.GoogleDrivePath)
	if err != nil {
		return nil, fmt.Errorf("listing backups for %s: %w", job.Name, err)
	}
	out := make([]BackupFile, 0, len(files))
	for _, f := range files {
		// A ModTime that fails to parse becomes the zero Time rather than
		// a fatal error - the file is still usable (e.g. for restore, all
		// that matters is Path/Name), it just won't sort meaningfully.
		modified, _ := time.Parse(time.RFC3339, f.ModTime)
		out = append(out, BackupFile{Name: f.Name, Size: f.Size, Modified: modified, Path: f.Path})
	}
	return out, nil
}

// RestoreExecutor is implemented once per service's restore strategy and
// performs the restore directly - there is no separate generated-script
// path (see DOCKVAULT_CONTEXT.md's architecture note).
type RestoreExecutor interface {
	// Name identifies the strategy, e.g. "postgres", "standard".
	Name() string
	// ListRemoteBackups lists available backup files for job, newest first.
	ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error)
	// Restore downloads and applies backupFile to job's target. warning is
	// non-empty (with a nil error) when the restore succeeded but wasn't
	// fully verified - e.g. backupFile predates integrity manifests and
	// so couldn't be checked at all; a non-nil error means Restore
	// refused outright (a manifest that parsed cleanly but disagreed with
	// the downloaded data's hash) and the target was left untouched.
	Restore(ctx context.Context, job *backup.Job, backupFile BackupFile) (warning string, err error)
}

// stopsContainer is the set of restore types that stop job.ContainerName
// during Restore (to safely swap out its data files) and deliberately
// leave it stopped afterward - see redis_restore.go's doc comment for
// why. Callers (the `dockvault restore` CLI flow) use this to know
// whether the post-restore "start it back up?" prompt applies at all.
var stopsContainer = map[string]bool{
	"redis": true,
	"n8n":   true,
}

// StopsContainerDuringRestore reports whether backupType's restore
// executor stops (and leaves stopped) job.ContainerName - see stopsContainer.
func StopsContainerDuringRestore(backupType string) bool {
	return stopsContainer[backupType]
}

// Registry is a lookup of executor Name() -> RestoreExecutor.
type Registry map[string]RestoreExecutor

// NewRegistry builds a Registry from a list of executors, keyed by Name().
func NewRegistry(executors ...RestoreExecutor) Registry {
	r := make(Registry, len(executors))
	for _, e := range executors {
		r[e.Name()] = e
	}
	return r
}
