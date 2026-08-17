package tests

import (
	"testing"

	"dockvault/internal/backup"
	"dockvault/internal/backup/hostdir"
	"dockvault/internal/backup/mongodb"
	"dockvault/internal/backup/mysql"
	"dockvault/internal/backup/n8n"
	"dockvault/internal/backup/postgres"
	"dockvault/internal/backup/rabbitmq"
	"dockvault/internal/backup/redis"
	"dockvault/internal/backup/standard"
)

// TestExecutorNames locks in the Name() each executor reports, since
// internal/detector.RecommendedBackupType and the generator's registry
// lookups both depend on these strings matching exactly.
func TestExecutorNames(t *testing.T) {
	registry := backup.NewRegistry(
		standard.New(nil, nil),
		postgres.New(nil, nil),
		mysql.New(nil, nil),
		mongodb.New(nil, nil),
		redis.New(nil, nil),
		n8n.New(nil, nil),
		hostdir.New(nil, nil),
		rabbitmq.New(nil, nil),
	)

	for _, name := range []string{"standard", "postgres", "mysql", "mongodb", "redis", "n8n", "hostdir", "rabbitmq"} {
		if _, ok := registry[name]; !ok {
			t.Errorf("expected executor %q to be registered", name)
		}
	}
}

// TestUniqueRemoteDirs_DedupsPreservingOrder is the regression test for a
// bug found via real Google Shared Drive testing: running jobs whose
// GoogleDrivePath shares an ancestor directory in parallel raced against
// Drive's lack of an atomic "create directory if missing", producing
// duplicate same-named folders and breaking post-upload hash
// verification ("directory not found"). The fix pre-creates every unique
// destination directory sequentially before parallel job execution -
// this locks in the dedup step that makes that possible.
func TestUniqueRemoteDirs_DedupsPreservingOrder(t *testing.T) {
	jobs := []*backup.Job{
		{Name: "a", GoogleDrivePath: "/stage/host/postgres/a/"},
		{Name: "b", GoogleDrivePath: "/stage/host/redis/b/"},
		{Name: "c", GoogleDrivePath: "/stage/host/postgres/a/"}, // duplicate of "a"'s dir
		{Name: "d", GoogleDrivePath: "/stage/host/hostdir/d/"},
		{Name: "e", GoogleDrivePath: ""}, // no destination yet - skip, not a real dir
	}

	got := backup.UniqueRemoteDirs(jobs)
	want := []string{"/stage/host/postgres/a/", "/stage/host/redis/b/", "/stage/host/hostdir/d/"}
	if len(got) != len(want) {
		t.Fatalf("UniqueRemoteDirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("UniqueRemoteDirs[%d] = %q, want %q (order must be first-seen, not sorted)", i, got[i], want[i])
		}
	}
}
