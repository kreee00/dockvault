// Package mongodb implements the MongoDB backup type: mongodump --archive
// --gzip, with optional auth, per the spec's Backup Type 4.
package mongodb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
)

const Name = "mongodb"

// Executor performs MongoDB backups via `docker exec ... mongodump
// --archive --gzip`. mongodump already gzips its own output, so - unlike
// postgres/mysql - there's no local compression step; the archive bytes
// are written straight to disk.
type Executor struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

// New returns an Executor that shells out via dockerClient and rcloneClient.
func New(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *Executor {
	return &Executor{docker: dockerClient, rclone: rcloneClient}
}

func (e *Executor) Name() string { return Name }

// Backup runs, inside the target container:
//
//	mongodump --archive --gzip [--username --password --authenticationDatabase admin]
func (e *Executor) Backup(ctx context.Context, job *backup.Job) error {
	return backup.RunAndRecord(ctx, job, func() (string, error) { return e.backup(ctx, job) })
}

func (e *Executor) backup(ctx context.Context, job *backup.Job) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("mongodb.Backup: job %s has no target container", job.Name)
	}

	cmd := []string{"mongodump", "--archive", "--gzip"}
	if user := job.Credentials["MONGO_INITDB_ROOT_USERNAME"]; user != "" {
		cmd = append(cmd, "--username", user)
		if pw := job.Credentials["MONGO_INITDB_ROOT_PASSWORD"]; pw != "" {
			cmd = append(cmd, "--password", pw, "--authenticationDatabase", "admin")
		}
	}

	archive, err := e.docker.Exec(ctx, job.ContainerName, cmd, nil, nil)
	if err != nil {
		return "", fmt.Errorf("mongodump: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fileName := backup.ArchiveFileName(job.Name, backup.VolumeIdentifierOrFallback(job), "archive.gz")
	localPath := filepath.Join(tmpDir, fileName)
	// Mode 0600, not 0644 - see backup.GzipToFile's doc comment for why.
	if err := os.WriteFile(localPath, archive, 0o600); err != nil {
		return "", fmt.Errorf("writing dump output: %w", err)
	}

	return backup.UploadArchive(ctx, e.rclone, job, localPath)
}
