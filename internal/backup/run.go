package backup

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"dockvault/internal/utils"
)

// RunAndRecord wraps fn (an executor's core backup logic) with the
// bookkeeping every executor needs: set Job.LastBackup/LastBackupStatus on
// success or failure, and fire the job's webhook (a webhook failure never
// masks the real backup result - see SendWebhook's doc comment). fn
// returns the human-readable success status (e.g. "success (2.3 MB)") on
// success.
func RunAndRecord(ctx context.Context, job *Job, fn func() (string, error)) error {
	status, err := fn()

	now := time.Now().UTC()
	job.LastBackup = &now
	if err != nil {
		job.LastBackupStatus = "failed: " + err.Error()
	} else {
		job.LastBackupStatus = status
	}

	if job.WebhookURL != "" {
		payload := utils.WebhookPayload{
			Job:       job.Name,
			Status:    "success",
			Message:   job.LastBackupStatus,
			Timestamp: now,
		}
		if err != nil {
			payload.Status = "failure"
		}
		// Webhook delivery failures shouldn't fail an otherwise-successful
		// backup - log-and-continue per the project's error conventions.
		_ = utils.SendWebhook(ctx, job.WebhookURL, payload)
	}

	return err
}

// PruneWithWarning runs job's retention pruning (skipped if RetentionDays
// <= 0) and appends a warning suffix to status if pruning fails - a prune
// failure shouldn't turn a successful backup into a failed one, but it
// should stay visible in the recorded status.
func PruneWithWarning(ctx context.Context, rc RcloneClient, job *Job, status string) string {
	if job.RetentionDays <= 0 {
		return status
	}
	if _, err := rc.Prune(ctx, job.GoogleDrivePath, job.RetentionDays, job.MinFilesToKeep); err != nil {
		return fmt.Sprintf("%s, prune warning: %v", status, err)
	}
	return status
}

// UploadArchive uploads localPath (an already-produced backup file) to
// job.GoogleDrivePath, then applies PruneWithWarning, returning the final
// status string. Shared by every executor - each one produces a local
// file (tar, dump, or docker-cp'd file) before calling this, while its
// own os.MkdirTemp dir (and therefore localPath) is still alive - the
// defer that removes it fires only after the executor's own function
// returns, which happens after this one does.
//
// Order matters here and is deliberate:
//  1. Compute + write the integrity manifest (SHA256 for restore-time
//     re-verification, MD5 to compare against what Drive reports back).
//  2. Retain a local durable copy of the archive + manifest, regardless
//     of whether the remote leg below succeeds - this is what makes
//     dockvault actually satisfy 3-2-1's "2 independent media" leg, and
//     gating it on remote success would defeat its own purpose: if the
//     remote upload fails, or (step 4) succeeds but arrives corrupted,
//     the local copy is the only good one that exists at all.
//  3. Upload the archive, then the manifest.
//  4. Verify the archive's remote MD5 against what was just computed
//     locally - a mismatch is a hard failure (not a warning), since
//     "upload returned success" isn't reliable evidence of a good backup
//     on its own; see DOCKVAULT_CONTEXT.md's audit notes on this.
//  5. Prune old remote copies (PruneWithWarning, unchanged).
func UploadArchive(ctx context.Context, rc RcloneClient, job *Job, localPath string) (string, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("reading archive output: %w", err)
	}

	status := ""
	manifestPath := ""
	manifest, merr := ComputeManifest(job, localPath)
	if merr != nil {
		status = fmt.Sprintf(", manifest warning: %v", merr)
	} else {
		manifestPath = ManifestPathFor(localPath)
		if werr := WriteManifest(manifest, manifestPath); werr != nil {
			manifestPath = ""
			status = fmt.Sprintf(", manifest warning: %v", werr)
		}
	}

	status = retainLocalCopy(job, localPath, manifestPath, status)

	if err := rc.Upload(ctx, localPath, job.GoogleDrivePath); err != nil {
		return "", fmt.Errorf("uploading to %s: %w", job.GoogleDrivePath, err)
	}
	if manifestPath != "" {
		if uerr := rc.Upload(ctx, manifestPath, job.GoogleDrivePath); uerr != nil {
			status = fmt.Sprintf("%s, manifest upload warning: %v", status, uerr)
		}
	}

	if manifestPath != "" {
		remoteFile := path.Join(job.GoogleDrivePath, filepath.Base(localPath))
		if err := verifyRemoteHash(ctx, rc, remoteFile, manifest.MD5); err != nil {
			return "", err
		}
	}

	status = fmt.Sprintf("success (%s)%s", FormatBytes(info.Size()), status)
	return PruneWithWarning(ctx, rc, job, status), nil
}

// verifyRemoteHash fetches remoteFile's MD5 as reported by Drive and
// compares it to localMD5 (already computed by ComputeManifest). A
// mismatch means the bytes that landed on Drive don't match what was
// sent - a real, hard backup failure, not a warning to append to an
// otherwise-successful status.
func verifyRemoteHash(ctx context.Context, rc RcloneClient, remoteFile, localMD5 string) error {
	remoteMD5, err := rc.Hashsum(ctx, remoteFile)
	if err != nil {
		return fmt.Errorf("verifying upload integrity for %s: %w", remoteFile, err)
	}
	if remoteMD5 != localMD5 {
		return fmt.Errorf("upload integrity check failed for %s: local md5 %s != remote md5 %s (the uploaded object doesn't match what was sent)", remoteFile, localMD5, remoteMD5)
	}
	return nil
}

// UniqueRemoteDirs returns each distinct job.GoogleDrivePath across jobs,
// in first-seen order. Used by the CLI to pre-create every destination
// directory sequentially before running jobs in parallel - see
// rclone.Client.Mkdir's doc comment for why that ordering matters.
func UniqueRemoteDirs(jobs []*Job) []string {
	seen := make(map[string]bool, len(jobs))
	dirs := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job.GoogleDrivePath == "" || seen[job.GoogleDrivePath] {
			continue
		}
		seen[job.GoogleDrivePath] = true
		dirs = append(dirs, job.GoogleDrivePath)
	}
	return dirs
}

// VolumeIdentifierOrFallback returns job.VolumeIdentifier if set, falling
// back to VolumeID, then a generic "data" placeholder - used when building
// an archive filename for a job that predates VolumeIdentifier being
// populated (e.g. constructed by hand rather than through the generator).
func VolumeIdentifierOrFallback(job *Job) string {
	if job.VolumeIdentifier != "" {
		return job.VolumeIdentifier
	}
	if job.VolumeID != "" {
		return job.VolumeID
	}
	return "data"
}
