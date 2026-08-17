package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dockvault/internal/rclone"
)

// retainLocalCopy copies localPath (and manifestPath, if non-empty) into
// job.LocalBackupDir/job.Name/, then prunes that directory down to the
// newest job.LocalRetentionCount archives - reusing rclone.FilesToPrune's
// exact floor-safe algorithm (retentionDays=0 makes it a pure "keep only
// the newest N, regardless of age" rule: every real file's ModTime is
// necessarily before "now", so every candidate beyond the newest N
// qualifies for deletion).
//
// No-ops if job.LocalRetentionCount <= 0 (mirrors RetentionDays <= 0
// disabling remote pruning) or if job.LocalBackupDir is empty. The empty
// check matters even though workspace.LoadConfig always resolves a real
// default path, because a job loaded from a hand-edited or older job.json
// could have a nonzero LocalRetentionCount with no LocalBackupDir at all
// (found during real-container verification testing) - without this
// check, filepath.Join(job.LocalBackupDir, job.Name) with an empty
// LocalBackupDir silently becomes a relative path, retaining copies into
// whatever directory dockvault happens to be run from instead of
// anywhere predictable.
//
// Best-effort throughout: any failure appends a warning to status rather
// than returning an error - the remote copy (or the fact this ran before
// the remote leg at all - see UploadArchive) is what a failed local copy
// must never be allowed to jeopardize.
func retainLocalCopy(job *Job, localPath, manifestPath, status string) string {
	if job.LocalRetentionCount <= 0 || job.LocalBackupDir == "" {
		return status
	}

	dir := filepath.Join(job.LocalBackupDir, job.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Sprintf("%s, local copy warning: creating %s: %v", status, dir, err)
	}
	_ = os.Chmod(dir, 0o700)

	dstArchive := filepath.Join(dir, filepath.Base(localPath))
	if err := copyFile(localPath, dstArchive); err != nil {
		return fmt.Sprintf("%s, local copy warning: %v", status, err)
	}
	if manifestPath != "" {
		if err := copyFile(manifestPath, filepath.Join(dir, filepath.Base(manifestPath))); err != nil {
			// The archive copy already succeeded - a missing local
			// manifest doesn't invalidate it, just note it and continue.
			status = fmt.Sprintf("%s, local manifest copy warning: %v", status, err)
		}
	}

	if err := pruneLocalCopies(dir, job.LocalRetentionCount); err != nil {
		return fmt.Sprintf("%s, local prune warning: %v", status, err)
	}
	return status
}

// pruneLocalCopies deletes archives (and their manifest sidecars) beyond
// the newest keep in dir.
func pruneLocalCopies(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("listing %s: %w", dir, err)
	}

	var files []rclone.FileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, rclone.ManifestSuffix) {
			continue // manifests are pruned alongside their archive below, not independently counted
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, rclone.FileInfo{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
			Path:    filepath.Join(dir, name),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime > files[j].ModTime })

	for _, f := range rclone.FilesToPrune(files, 0, keep) {
		if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w", f.Path, err)
		}
		_ = os.Remove(f.Path + rclone.ManifestSuffix)
	}
	return nil
}

// copyFile copies src to dst, mode 0600 (dst's parent directory is
// already 0700 - see EnsureLayout's doc comment for the same convention
// used throughout the workspace package).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copying %s -> %s: %w", src, dst, err)
	}
	return nil
}
