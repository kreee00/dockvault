package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
	"dockvault/internal/rclone"
)

// downloadManifest fetches file's companion integrity manifest
// (file.Path + rclone.ManifestSuffix) into destDir and parses it. A
// missing manifest, a download failure, or a parse failure are all
// reported the same way (ok=false, no error) rather than as a hard
// failure - backups taken before this feature existed have no manifest
// at all, and refusing to restore them over that would be a nasty
// surprise with no way to fix it retroactively. Only a manifest that
// downloads and parses cleanly is usable for verification.
func downloadManifest(ctx context.Context, rc backup.RcloneClient, file BackupFile, destDir string) (m backup.ArchiveManifest, ok bool) {
	remoteManifest := file.Path + rclone.ManifestSuffix
	if err := rc.Download(ctx, remoteManifest, destDir); err != nil {
		return backup.ArchiveManifest{}, false
	}
	data, err := os.ReadFile(filepath.Join(destDir, file.Name+rclone.ManifestSuffix))
	if err != nil {
		return backup.ArchiveManifest{}, false
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return backup.ArchiveManifest{}, false
	}
	return m, true
}

// missingManifestWarning is returned whenever downloadManifest can't
// produce a usable manifest - the same message regardless of the exact
// underlying reason (missing, download failure, parse failure), since to
// an operator reading it, "there's nothing to verify against" is the
// whole story.
const missingManifestWarning = "restored without integrity verification - no usable backup manifest found (this backup may predate integrity verification, or its manifest failed to download)"

// verifyManifestBytes checks a manifest for file against the SHA-256 of
// data (bytes already downloaded and, if applicable, decompressed by the
// caller - callers should call this before decompressing to verify the
// exact bytes that were uploaded, not a derived form of them). destDir is
// used as scratch space for the manifest download itself.
func verifyManifestBytes(ctx context.Context, rc backup.RcloneClient, file BackupFile, destDir string, data []byte) (warning string, err error) {
	m, ok := downloadManifest(ctx, rc, file, destDir)
	if !ok {
		return missingManifestWarning, nil
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != m.SHA256 {
		return "", fmt.Errorf("integrity check failed for %s: expected sha256 %s, got %s - refusing to restore", file.Name, m.SHA256, got)
	}
	return "", nil
}

// verifyManifestFile is verifyManifestBytes's counterpart for restore
// types that download straight to disk without reading the file into
// memory (standard/hostdir/n8n/redis) - streams localPath through
// SHA-256 instead of taking the bytes directly, so a large archive isn't
// read into memory a second time just to verify it.
func verifyManifestFile(ctx context.Context, rc backup.RcloneClient, file BackupFile, destDir, localPath string) (warning string, err error) {
	m, ok := downloadManifest(ctx, rc, file, destDir)
	if !ok {
		return missingManifestWarning, nil
	}

	f, ferr := os.Open(localPath)
	if ferr != nil {
		return "", fmt.Errorf("opening %s to verify: %w", localPath, ferr)
	}
	defer f.Close()

	h := sha256.New()
	if _, cerr := io.Copy(h, f); cerr != nil {
		return "", fmt.Errorf("hashing %s to verify: %w", localPath, cerr)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != m.SHA256 {
		return "", fmt.Errorf("integrity check failed for %s: expected sha256 %s, got %s - refusing to restore", file.Name, m.SHA256, got)
	}
	return "", nil
}
