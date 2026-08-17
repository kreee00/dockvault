// Package hostdir implements the Host Directory backup type: a raw tar of
// a filesystem path with no Docker volume involved, per the spec's Backup
// Type 7 (e.g. /etc/nginx, /etc/letsencrypt).
package hostdir

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
	"dockvault/internal/backup/standard"
	"dockvault/internal/docker"
)

const Name = "hostdir"

// Executor performs host-directory backups the same way standard.Executor
// tars a volume, except the throwaway container's source mount is a host
// path (job.HostPath) instead of a named Docker volume - `docker run -v`
// doesn't distinguish between the two syntactically, so the mechanism is
// identical.
type Executor struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

// New returns an Executor that shells out via dockerClient and rcloneClient.
func New(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *Executor {
	return &Executor{docker: dockerClient, rclone: rcloneClient}
}

func (e *Executor) Name() string { return Name }

// Backup runs:
//
//	docker run --rm -v <host-path>:/source:ro -v <tmp>:/backup alpine \
//	  tar czf /backup/<job>_<volume-identifier>_<timestamp>.tar.gz -C /source .
func (e *Executor) Backup(ctx context.Context, job *backup.Job) error {
	return backup.RunAndRecord(ctx, job, func() (string, error) { return e.backup(ctx, job) })
}

func (e *Executor) backup(ctx context.Context, job *backup.Job) (string, error) {
	if job.HostPath == "" {
		return "", fmt.Errorf("hostdir.Backup: job %s has no host path", job.Name)
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fileName := backup.ArchiveFileName(job.Name, backup.VolumeIdentifierOrFallback(job), "tar.gz")
	mounts := []docker.MountSpec{
		{Source: job.HostPath, Target: "/source", ReadOnly: true},
		{Source: tmpDir, Target: "/backup"},
	}
	cmd := []string{"tar", "czf", "/backup/" + fileName, "-C", "/source", "."}

	if _, err := e.docker.Run(ctx, standard.AlpineImage, mounts, cmd); err != nil {
		return "", fmt.Errorf("taring %s: %w", job.HostPath, err)
	}

	return backup.UploadArchive(ctx, e.rclone, job, filepath.Join(tmpDir, fileName))
}
