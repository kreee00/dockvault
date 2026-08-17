// Package n8n implements the n8n backup type: volume tar plus a graceful
// container stop/start around it, per the spec's Backup Type 6.
package n8n

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
	"dockvault/internal/backup/standard"
	"dockvault/internal/docker"
)

const Name = "n8n"

// Executor performs n8n backups by stopping the container (n8n keeps an
// open SQLite/queue state that isn't safe to tar live), taring the volume
// the same way standard.Executor does, then always restarting the
// container - even if the tar step failed - so a failed backup doesn't
// also leave the service down.
type Executor struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

// New returns an Executor that shells out via dockerClient and rcloneClient.
func New(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *Executor {
	return &Executor{docker: dockerClient, rclone: rcloneClient}
}

func (e *Executor) Name() string { return Name }

// Backup runs: docker stop <container>, tar the volume (as in
// standard.Executor), docker start <container> - the restart always
// happens (via a deferred call using a named return), whether the tar
// step succeeded or not, mirroring the "nested trap" guarantee the spec
// asked for from the (now removed) generated-script version of this
// executor.
func (e *Executor) Backup(ctx context.Context, job *backup.Job) error {
	return backup.RunAndRecord(ctx, job, func() (string, error) { return e.backup(ctx, job) })
}

func (e *Executor) backup(ctx context.Context, job *backup.Job) (status string, err error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("n8n.Backup: job %s has no target container", job.Name)
	}
	if job.VolumeID == "" {
		return "", fmt.Errorf("n8n.Backup: job %s has no volume ID", job.Name)
	}

	if stopErr := e.docker.Stop(ctx, job.ContainerName); stopErr != nil {
		return "", fmt.Errorf("stopping container %s: %w", job.ContainerName, stopErr)
	}
	defer func() {
		if startErr := e.docker.Start(ctx, job.ContainerName); startErr != nil {
			if err != nil {
				err = fmt.Errorf("%w (additionally, restarting container %s failed: %v)", err, job.ContainerName, startErr)
			} else {
				status += fmt.Sprintf(", warning: restarting container %s failed: %v", job.ContainerName, startErr)
			}
		}
	}()

	tmpDir, mkErr := os.MkdirTemp("", "dockvault-"+job.Name+"-")
	if mkErr != nil {
		return "", fmt.Errorf("creating temp dir: %w", mkErr)
	}
	defer os.RemoveAll(tmpDir)

	fileName := backup.ArchiveFileName(job.Name, backup.VolumeIdentifierOrFallback(job), "tar.gz")
	mounts := []docker.MountSpec{
		{Source: job.VolumeID, Target: "/source", ReadOnly: true},
		{Source: tmpDir, Target: "/backup"},
	}
	cmd := []string{"tar", "czf", "/backup/" + fileName, "-C", "/source", "."}

	if _, runErr := e.docker.Run(ctx, standard.AlpineImage, mounts, cmd); runErr != nil {
		return "", fmt.Errorf("taring volume %s: %w", job.VolumeID, runErr)
	}

	status, err = backup.UploadArchive(ctx, e.rclone, job, filepath.Join(tmpDir, fileName))
	return status, err
}
