package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dockvault/internal/backup"
	"dockvault/internal/rclone"
	"dockvault/internal/restore"
)

// manifestJSONFor builds a valid ArchiveManifest JSON payload recording
// sha256 (hex) - used to configure a mockRcloneClient's downloadByRemote
// for a backup file's manifest sidecar.
func manifestJSONFor(t *testing.T, sha256Hex string) []byte {
	t.Helper()
	data, err := json.Marshal(backup.ArchiveManifest{SHA256: sha256Hex})
	if err != nil {
		t.Fatalf("manifestJSONFor: %v", err)
	}
	return data
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// wrongSHA256 is a syntactically valid but never-actually-matching
// SHA-256 hex digest (64 hex chars), for tests that need a manifest to
// deliberately disagree with whatever was actually downloaded.
var wrongSHA256 = strings.Repeat("0", 64)

// TestRestoreExecutorNames mirrors TestExecutorNames for the restore side.
func TestRestoreExecutorNames(t *testing.T) {
	registry := restore.NewRegistry(
		restore.NewStandardRestorer(nil, nil),
		restore.NewPostgresRestorer(nil, nil),
		restore.NewMysqlRestorer(nil, nil),
		restore.NewMongodbRestorer(nil, nil),
		restore.NewRedisRestorer(nil, nil),
		restore.NewN8nRestorer(nil, nil),
		restore.NewHostdirRestorer(nil, nil),
		restore.NewRabbitmqRestorer(nil, nil),
	)

	for _, name := range []string{"standard", "postgres", "mysql", "mongodb", "redis", "n8n", "hostdir", "rabbitmq"} {
		if _, ok := registry[name]; !ok {
			t.Errorf("expected restore executor %q to be registered", name)
		}
	}
}

func TestListRemoteBackups_NewestFirst(t *testing.T) {
	mockRclone := &mockRcloneClient{
		listFiles: []rclone.FileInfo{
			{Name: "job_vol_20260101-000000Z.tar.gz", ModTime: "2026-01-01T00:00:00Z", Size: 100},
			{Name: "job_vol_20260301-000000Z.tar.gz", ModTime: "2026-03-01T00:00:00Z", Size: 300},
			{Name: "job_vol_20260201-000000Z.tar.gz", ModTime: "2026-02-01T00:00:00Z", Size: 200},
		},
	}
	job := &backup.Job{Name: "job", GoogleDrivePath: "/stage/host/standard/job/"}

	files, err := restore.ListRemoteBackups(context.Background(), mockRclone, job)
	if err != nil {
		t.Fatalf("ListRemoteBackups returned error: %v", err)
	}
	// rclone.Client.ListFiles is documented to return newest-first already
	// (the mock here returns its listFiles verbatim), so this just checks
	// the BackupFile conversion, not re-sorting.
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3", len(files))
	}
	want, _ := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	if !files[0].Modified.Equal(want) {
		t.Errorf("files[0].Modified = %v, want %v", files[0].Modified, want)
	}
}

func TestStandardRestore_Success(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{}
	restorer := restore.NewStandardRestorer(mockDocker, mockRclone)

	job := &backup.Job{Name: "job", VolumeID: "job_vol"}
	file := restore.BackupFile{Name: "job_vol_20260101-000000Z.tar.gz", Path: "/stage/host/standard/job/job_vol_20260101-000000Z.tar.gz"}

	if _, err := restorer.Restore(context.Background(), job, file); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if mockDocker.gotMounts[0].Source != "job_vol" || mockDocker.gotMounts[0].Target != "/target" {
		t.Errorf("mounts = %v, want first mount to target job_vol -> /target", mockDocker.gotMounts)
	}
	found := false
	for _, arg := range mockDocker.gotCmd {
		if arg == "xzf" {
			found = true
		}
	}
	if !found {
		t.Errorf("cmd = %v, want tar extraction (xzf)", mockDocker.gotCmd)
	}
}

