package backup

import (
	"crypto/md5" //nolint:gosec // MD5 here is Google Drive's own exposed checksum format, used to catch upload corruption in transit - not for anything security-sensitive.
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"dockvault/internal/rclone"
)

// ArchiveManifest is the integrity sidecar written alongside every backup
// archive: SHA256 is what restore re-verifies against before touching a
// database or container, MD5 is what UploadArchive compares against
// Google Drive's own reported checksum immediately after upload (Drive
// only exposes MD5, not SHA256, so both are computed).
type ArchiveManifest struct {
	JobName    string    `json:"job_name"`
	BackupType string    `json:"backup_type"`
	FileName   string    `json:"file_name"`
	SHA256     string    `json:"sha256"`
	MD5        string    `json:"md5"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

// ComputeManifest hashes localPath in a single streaming pass (both
// SHA-256 and MD5 at once, via io.MultiWriter) and returns the resulting
// ArchiveManifest. Streaming, not a second full read into memory,
// matters here - archives can be multi-GB.
func ComputeManifest(job *Job, localPath string) (ArchiveManifest, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return ArchiveManifest{}, fmt.Errorf("opening %s to hash: %w", localPath, err)
	}
	defer f.Close()

	sha := sha256.New()
	md5h := md5.New()
	size, err := io.Copy(io.MultiWriter(sha, md5h), f)
	if err != nil {
		return ArchiveManifest{}, fmt.Errorf("hashing %s: %w", localPath, err)
	}

	return ArchiveManifest{
		JobName:    job.Name,
		BackupType: job.BackupType,
		FileName:   filepath.Base(localPath),
		SHA256:     hex.EncodeToString(sha.Sum(nil)),
		MD5:        hex.EncodeToString(md5h.Sum(nil)),
		SizeBytes:  size,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

// ManifestPathFor returns the local sidecar manifest path for an archive
// at localPath.
func ManifestPathFor(localPath string) string {
	return localPath + rclone.ManifestSuffix
}

// WriteManifest JSON-encodes m and writes it to path, mode 0600 - it sits
// next to a file (the archive) that may itself hold sensitive data, no
// reason for the manifest to default to more open permissions.
func WriteManifest(m ArchiveManifest, path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing manifest %s: %w", path, err)
	}
	return nil
}
