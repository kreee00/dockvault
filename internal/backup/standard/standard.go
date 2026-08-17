// Package standard implements the "Standard" backup type: a tar of a named
// Docker volume via a throwaway alpine container, per the spec's
// Backup Type 1.
package standard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
	"dockvault/internal/docker"
)

const Name = "standard"

// AlpineImage is the throwaway container image used to tar a volume or
// host path - tiny and ships `tar` in its base image, so no extra install
// step is needed. Shared by every tar-based executor (standard, n8n,
// hostdir) and by the matching restore executors.
const AlpineImage = "alpine:latest"

// Executor performs standard volume backups by shelling out to Docker (to
// tar the volume) and rclone (to upload the result), via the seamed
// backup.DockerClient/backup.RcloneClient interfaces so tests can mock
// both without a real daemon or rclone binary.
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
//	docker run --rm -v <volume>:/source:ro -v <tmp>:/backup alpine \
//	  tar czf /backup/<job>_<volume-identifier>_<timestamp>.tar.gz -C /source .
//
// then uploads the resulting archive to job.GoogleDrivePath, prunes old
// backups, and fires job's webhook (if set) - see backup.RunAndRecord,
// which also always records the outcome on job (LastBackup/
// LastBackupStatus) before returning, success or failure.
func (e *Executor) Backup(ctx context.Context, job *backup.Job) error {
	return backup.RunAndRecord(ctx, job, func() (string, error) { return e.backup(ctx, job) })
}

func (e *Executor) backup(ctx context.Context, job *backup.Job) (string, error) {
	if job.VolumeID == "" {
		return "", fmt.Errorf("standard.Backup: job %s has no volume ID", job.Name)
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fileName := backup.ArchiveFileName(job.Name, backup.VolumeIdentifierOrFallback(job), "tar.gz")
	mounts := []docker.MountSpec{
		{Source: job.VolumeID, Target: "/source", ReadOnly: true},
		{Source: tmpDir, Target: "/backup"},
	}
	cmd := []string{"tar", "czf", "/backup/" + fileName, "-C", "/source", "."}

	if _, err := e.docker.Run(ctx, AlpineImage, mounts, cmd); err != nil {
		return "", fmt.Errorf("taring volume %s: %w", job.VolumeID, err)
	}

	return backup.UploadArchive(ctx, e.rclone, job, filepath.Join(tmpDir, fileName))
}
