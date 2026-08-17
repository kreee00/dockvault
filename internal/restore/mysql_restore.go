// mysql_restore.go restores a mysqldump via: mysql < restored_dump.sql.
package restore

import (
	"bytes"
	"context"
	"fmt"

	"dockvault/internal/backup"
)

type MysqlRestorer struct {
	docker backup.DockerClient
	rclone backup.RcloneClient
}

func NewMysqlRestorer(dockerClient backup.DockerClient, rcloneClient backup.RcloneClient) *MysqlRestorer {
	return &MysqlRestorer{docker: dockerClient, rclone: rcloneClient}
}

func (r *MysqlRestorer) Name() string { return "mysql" }

func (r *MysqlRestorer) ListRemoteBackups(ctx context.Context, job *backup.Job) ([]BackupFile, error) {
	return ListRemoteBackups(ctx, r.rclone, job)
}

// Restore downloads and gunzips file, then feeds the plain SQL to
// `mysql -u root $MYSQL_DATABASE` over stdin inside job's container
// (MYSQL_ROOT_PASSWORD via MYSQL_PWD env, never on argv).
func (r *MysqlRestorer) Restore(ctx context.Context, job *backup.Job, file BackupFile) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("mysql.Restore: job %s has no target container", job.Name)
	}
	db := job.Credentials["MYSQL_DATABASE"]
	rootPW := job.Credentials["MYSQL_ROOT_PASSWORD"]
	if db == "" || rootPW == "" {
		return "", fmt.Errorf("mysql.Restore: job %s is missing MYSQL_DATABASE/MYSQL_ROOT_PASSWORD credentials", job.Name)
	}

	sql, warning, err := downloadAndRead(ctx, r.rclone, file, true)
	if err != nil {
		return "", err
	}

	env := map[string]string{"MYSQL_PWD": rootPW}
	if _, err := r.docker.Exec(ctx, job.ContainerName, []string{"mysql", "-u", "root", db}, env, bytes.NewReader(sql)); err != nil {
		return "", fmt.Errorf("mysql: %w", err)
	}
	return warning, nil
}
