// Package rabbitmq implements the RabbitMQ backup type: `rabbitmqctl
// export_definitions`, not a raw data-directory copy.
//
// This is a deliberate scope decision, not an oversight: RabbitMQ's data
// directory is a live Mnesia database that doesn't tolerate being tarred
// while the broker is running (a torn read of Mnesia's files risks a
// corrupt, unrestorable copy), and a file-level restore would additionally
// require an exact-matching RabbitMQ/Erlang version on the target host.
// `export_definitions` instead captures topology - exchanges, queues,
// bindings, users (hashed passwords), permissions, and policies - as a
// small, versionless JSON document that RabbitMQ can re-import onto any
// compatible version. The trade-off: in-flight message bodies are not
// backed up. That's the standard, RabbitMQ-recommended approach - message
// queues are meant to be transient, not a durable data store - and
// there's no safer alternative that doesn't reintroduce the live-copy
// corruption risk above.
package rabbitmq

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
)

const Name = "rabbitmq"

// definitionsPathInContainer is a fixed scratch path inside the target
// container - export_definitions/import_definitions both take a file path
// argument, not stdin/stdout.
const definitionsPathInContainer = "/tmp/dockvault-definitions.json"

// Executor performs RabbitMQ backups by exporting broker definitions to a
// file inside the container, copying it out via `docker cp`, then
// gzipping it locally before upload.
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
//	rabbitmqctl export_definitions /tmp/dockvault-definitions.json
//
// then `docker cp`s the result out and gzips it locally. No credentials
// are needed: rabbitmqctl authenticates via the Erlang cookie file already
// present in the container (the same mechanism the broker's own local CLI
// tooling relies on) - `docker exec` runs as the image's default user,
// which already has cookie access.
func (e *Executor) Backup(ctx context.Context, job *backup.Job) error {
	return backup.RunAndRecord(ctx, job, func() (string, error) { return e.backup(ctx, job) })
}

func (e *Executor) backup(ctx context.Context, job *backup.Job) (string, error) {
	if job.ContainerName == "" {
		return "", fmt.Errorf("rabbitmq.Backup: job %s has no target container", job.Name)
	}

	// Deferred before the export attempt (not after, unlike n8n's
	// Stop-then-deferred-Start) since `rm -f` is a no-op on a path that
	// was never created - this also cleans up a partial write if
	// export_definitions crashes mid-write rather than failing outright.
	// Best-effort throughout: a leftover definitions export (hashed
	// passwords, topology) is a minor exposure worth avoiding, but not
	// worth failing an otherwise-successful backup over.
	defer func() {
		_, _ = e.docker.Exec(ctx, job.ContainerName, []string{"rm", "-f", definitionsPathInContainer}, nil, nil)
	}()

	if _, err := e.docker.Exec(ctx, job.ContainerName, []string{"rabbitmqctl", "export_definitions", definitionsPathInContainer}, nil, nil); err != nil {
		return "", fmt.Errorf("rabbitmqctl export_definitions: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "dockvault-"+job.Name+"-")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	rawPath := filepath.Join(tmpDir, "definitions.json")
	if err := e.docker.Cp(ctx, job.ContainerName+":"+definitionsPathInContainer, rawPath); err != nil {
		return "", fmt.Errorf("copying definitions out of container: %w", err)
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		return "", fmt.Errorf("reading copied definitions: %w", err)
	}

	fileName := backup.ArchiveFileName(job.Name, backup.VolumeIdentifierOrFallback(job), "json.gz")
	localPath := filepath.Join(tmpDir, fileName)
	if err := backup.GzipToFile(raw, localPath); err != nil {
		return "", fmt.Errorf("compressing definitions: %w", err)
	}

	return backup.UploadArchive(ctx, e.rclone, job, localPath)
}