func TestPostgresRestore_FeedsDecompressedSQLOverStdin(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{downloadInto: gzipBytes(t, "INSERT INTO widgets VALUES (1, 'sprocket');")}
	restorer := restore.NewPostgresRestorer(mockDocker, mockRclone)

	job := &backup.Job{
		Name:          "postgres_appdb",
		ContainerName: "test-postgres",
		Credentials:   map[string]string{"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "testpass123", "POSTGRES_DB": "appdb"},
	}
	file := restore.BackupFile{
		Name: "postgres_appdb_pgdata_20260101-000000Z.sql.gz",
		Path: "/stage/host/postgres/postgres_appdb/postgres_appdb_pgdata_20260101-000000Z.sql.gz",
	}

	if _, err := restorer.Restore(context.Background(), job, file); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if len(mockDocker.execCalls) != 1 {
		t.Fatalf("expected exactly one Exec call, got %d", len(mockDocker.execCalls))
	}
	call := mockDocker.execCalls[0]
	if call.Cmd[0] != "psql" {
		t.Errorf("Exec cmd[0] = %q, want %q", call.Cmd[0], "psql")
	}
	if !strings.Contains(string(call.Stdin), "sprocket") {
		t.Errorf("stdin = %q, want it to contain the decompressed SQL", call.Stdin)
	}
}

func TestRedisRestore_StopsButDoesNotRestart(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{}
	restorer := restore.NewRedisRestorer(mockDocker, mockRclone)

	job := &backup.Job{Name: "redis_cache", ContainerName: "test-redis"}
	file := restore.BackupFile{
		Name: "redis_cache_vol_20260101-000000Z.rdb",
		Path: "/stage/host/redis/redis_cache/redis_cache_vol_20260101-000000Z.rdb",
	}

	if _, err := restorer.Restore(context.Background(), job, file); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if len(mockDocker.stopCalls) != 1 || mockDocker.stopCalls[0] != "test-redis" {
		t.Errorf("stopCalls = %v, want [test-redis]", mockDocker.stopCalls)
	}
	if len(mockDocker.startCalls) != 0 {
		t.Errorf("startCalls = %v, want none - restore must not auto-restart (see resolved design decision)", mockDocker.startCalls)
	}
	if len(mockDocker.cpCalls) != 1 || mockDocker.cpCalls[0].Dst != "test-redis:/data/dump.rdb" {
		t.Errorf("cpCalls = %v, want a copy into test-redis:/data/dump.rdb", mockDocker.cpCalls)
	}
}

func TestPostgresRestore_RefusesOnManifestMismatch(t *testing.T) {
	archive := gzipBytes(t, "INSERT INTO widgets VALUES (1, 'sprocket');")
	file := restore.BackupFile{
		Name: "postgres_appdb_pgdata_20260101-000000Z.sql.gz",
		Path: "/stage/host/postgres/postgres_appdb/postgres_appdb_pgdata_20260101-000000Z.sql.gz",
	}
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{
		downloadByRemote: map[string][]byte{
			file.Path:                         archive,
			file.Path + rclone.ManifestSuffix: manifestJSONFor(t, wrongSHA256),
		},
	}
	restorer := restore.NewPostgresRestorer(mockDocker, mockRclone)
	job := &backup.Job{
		Name: "postgres_appdb", ContainerName: "test-postgres",
		Credentials: map[string]string{"POSTGRES_USER": "appuser", "POSTGRES_DB": "appdb"},
	}

	_, err := restorer.Restore(context.Background(), job, file)
	if err == nil {
		t.Fatal("expected a manifest hash mismatch to refuse the restore")
	}
	if len(mockDocker.execCalls) != 0 {
		t.Errorf("execCalls = %v, want none - psql must never run against unverified data", mockDocker.execCalls)
	}
}

func TestPostgresRestore_MatchingManifestNoWarning(t *testing.T) {
	archive := gzipBytes(t, "INSERT INTO widgets VALUES (1, 'sprocket');")
	file := restore.BackupFile{
		Name: "postgres_appdb_pgdata_20260101-000000Z.sql.gz",
		Path: "/stage/host/postgres/postgres_appdb/postgres_appdb_pgdata_20260101-000000Z.sql.gz",
	}
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{
		downloadByRemote: map[string][]byte{
			file.Path:                         archive,
			file.Path + rclone.ManifestSuffix: manifestJSONFor(t, sha256Hex(archive)),
		},
	}
	restorer := restore.NewPostgresRestorer(mockDocker, mockRclone)
	job := &backup.Job{
		Name: "postgres_appdb", ContainerName: "test-postgres",
		Credentials: map[string]string{"POSTGRES_USER": "appuser", "POSTGRES_DB": "appdb"},
	}

	warning, err := restorer.Restore(context.Background(), job, file)
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want none - the manifest matched", warning)
	}
	if len(mockDocker.execCalls) != 1 {
		t.Errorf("execCalls = %v, want exactly one (psql actually ran)", mockDocker.execCalls)
	}
}

func TestStandardRestore_MissingManifestWarnsButProceeds(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{} // no manifest configured anywhere
	restorer := restore.NewStandardRestorer(mockDocker, mockRclone)

	job := &backup.Job{Name: "job", VolumeID: "job_vol"}
	file := restore.BackupFile{Name: "job_vol_20260101-000000Z.tar.gz", Path: "/stage/host/standard/job/job_vol_20260101-000000Z.tar.gz"}

	warning, err := restorer.Restore(context.Background(), job, file)
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if warning == "" {
		t.Error("expected a non-empty warning when no manifest is available")
	}
	if mockDocker.gotImage == "" {
		t.Error("expected the tar extraction to still have happened despite the missing manifest")
	}
}

