// n8n_restore.go restores via: stop container, wipe volume, extract tar.
//
// As with redis (see redis_restore.go), the container is deliberately
// left stopped rather than auto-restarted - `dockvault restore` prompts
// for that separately, per the resolved design decision in
// DOCKVAULT_CONTEXT.md. Stopping it here is still required: wiping and
// replacing files under a volume the container has open risks corruption.
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

type N8nRestorer struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

func NewN8nRestorer(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *N8nRestorer {
	return &N8nRestorer{docker: dockerClient, rclone: rcloneClient}
}

func (r *N8nRestorer) Name() string { return "n8n" }

func (r *N8nRestorer) ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error) {
	return ListRemoteBackups(ctx, r.rclone, job)
}

func (r *N8nRestorer) Restore(ctx context.Context, job *backup.Job, file BackupFile) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("n8n.Restore: job %s has no target container", job.Name)
	}
	if job.VolumeID == "" {
		return "", fmt.Errorf("n8n.Restore: job %s has no volume ID", job.Name)
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-restore-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := r.rclone.Download(ctx, file.Path, tmpDir); err != nil {
		return "", fmt.Errorf("downloading %s: %w", file.Path, err)
	}
	// Verify before touching the container at all - a corrupted/tampered
	// archive must be caught while it's still running and its volume
	// still intact, not after Stop+wipe below have already happened.
	warning, verr := verifyManifestFile(ctx, r.rclone, file, tmpDir, filepath.Join(tmpDir, file.Name))
	if verr != nil {
		return "", verr
	}

	if err := r.docker.Stop(ctx, job.ContainerName); err != nil {
		return "", fmt.Errorf("stopping container %s: %w", job.ContainerName, err)
	}

	// Wipe the volume first so stale pre-restore files don't linger
	// alongside the restored ones.
	wipeCmd := []string{"sh", "-c", "rm -rf /target/* /target/.[!.]* 2>/dev/null || true"}
	if _, err := r.docker.Run(ctx, standard.AlpineImage, []docker.MountSpec{{Source: job.VolumeID, Target: "/target"}}, wipeCmd); err != nil {
		return "", fmt.Errorf("wiping volume %s: %w", job.VolumeID, err)
	}

	mounts := []docker.MountSpec{
		{Source: job.VolumeID, Target: "/target"},
		{Source: tmpDir, Target: "/backup", ReadOnly: true},
	}
	extractCmd := []string{"tar", "xzf", "/backup/" + file.Name, "-C", "/target"}
	if _, err := r.docker.Run(ctx, standard.AlpineImage, mounts, extractCmd); err != nil {
		return "", fmt.Errorf("extracting into volume %s: %w", job.VolumeID, err)
	}
	return warning, nil
}
