// redis_restore.go restores via: stop container, docker cp restored.rdb in.
//
// The container is deliberately left stopped rather than auto-restarted:
// per the resolved design decision in DOCKVAULT_CONTEXT.md ("Restore on
// stopped containers / post-restore container state"), dockvault never
// silently restarts a container after a restore - the `dockvault restore`
// CLI flow prompts the user for that separately. Stopping it here is still
// required, though: Redis holds dump.rdb open and won't pick up a
// replacement file without a restart anyway, and copying over it live
// risks corruption.
package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
)

type RedisRestorer struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

func NewRedisRestorer(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *RedisRestorer {
	return &RedisRestorer{docker: dockerClient, rclone: rcloneClient}
}

func (r *RedisRestorer) Name() string { return "redis" }

func (r *RedisRestorer) ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error) {
	return ListRemoteBackups(ctx, r.rclone, job)
}

func (r *RedisRestorer) Restore(ctx context.Context, job *backup.Job, file BackupFile) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("redis.Restore: job %s has no target container", job.Name)
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-restore-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := r.rclone.Download(ctx, file.Path, tmpDir); err != nil {
		return "", fmt.Errorf("downloading %s: %w", file.Path, err)
	}
	localPath := filepath.Join(tmpDir, file.Name)
	// Verify before touching the container at all - a corrupted/tampered
	// archive must be caught while it's still running, not after Stop below.
	warning, verr := verifyManifestFile(ctx, r.rclone, file, tmpDir, localPath)
	if verr != nil {
		return "", verr
	}

	if err := r.docker.Stop(ctx, job.ContainerName); err != nil {
		return "", fmt.Errorf("stopping container %s: %w", job.ContainerName, err)
	}

	if err := r.docker.Cp(ctx, localPath, job.ContainerName+":/data/dump.rdb"); err != nil {
		return "", fmt.Errorf("copying dump.rdb into container: %w", err)
	}
	return warning, nil
}
