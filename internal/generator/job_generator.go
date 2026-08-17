// Package generator turns detector output into on-disk jobs: it creates
// each job's directory (job metadata + an optional .env for
// credentials/webhook) under $DOCKVAULT_HOME, per the spec's `generate`
// command. There is no script to render - `dockvault backup`/`restore`
// execute jobs directly via internal/backup and internal/restore's
// executors.
package generator

import (
	"context"
	"fmt"

	"dockvault/internal/backup"
	"dockvault/internal/detector"
	"dockvault/internal/workspace"
)

// Generator orchestrates job creation.
type Generator struct {
	Workspace *workspace.Workspace
}

// New returns a Generator bound to a workspace.
func New(ws *workspace.Workspace) *Generator {
	return &Generator{Workspace: ws}
}

// ExecutorNameForServiceType maps a detector service type to the
// BackupExecutor/RestoreExecutor Name() that handles it. Every service
// type is handled by the identically-named executor except
// ServiceGeneric, which has no dedicated executor and uses "standard"
// (a plain volume tar) instead.
func ExecutorNameForServiceType(serviceType string) string {
	if serviceType == detector.ServiceGeneric {
		return "standard"
	}
	return serviceType
}

// jobFromVolume builds a *backup.Job from a detector.VolumeInfo and the
// workspace's config defaults. v.GoogleDrivePath and v.VolumeIdentifier
// (both already in detector's standardized forms) are copied verbatim -
// this function doesn't compute or reformat either one.
func jobFromVolume(v detector.VolumeInfo, cfg workspace.Config) *backup.Job {
	var containerName string
	if len(v.Containers) > 0 {
		containerName = v.Containers[0]
	}
	return &backup.Job{
		Name:                v.JobName,
		BackupType:          ExecutorNameForServiceType(v.ServiceType),
		ServiceType:         v.ServiceType,
		VolumeID:            v.Name,
		VolumeIdentifier:    v.VolumeIdentifier,
		ContainerName:       containerName,
		Credentials:         v.Credentials,
		GoogleDrivePath:     v.GoogleDrivePath,
		RetentionDays:       cfg.RetentionDays,
		MinFilesToKeep:      cfg.MinFilesToKeep,
		WebhookURL:          cfg.WebhookURL,
		LocalBackupDir:      cfg.LocalBackupDir,
		LocalRetentionCount: cfg.LocalRetentionCount,
	}
}

// jobFromHostPath is jobFromVolume's counterpart for a
// detector.HostPathInfo (the "Host Directory" backup type). Only
// "bind-mount" and "notable-path" kinds are jobbable - "env-file" entries
// carry no JobName/GoogleDrivePath and should be skipped by the caller
// before this is reached.
func jobFromHostPath(h detector.HostPathInfo, cfg workspace.Config) *backup.Job {
	return &backup.Job{
		Name:                h.JobName,
		BackupType:          "hostdir",
		ServiceType:         detector.ServiceHostDir,
		HostPath:            h.Path,
		VolumeIdentifier:    h.VolumeIdentifier,
		ContainerName:       h.Source, // informational only; hostdir doesn't use it
		GoogleDrivePath:     h.GoogleDrivePath,
		RetentionDays:       cfg.RetentionDays,
		MinFilesToKeep:      cfg.MinFilesToKeep,
		WebhookURL:          cfg.WebhookURL,
		LocalBackupDir:      cfg.LocalBackupDir,
		LocalRetentionCount: cfg.LocalRetentionCount,
	}
}

// CreateFromVolume builds a job from v and the workspace's config
// defaults and writes it to disk (job.json + .env, per
// workspace.SaveJob), unless dryRun is set, in which case it only builds
// and returns the job without touching disk - callers use this to print a
// preview.
func (g *Generator) CreateFromVolume(ctx context.Context, v detector.VolumeInfo, dryRun bool) (*backup.Job, error) {
	cfg, err := g.Workspace.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading workspace config: %w", err)
	}
	job := jobFromVolume(v, cfg)
	if dryRun {
		return job, nil
	}
	if err := g.Workspace.SaveJob(job); err != nil {
		return nil, fmt.Errorf("saving job %q: %w", job.Name, err)
	}
	return job, nil
}

// CreateFromHostPath does the equivalent of CreateFromVolume for a
// detector.HostPathInfo (the "Host Directory" backup type).
func (g *Generator) CreateFromHostPath(ctx context.Context, h detector.HostPathInfo, dryRun bool) (*backup.Job, error) {
	if h.JobName == "" {
		return nil, fmt.Errorf("generator.CreateFromHostPath: %s (kind=%s) has no proposed job name - not jobbable", h.Path, h.Kind)
	}
	cfg, err := g.Workspace.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("loading workspace config: %w", err)
	}
	job := jobFromHostPath(h, cfg)
	if dryRun {
		return job, nil
	}
	if err := g.Workspace.SaveJob(job); err != nil {
		return nil, fmt.Errorf("saving job %q: %w", job.Name, err)
	}
	return job, nil
}
