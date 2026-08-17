package tests

import (
	"context"
	"crypto/md5" //nolint:gosec // verifying against Drive's own checksum format, not for anything security-sensitive.
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dockvault/internal/backup"
)

func TestComputeManifest_MatchesKnownHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive.tar.gz")
	content := []byte("fake archive contents for hashing")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	job := &backup.Job{Name: "job", BackupType: "standard"}
	m, err := backup.ComputeManifest(job, path)
	if err != nil {
		t.Fatalf("ComputeManifest returned error: %v", err)
	}

	wantSHA := sha256.Sum256(content)
	if m.SHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Errorf("SHA256 = %q, want %q", m.SHA256, hex.EncodeToString(wantSHA[:]))
	}
	wantMD5 := md5.Sum(content)
	if m.MD5 != hex.EncodeToString(wantMD5[:]) {
		t.Errorf("MD5 = %q, want %q", m.MD5, hex.EncodeToString(wantMD5[:]))
	}
	if m.SizeBytes != int64(len(content)) {
		t.Errorf("SizeBytes = %d, want %d", m.SizeBytes, len(content))
	}
	if m.JobName != "job" || m.BackupType != "standard" || m.FileName != "archive.tar.gz" {
		t.Errorf("manifest = %+v, unexpected identity fields", m)
	}
}

func TestUploadArchive_WritesAndUploadsManifest(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "job_vol_20260101-000000Z.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	mockRclone := &mockRcloneClient{}
	job := &backup.Job{Name: "job", BackupType: "standard", GoogleDrivePath: "/stage/host/standard/job/"}

	if _, err := backup.UploadArchive(context.Background(), mockRclone, job, archivePath); err != nil {
		t.Fatalf("UploadArchive returned error: %v", err)
	}

	if len(mockRclone.uploadCalls) != 2 {
		t.Fatalf("uploadCalls = %v, want 2 (archive then manifest)", mockRclone.uploadCalls)
	}
	if mockRclone.uploadCalls[0].Local != archivePath {
		t.Errorf("first upload = %q, want the archive %q", mockRclone.uploadCalls[0].Local, archivePath)
	}
	manifestLocal := mockRclone.uploadCalls[1].Local
	if manifestLocal != archivePath+".manifest.json" {
		t.Errorf("second upload = %q, want %q", manifestLocal, archivePath+".manifest.json")
	}
	if _, err := os.Stat(manifestLocal); err != nil {
		t.Errorf("manifest file %q wasn't actually written locally: %v", manifestLocal, err)
	}
}

func TestUploadArchive_RefusesOnRemoteHashMismatch(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "job_vol_20260101-000000Z.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	mockRclone := &mockRcloneClient{hashsumOverride: "0000000000000000000000000000ff"} // deliberately wrong
	job := &backup.Job{Name: "job", BackupType: "standard", GoogleDrivePath: "/stage/host/standard/job/"}

	_, err := backup.UploadArchive(context.Background(), mockRclone, job, archivePath)
	if err == nil {
		t.Fatal("expected a remote hash mismatch to fail the backup")
	}
}

func TestUploadArchive_RemoteHashMatchSucceeds(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "job_vol_20260101-000000Z.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	mockRclone := &mockRcloneClient{} // default Hashsum behavior: honest remote, always matches
	job := &backup.Job{Name: "job", BackupType: "standard", GoogleDrivePath: "/stage/host/standard/job/"}

	status, err := backup.UploadArchive(context.Background(), mockRclone, job, archivePath)
	if err != nil {
		t.Fatalf("UploadArchive returned error: %v", err)
	}
	if len(mockRclone.hashsumCalls) != 1 {
		t.Errorf("hashsumCalls = %v, want exactly 1 (verifying the archive, not the manifest)", mockRclone.hashsumCalls)
	}
	if !strings.HasPrefix(status, "success") {
		t.Errorf("status = %q, want it to start with \"success\"", status)
	}
}

// TestUploadArchive_ManifestUploadFailureDoesNotFailBackup fails only the
// 2nd Upload call (the manifest) - the 1st (the archive) succeeds - and
// confirms UploadArchive still returns success overall, with a warning
// folded into the status string rather than a hard error. A manifest
// that fails to upload doesn't invalidate an otherwise-good backup; the
// operator should still see it happened, which the warning provides.
func TestUploadArchive_ManifestUploadFailureDoesNotFailBackup(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "job_vol_20260101-000000Z.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	mockRclone := &mockRcloneClient{
		uploadErrOnCall: map[int]error{1: fmt.Errorf("simulated manifest upload failure")},
	}
	job := &backup.Job{Name: "job", BackupType: "standard", GoogleDrivePath: "/stage/host/standard/job/"}

	status, err := backup.UploadArchive(context.Background(), mockRclone, job, archivePath)
	if err != nil {
		t.Fatalf("UploadArchive returned error: %v, want success with a warning", err)
	}
	if !strings.Contains(status, "manifest upload warning") {
		t.Errorf("status = %q, want it to mention the manifest upload warning", status)
	}
	if len(mockRclone.uploadCalls) != 2 {
		t.Errorf("uploadCalls = %v, want 2 (the archive upload still happened)", mockRclone.uploadCalls)
	}
}
