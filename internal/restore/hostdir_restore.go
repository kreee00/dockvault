// hostdir_restore.go restores via: extract tar into the target host path.
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

type HostdirRestorer struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

func NewHostdirRestorer(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *HostdirRestorer {
	return &HostdirRestorer{docker: dockerClient, rclone: rcloneClient}
}

func (r *HostdirRestorer) Name() string { return "hostdir" }

func (r *HostdirRestorer) ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error) {
	return ListRemoteBackups(ctx, r.rclone, job)
}

func (r *HostdirRestorer) Restore(ctx context.Context, job *backup.Job, file BackupFile) (string, error) {
	if job.HostPath == "" {
		return "", fmt.Errorf("hostdir.Restore: job %s has no host path", job.Name)
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
		{Source: job.HostPath, Target: "/target"},
		{Source: tmpDir, Target: "/backup", ReadOnly: true},
	}
	cmd := []string{"tar", "xzf", "/backup/" + file.Name, "-C", "/target"}
	if _, err := r.docker.Run(ctx, standard.AlpineImage, mounts, cmd); err != nil {
		return "", fmt.Errorf("extracting into %s: %w", job.HostPath, err)
	}
	return warning, nil
}
