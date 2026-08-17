// postgres_restore.go restores a pg_dump via: psql < restored_dump.sql.
package restore

import (
	"bytes"
	"context"
	"fmt"

	"dockvault/internal/backup"
)

type PostgresRestorer struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

func NewPostgresRestorer(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *PostgresRestorer {
	return &PostgresRestorer{docker: dockerClient, rclone: rcloneClient}
}

func (r *PostgresRestorer) Name() string { return "postgres" }

func (r *PostgresRestorer) ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error) {
	return ListRemoteBackups(ctx, r.rclone, job)
}

// Restore downloads and gunzips file, then feeds the plain SQL to
// `psql -U $POSTGRES_USER $POSTGRES_DB` over stdin inside job's container.
func (r *PostgresRestorer) Restore(ctx context.Context, job *backup.Job, file BackupFile) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("postgres.Restore: job %s has no target container", job.Name)
	}
	user := job.Credentials["POSTGRES_USER"]
	db := job.Credentials["POSTGRES_DB"]
	if user == "" || db == "" {
		return "", fmt.Errorf("postgres.Restore: job %s is missing POSTGRES_USER/POSTGRES_DB credentials", job.Name)
	}

	sql, warning, err := downloadAndRead(ctx, r.rclone, file, true)
	if err != nil {
		return "", err
	}

	env := map[string]string{}
	if pw := job.Credentials["POSTGRES_PASSWORD"]; pw != "" {
		env["PGPASSWORD"] = pw
	}

	if _, err := r.docker.Exec(ctx, job.ContainerName, []string{"psql", "-U", user, db}, env, bytes.NewReader(sql)); err != nil {
		return "", fmt.Errorf("psql: %w", err)
	}
	return warning, nil
}
