package tests

import (
	"fmt"
	"testing"
	"time"

	"dockvault/internal/rclone"
)

// fileAtAge builds a rclone.FileInfo whose ModTime is age old, newest
// first ordering assumed by FilesToPrune (matching what ListFiles
// actually returns).
func fileAtAge(name string, age time.Duration) rclone.FileInfo {
	return rclone.FileInfo{
		Name:    name,
		Path:    "/stage/host/postgres/job/" + name,
		ModTime: time.Now().Add(-age).Format(time.RFC3339),
	}
}

// TestFilesToPrune_NeverDropsBelowMinFilesToKeep is a regression test for
// a retention-safety bug found via audit: the old Prune() only checked
// "is the current file count > minFilesToKeep" before running a blanket
// `rclone delete --min-age`, which has no concept of a minimum-count
// floor - a backlog of old files could all get deleted in one call,
// leaving fewer than minFilesToKeep backups. This constructs exactly that
// backlog (35 files, 32 of them older than the 7-day retention window,
// min_files_to_keep=30) and asserts the newest 30 are never candidates
// for deletion, no matter how old the rest are.
func TestFilesToPrune_NeverDropsBelowMinFilesToKeep(t *testing.T) {
	const total = 35
	const minFilesToKeep = 30
	const retentionDays = 7

	files := make([]rclone.FileInfo, 0, total)
	for i := 0; i < total; i++ {
		// Newest-first: file 0 is today, file 34 is 34 days old - so 27
		// of these (ages 8..34) are older than the 7-day retention
		// window, well beyond what minFilesToKeep=30 alone would suggest
		// is "safe" to prune under the old (buggy) logic.
		files = append(files, fileAtAge(fmt.Sprintf("backup_%d.tar.gz", i), time.Duration(i)*24*time.Hour))
	}

	toDelete := rclone.FilesToPrune(files, retentionDays, minFilesToKeep)

	survivors := total - len(toDelete)
	if survivors < minFilesToKeep {
		t.Fatalf("FilesToPrune would leave only %d files, want at least minFilesToKeep=%d to survive", survivors, minFilesToKeep)
	}

	deletedNames := map[string]bool{}
	for _, f := range toDelete {
		deletedNames[f.Name] = true
	}
	for i := 0; i < minFilesToKeep; i++ {
		name := fmt.Sprintf("backup_%d.tar.gz", i)
		if deletedNames[name] {
			t.Errorf("FilesToPrune deleted %q, one of the newest %d files - it must never be a deletion candidate", name, minFilesToKeep)
		}
	}
}

func TestFilesToPrune_SkipsWhenAtOrUnderMinFilesToKeep(t *testing.T) {
	files := []rclone.FileInfo{
		fileAtAge("a.tar.gz", 100*24*time.Hour),
		fileAtAge("b.tar.gz", 100*24*time.Hour),
	}
	if got := rclone.FilesToPrune(files, 7, 5); got != nil {
		t.Errorf("FilesToPrune with fewer files than minFilesToKeep = %v, want nil (nothing pruned)", got)
	}
}

func TestFilesToPrune_LeavesRecentFilesEvenBeyondMinFilesToKeep(t *testing.T) {
	// 3 files, minFilesToKeep=1, retentionDays=30, but all 3 are recent
	// (1 day old) - only the age-eligible ones (past minFilesToKeep AND
	// past retentionDays) should be pruned; here that's none.
	files := []rclone.FileInfo{
		fileAtAge("a.tar.gz", 1*24*time.Hour),
		fileAtAge("b.tar.gz", 1*24*time.Hour),
		fileAtAge("c.tar.gz", 1*24*time.Hour),
	}
	if got := rclone.FilesToPrune(files, 30, 1); len(got) != 0 {
		t.Errorf("FilesToPrune = %v, want none pruned (all recent files are within retentionDays)", got)
	}
}
