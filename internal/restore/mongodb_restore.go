// mongodb_restore.go restores via: mongorestore --archive --gzip < backup.
package restore

import (
	"bytes"
	"context"
	"fmt"

	"dockvault/internal/backup"
)

type MongodbRestorer struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

func NewMongodbRestorer(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *MongodbRestorer {
	return &MongodbRestorer{docker: dockerClient, rclone: rcloneClient}
}

func (r *MongodbRestorer) Name() string { return "mongodb" }

func (r *MongodbRestorer) ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error) {
	return ListRemoteBackups(ctx, r.rclone, job)
}

// Restore downloads file (already gzip-archived by mongodump --gzip, so
// no local decompression needed) and feeds it to
// `mongorestore --archive --gzip [--username --password --authenticationDatabase admin]`
// over stdin inside job's container.
func (r *MongodbRestorer) Restore(ctx context.Context, job *backup.Job, file BackupFile) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("mongodb.Restore: job %s has no target container", job.Name)
	}

	archive, warning, err := downloadAndRead(ctx, r.rclone, file, false)
	if err != nil {
		return "", err
	}

	// --drop makes mongorestore replace existing collections instead of
	// merging into them - see postgres.go's pg_dump --clean comment for
	// the same reasoning (confirmed by testing).
	cmd := []string{"mongorestore", "--archive", "--gzip", "--drop"}
	if user := job.Credentials["MONGO_INITDB_ROOT_USERNAME"]; user != "" {
		cmd = append(cmd, "--username", user)
		if pw := job.Credentials["MONGO_INITDB_ROOT_PASSWORD"]; pw != "" {
			cmd = append(cmd, "--password", pw, "--authenticationDatabase", "admin")
		}
	}

	if _, err := r.docker.Exec(ctx, job.ContainerName, cmd, nil, bytes.NewReader(archive)); err != nil {
		return "", fmt.Errorf("mongorestore: %w", err)
	}
	return warning, nil
}
