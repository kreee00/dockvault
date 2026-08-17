// rabbitmq_restore.go restores broker definitions via:
// docker cp <definitions> <container>:<scratch path>, then
// rabbitmqctl import_definitions <scratch path>.
package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
)

// rabbitmqRestorePathInContainer mirrors the backup side's scratch path
// (see rabbitmq.definitionsPathInContainer) - a fixed, predictable
// location, since import_definitions also takes a file path argument, not
// stdin.
const rabbitmqRestorePathInContainer = "/tmp/dockvault-restore-definitions.json"

type RabbitmqRestorer struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

func NewRabbitmqRestorer(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *RabbitmqRestorer {
	return &RabbitmqRestorer{docker: dockerClient, rclone: rcloneClient}
}

func (r *RabbitmqRestorer) Name() string { return "rabbitmq" }

func (r *RabbitmqRestorer) ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error) {
	return ListRemoteBackups(ctx, r.rclone, job)
}

// Restore downloads and gunzips file (manifest-verified by downloadAndRead
// before any of this runs - a hash mismatch aborts before the broker is
// ever touched), writes the plain definitions JSON to a local temp file,
// copies it into the container, then runs `rabbitmqctl import_definitions`
// on it. No container stop/restart: unlike redis/n8n, RabbitMQ can import
// definitions while running - see StopsContainerDuringRestore /
// stopsContainer, which has no "rabbitmq" entry.
func (r *RabbitmqRestorer) Restore(ctx context.Context, job *backup.Job, file BackupFile) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("rabbitmq.Restore: job %s has no target container", job.Name)
	}

	data, warning, err := downloadAndRead(ctx, r.rclone, file, true)
	if err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-restore-rabbitmq-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	localPath := filepath.Join(tmpDir, "definitions.json")
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return "", fmt.Errorf("writing definitions for copy: %w", err)
	}

	if err := r.docker.Cp(ctx, localPath, job.ContainerName+":"+rabbitmqRestorePathInContainer); err != nil {
		return "", fmt.Errorf("copying definitions into container: %w", err)
	}
	defer func() {
		_, _ = r.docker.Exec(ctx, job.ContainerName, []string{"rm", "-f", rabbitmqRestorePathInContainer}, nil, nil)
	}()

	if _, err := r.docker.Exec(ctx, job.ContainerName, []string{"rabbitmqctl", "import_definitions", rabbitmqRestorePathInContainer}, nil, nil); err != nil {
		return "", fmt.Errorf("rabbitmqctl import_definitions: %w", err)
	}
	return warning, nil
}
