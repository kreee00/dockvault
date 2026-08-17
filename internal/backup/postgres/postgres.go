// Package postgres implements the PostgreSQL backup type: pg_dump | gzip,
// with user/password/db detected from the container environment, per the
// spec's Backup Type 2.
package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
)

const Name = "postgres"

// Executor performs postgres backups via `docker exec ... pg_dump`,
// gzipping the output itself (rather than relying on gzip being present
// inside the target container) before uploading.
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
//	pg_dump -U $POSTGRES_USER $POSTGRES_DB
//
// using credentials from job.Credentials (POSTGRES_USER/PASSWORD/DB),
// gzips the output locally, and uploads it - see backup.RunAndRecord for
// the shared outcome-recording/webhook wrapper.
func (e *Executor) Backup(ctx context.Context, job *backup.Job) error {
	return backup.RunAndRecord(ctx, job, func() (string, error) { return e.backup(ctx, job) })
}

func (e *Executor) backup(ctx context.Context, job *backup.Job) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("postgres.Backup: job %s has no target container", job.Name)
	}
	user := job.Credentials["POSTGRES_USER"]
	db := job.Credentials["POSTGRES_DB"]
	if user == "" || db == "" {
		return "", fmt.Errorf("postgres.Backup: job %s is missing POSTGRES_USER/POSTGRES_DB credentials", job.Name)
	}

	env := map[string]string{}
	if pw := job.Credentials["POSTGRES_PASSWORD"]; pw != "" {
		env["PGPASSWORD"] = pw
	}

	// --clean --if-exists makes the dump include DROP statements before
	// each CREATE, so restoring it replaces existing data (a plain
	// pg_dump only ever INSERTs, leaving pre-restore rows in place
	// alongside the restored ones - confirmed by testing a restore
	// against a live, deliberately-corrupted table).
	dump, err := e.docker.Exec(ctx, job.ContainerName, []string{"pg_dump", "--clean", "--if-exists", "-U", user, db}, env, nil)
	if err != nil {
		return "", fmt.Errorf("pg_dump: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fileName := backup.ArchiveFileName(job.Name, backup.VolumeIdentifierOrFallback(job), "sql.gz")
	localPath := filepath.Join(tmpDir, fileName)
	if err := backup.GzipToFile(dump, localPath); err != nil {
		return "", fmt.Errorf("compressing dump: %w", err)
	}

	return backup.UploadArchive(ctx, e.rclone, job, localPath)
}
