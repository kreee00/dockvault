// Package mysql implements the MySQL backup type: mysqldump | gzip, same
// credential-detection pattern as postgres, per the spec's Backup Type 3.
package mysql

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
)

const Name = "mysql"

// Executor performs MySQL backups via `docker exec ... mysqldump`,
// gzipping the output itself before uploading.
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
//	mysqldump -u root $MYSQL_DATABASE   (MYSQL_ROOT_PASSWORD via MYSQL_PWD env, never on argv)
func (e *Executor) Backup(ctx context.Context, job *backup.Job) error {
	return backup.RunAndRecord(ctx, job, func() (string, error) { return e.backup(ctx, job) })
}

func (e *Executor) backup(ctx context.Context, job *backup.Job) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("mysql.Backup: job %s has no target container", job.Name)
	}
	db := job.Credentials["MYSQL_DATABASE"]
	rootPW := job.Credentials["MYSQL_ROOT_PASSWORD"]
	if db == "" || rootPW == "" {
		return "", fmt.Errorf("mysql.Backup: job %s is missing MYSQL_DATABASE/MYSQL_ROOT_PASSWORD credentials", job.Name)
	}

	// MYSQL_PWD (an env var the mysql client tools read directly) keeps
	// the password off argv, unlike the -p<password> form.
	env := map[string]string{"MYSQL_PWD": rootPW}
	// --single-transaction wraps the dump in one REPEATABLE READ
	// transaction so InnoDB tables get a consistent snapshot even under
	// concurrent writes - without it, mysqldump has no such guarantee
	// (each table is read at whatever point mysqldump reaches it, so
	// concurrent writes during the dump can leave the backup internally
	// inconsistent across tables/rows). Found via reliability audit: this
	// flag was missing. Doesn't help MyISAM tables, which need table
	// locks instead, but InnoDB is the default/common engine.
	cmd := []string{"mysqldump", "--single-transaction", "-u", "root", db}
	dump, err := e.docker.Exec(ctx, job.ContainerName, cmd, env, nil)
	if err != nil {
		return "", fmt.Errorf("mysqldump: %w", err)
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
