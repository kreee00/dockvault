// Package redis implements the Redis backup type: trigger BGSAVE, poll
// LASTSAVE until it advances, then `docker cp` the resulting dump.rdb out,
// per the spec's Backup Type 5.
package redis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dockvault/internal/backup"
)

const Name = "redis"

// pollInterval and maxPollAttempts control how long Backup waits for
// BGSAVE to complete before giving up. Both are vars (not consts) so
// tests can shrink pollInterval to avoid a real multi-second sleep -
// mirrors the detector package's currentGOOS test-seam idiom.
var (
	pollInterval    = 500 * time.Millisecond
	maxPollAttempts = 60 // 30s total at the default interval
)

// SetPollIntervalForTest overrides pollInterval (tests live in an external
// package, so this is the only way in) and returns a func that restores
// the previous value - call it via defer.
func SetPollIntervalForTest(d time.Duration) (restore func()) {
	prev := pollInterval
	pollInterval = d
	return func() { pollInterval = prev }
}

// Executor performs redis backups by triggering BGSAVE over redis-cli,
// waiting for it to finish, then copying the resulting RDB file out of
// the container with `docker cp`.
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
//	redis-cli [-a $REDIS_PASSWORD] BGSAVE
//	(poll LASTSAVE until it changes)
//	docker cp <container>:/data/dump.rdb <dest>
func (e *Executor) Backup(ctx context.Context, job *backup.Job) error {
	return backup.RunAndRecord(ctx, job, func() (string, error) { return e.backup(ctx, job) })
}

func (e *Executor) backup(ctx context.Context, job *backup.Job) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("redis.Backup: job %s has no target container", job.Name)
	}

	env := map[string]string{}
	if pw := job.Credentials["REDIS_PASSWORD"]; pw != "" {
		env["REDISCLI_AUTH"] = pw
	}

	before, err := e.lastSave(ctx, job.ContainerName, env)
	if err != nil {
		return "", fmt.Errorf("checking LASTSAVE: %w", err)
	}
	if _, err := e.docker.Exec(ctx, job.ContainerName, []string{"redis-cli", "BGSAVE"}, env, nil); err != nil {
		return "", fmt.Errorf("BGSAVE: %w", err)
	}
	if err := e.waitForSave(ctx, job.ContainerName, env, before); err != nil {
		return "", err
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fileName := backup.ArchiveFileName(job.Name, backup.VolumeIdentifierOrFallback(job), "rdb")
	localPath := filepath.Join(tmpDir, fileName)
	if err := e.docker.Cp(ctx, job.ContainerName+":/data/dump.rdb", localPath); err != nil {
		return "", fmt.Errorf("copying dump.rdb out of container: %w", err)
	}

	return backup.UploadArchive(ctx, e.rclone, job, localPath)
}

func (e *Executor) lastSave(ctx context.Context, container string, env map[string]string) (string, error) {
	out, err := e.docker.Exec(ctx, container, []string{"redis-cli", "LASTSAVE"}, env, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// waitForSave polls LASTSAVE until it differs from before (BGSAVE bumps
// it once the background save finishes) or maxPollAttempts is exhausted.
func (e *Executor) waitForSave(ctx context.Context, container string, env map[string]string, before string) error {
	for i := 0; i < maxPollAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
		after, err := e.lastSave(ctx, container, env)
		if err == nil && after != before {
			return nil
		}
	}
	return fmt.Errorf("timed out waiting for BGSAVE to complete on container %s", container)
}