func TestN8nRestore_RefusesBeforeStoppingContainerOnMismatch(t *testing.T) {
	file := restore.BackupFile{Name: "n8n_data_vol_20260101-000000Z.tar.gz", Path: "/stage/host/n8n/n8n_data/n8n_data_vol_20260101-000000Z.tar.gz"}
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{
		downloadByRemote: map[string][]byte{
			file.Path:                         []byte("real archive bytes"),
			file.Path + rclone.ManifestSuffix: manifestJSONFor(t, wrongSHA256),
		},
	}
	restorer := restore.NewN8nRestorer(mockDocker, mockRclone)
	job := &backup.Job{Name: "n8n_data", ContainerName: "test-n8n", VolumeID: "n8n_data"}

	_, err := restorer.Restore(context.Background(), job, file)
	if err == nil {
		t.Fatal("expected a manifest hash mismatch to refuse the restore")
	}
	if len(mockDocker.stopCalls) != 0 {
		t.Errorf("stopCalls = %v, want none - the container must never be touched on a verification failure", mockDocker.stopCalls)
	}
}

func TestRedisRestore_RefusesBeforeStoppingContainerOnMismatch(t *testing.T) {
	file := restore.BackupFile{Name: "redis_cache_vol_20260101-000000Z.rdb", Path: "/stage/host/redis/redis_cache/redis_cache_vol_20260101-000000Z.rdb"}
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{
		downloadByRemote: map[string][]byte{
			file.Path:                         []byte("real rdb bytes"),
			file.Path + rclone.ManifestSuffix: manifestJSONFor(t, wrongSHA256),
		},
	}
	restorer := restore.NewRedisRestorer(mockDocker, mockRclone)
	job := &backup.Job{Name: "redis_cache", ContainerName: "test-redis"}

	_, err := restorer.Restore(context.Background(), job, file)
	if err == nil {
		t.Fatal("expected a manifest hash mismatch to refuse the restore")
	}
	if len(mockDocker.stopCalls) != 0 {
		t.Errorf("stopCalls = %v, want none - the container must never be touched on a verification failure", mockDocker.stopCalls)
	}
}

func TestRabbitmqRestore_ImportsDefinitions(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{downloadInto: gzipBytes(t, `{"queues":[{"name":"orders"}]}`)}
	restorer := restore.NewRabbitmqRestorer(mockDocker, mockRclone)

	job := &backup.Job{Name: "rabbitmq_broker", ContainerName: "test-rabbitmq"}
	file := restore.BackupFile{
		Name: "rabbitmq_broker_data_20260101-000000Z.json.gz",
		Path: "/stage/host/rabbitmq/rabbitmq_broker/rabbitmq_broker_data_20260101-000000Z.json.gz",
	}

	if _, err := restorer.Restore(context.Background(), job, file); err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if len(mockDocker.cpCalls) != 1 || mockDocker.cpCalls[0].Dst != "test-rabbitmq:/tmp/dockvault-restore-definitions.json" {
		t.Errorf("cpCalls = %v, want a copy into test-rabbitmq:/tmp/dockvault-restore-definitions.json", mockDocker.cpCalls)
	}
	// One Exec for import_definitions, one for best-effort cleanup.
	if len(mockDocker.execCalls) != 2 {
		t.Fatalf("expected exactly 2 Exec calls (import + cleanup), got %d: %v", len(mockDocker.execCalls), mockDocker.execCalls)
	}
	importCall := mockDocker.execCalls[0]
	if importCall.Cmd[0] != "rabbitmqctl" || importCall.Cmd[1] != "import_definitions" {
		t.Errorf("Exec cmd = %v, want rabbitmqctl import_definitions ...", importCall.Cmd)
	}
	if len(mockDocker.stopCalls) != 0 || len(mockDocker.startCalls) != 0 {
		t.Errorf("stopCalls=%v startCalls=%v, want none - RabbitMQ definitions import doesn't need the broker stopped", mockDocker.stopCalls, mockDocker.startCalls)
	}
}

func TestRabbitmqRestore_RefusesBeforeTouchingBrokerOnMismatch(t *testing.T) {
	file := restore.BackupFile{Name: "rabbitmq_broker_data_20260101-000000Z.json.gz", Path: "/stage/host/rabbitmq/rabbitmq_broker/rabbitmq_broker_data_20260101-000000Z.json.gz"}
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{
		downloadByRemote: map[string][]byte{
			file.Path:                         gzipBytes(t, `{"queues":[]}`),
			file.Path + rclone.ManifestSuffix: manifestJSONFor(t, wrongSHA256),
		},
	}
	restorer := restore.NewRabbitmqRestorer(mockDocker, mockRclone)
	job := &backup.Job{Name: "rabbitmq_broker", ContainerName: "test-rabbitmq"}

	_, err := restorer.Restore(context.Background(), job, file)
	if err == nil {
		t.Fatal("expected a manifest hash mismatch to refuse the restore")
	}
	if len(mockDocker.cpCalls) != 0 || len(mockDocker.execCalls) != 0 {
		t.Errorf("cpCalls=%v execCalls=%v, want none - the broker must never be touched on a verification failure", mockDocker.cpCalls, mockDocker.execCalls)
	}
}
