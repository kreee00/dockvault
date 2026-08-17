// standard_restore.go restores via: extract tar into the target volume.
package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
	"dockvault/internal/backup/standard"
	"dockvault/internal/docker"
)

type StandardRestorer struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

func NewStandardRestorer(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *StandardRestorer {
	return &StandardRestorer{docker: dockerClient, rclone: rcloneClient}
}

func (r *StandardRestorer) Name() string { return "standard" }

func (r *StandardRestorer) ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error) {
	return ListRemoteBackups(ctx, r.rclone, job)
}

// Restore downloads file and extracts it into job.VolumeID via a
// throwaway alpine container - the reverse of standard.Executor.Backup.
// It overlays the archive's contents onto the volume rather than wiping
// it first, matching the original spec wording for this restore type.
func (r *StandardRestorer) Restore(ctx context.Context, job *backup.Job, file BackupFile) (string, error) {
	if job.VolumeID == "" {
		return "", fmt.Errorf("standard.Restore: job %s has no volume ID", job.Name)
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-restore-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := r.rclone.Download(ctx, file.Path, tmpDir); err != nil {
		return "", fmt.Errorf("downloading %s: %w", file.Path, err)
	}
	warning, verr := verifyManifestFile(ctx, r.rclone, file, tmpDir, filepath.Join(tmpDir, file.Name))
	if verr != nil {
		return "", verr
	}

	mounts := []docker.MountSpec{
		{Source: job.VolumeID, Target: "/target"},
		{Source: tmpDir, Target: "/backup", ReadOnly: true},
	}
	cmd := []string{"tar", "xzf", "/backup/" + file.Name, "-C", "/target"}
	if _, err := r.docker.Run(ctx, standard.AlpineImage, mounts, cmd); err != nil {
		return "", fmt.Errorf("extracting into volume %s: %w", job.VolumeID, err)
	}
	return warning, nil
}
