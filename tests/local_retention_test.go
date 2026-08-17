package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dockvault/internal/backup"
)

// TestRetainLocalCopy_KeepsOnlyNewestN mirrors TestFilesToPrune_...'s
// construct-the-exact-scenario rigor for the local-copy path: 5 aged
// archives (+ their manifests) already on disk, LocalRetentionCount=2,
// assert exactly the 2 newest survive and the rest (both archive and
// manifest) are gone.
func TestRetainLocalCopy_KeepsOnlyNewestN(t *testing.T) {
	localDir := t.TempDir()
	jobDir := filepath.Join(localDir, "job")
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	now := time.Now()
	names := make([]string, 5)
	for i := 0; i < 5; i++ {
		name := "job_vol_" + string(rune('a'+i)) + ".tar.gz"
		names[i] = name
		path := filepath.Join(jobDir, name)
		if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
		if err := os.WriteFile(path+".manifest.json", []byte("{}"), 0o600); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
		// i=0 is oldest (5 days old), i=4 is newest (1 day old).
		age := time.Duration(5-i) * 24 * time.Hour
		mtime := now.Add(-age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes returned error: %v", err)
		}
	}

	// Build a fresh "backup" archive (what a real UploadArchive call would
	// be retaining alongside the 5 already on disk) so the newest-first
	// list has 6 candidates total and LocalRetentionCount=2 has to
	// actually prune something.
	newArchive := filepath.Join(t.TempDir(), "job_vol_new.tar.gz")
	if err := os.WriteFile(newArchive, []byte("new archive"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	job := &backup.Job{
		Name:                "job",
		BackupType:          "standard",
		GoogleDrivePath:     "/stage/host/standard/job/",
		LocalBackupDir:      localDir,
		LocalRetentionCount: 2,
	}
	mockRclone := &mockRcloneClient{}
	if _, err := backup.UploadArchive(context.Background(), mockRclone, job, newArchive); err != nil {
		t.Fatalf("UploadArchive returned error: %v", err)
	}

	entries, err := os.ReadDir(jobDir)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	var archives []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".manifest.json") {
			archives = append(archives, e.Name())
		}
	}
	if len(archives) != 2 {
		t.Fatalf("archives remaining = %v, want exactly 2 (newest 1 pre-existing + the just-retained new one)", archives)
	}
	// The two newest pre-existing archives were index 3 and 4 (1-2 days
	// old); the brand new one is newer than all of them.
	foundNew, foundNewestOld := false, false
	for _, a := range archives {
		if strings.Contains(a, "job_vol_new") {
			foundNew = true
		}
		if a == names[4] {
			foundNewestOld = true
		}
	}
	if !foundNew {
		t.Errorf("archives = %v, want the just-retained new archive to survive", archives)
	}
	if !foundNewestOld {
		t.Errorf("archives = %v, want the newest pre-existing archive (%s) to survive", archives, names[4])
	}
	if _, err := os.Stat(filepath.Join(jobDir, names[0]+".manifest.json")); !os.IsNotExist(err) {
		t.Errorf("expected the oldest archive's manifest to be pruned alongside it, got err=%v", err)
	}
}

func TestRetainLocalCopy_DisabledWhenRetentionCountZero(t *testing.T) {
	localDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	job := &backup.Job{
		Name: "job", BackupType: "standard", GoogleDrivePath: "/stage/host/standard/job/",
		LocalBackupDir: localDir, LocalRetentionCount: 0,
	}
	status, err := backup.UploadArchive(context.Background(), &mockRcloneClient{}, job, archivePath)
	if err != nil {
		t.Fatalf("UploadArchive returned error: %v", err)
	}
	if strings.Contains(status, "local") {
		t.Errorf("status = %q, want no local-copy warning when disabled", status)
	}
	entries, _ := os.ReadDir(localDir)
	if len(entries) != 0 {
		t.Errorf("localDir has %d entries, want 0 - local retention is disabled (LocalRetentionCount=0)", len(entries))
	}
}

// TestRetainLocalCopy_EmptyBackupDirIsAlsoDisabled is a regression test
// found during real-container verification: a job with a nonzero
// LocalRetentionCount but an empty LocalBackupDir (e.g. a hand-edited or
// pre-upgrade job.json missing the field) must not silently retain copies
// into a CWD-relative directory - it must be treated the same as
// disabled, exactly like LocalRetentionCount<=0 already is.
func TestRetainLocalCopy_EmptyBackupDirIsAlsoDisabled(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	suspiciousDir := filepath.Join(cwd, "job")
	defer os.RemoveAll(suspiciousDir)

	archivePath := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	job := &backup.Job{
		Name: "job", BackupType: "standard", GoogleDrivePath: "/stage/host/standard/job/",
		LocalBackupDir: "", LocalRetentionCount: 2, // the dangerous combination
	}
	if _, err := backup.UploadArchive(context.Background(), &mockRcloneClient{}, job, archivePath); err != nil {
		t.Fatalf("UploadArchive returned error: %v", err)
	}

	if _, err := os.Stat(suspiciousDir); !os.IsNotExist(err) {
		t.Errorf("expected no %s to be created (that would mean it fell back to a CWD-relative path), stat err=%v", suspiciousDir, err)
	}
}

func TestRetainLocalCopy_FailureIsWarningNotError(t *testing.T) {
	// Point LocalBackupDir at a path that can't be a directory (a file
	// sits where the job subdirectory needs to be created).
	base := t.TempDir()
	blocker := filepath.Join(base, "job")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	archivePath := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	job := &backup.Job{
		Name: "job", BackupType: "standard", GoogleDrivePath: "/stage/host/standard/job/",
		LocalBackupDir: base, LocalRetentionCount: 2,
	}
	status, err := backup.UploadArchive(context.Background(), &mockRcloneClient{}, job, archivePath)
	if err != nil {
		t.Fatalf("UploadArchive returned error: %v, want success with a warning (remote upload still succeeded)", err)
	}
	if !strings.Contains(status, "local copy warning") {
		t.Errorf("status = %q, want it to mention a local copy warning", status)
	}
}
