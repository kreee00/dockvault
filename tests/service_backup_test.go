package tests

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"dockvault/internal/backup"
	"dockvault/internal/backup/hostdir"
	"dockvault/internal/backup/mongodb"
	"dockvault/internal/backup/mysql"
	"dockvault/internal/backup/n8n"
	"dockvault/internal/backup/postgres"
	"dockvault/internal/backup/redis"
)

func TestPostgresBackup_Success(t *testing.T) {
	mockDocker := &mockDockerClient{execOutput: []byte("-- fake pg_dump output\n")}
	mockRclone := &mockRcloneClient{}
	executor := postgres.New(mockDocker, mockRclone)

	job := &backup.Job{
		Name:            "postgres_appdb",
		ContainerName:   "test-postgres",
		Credentials:     map[string]string{"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "testpass123", "POSTGRES_DB": "appdb"},
		GoogleDrivePath: "/stage/host/postgres/postgres_appdb/",
	}

	if err := executor.Backup(context.Background(), job); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}
	if !mockRclone.uploadCalled {
		t.Fatal("expected rclone.Upload to be called")
	}
	if len(mockRclone.uploadCalls) < 1 || !strings.HasSuffix(mockRclone.uploadCalls[0].Local, ".sql.gz") {
		t.Errorf("first uploaded file = %q, want a .sql.gz suffix (archive uploads before its manifest)", mockRclone.uploadCalls)
	}
	if len(mockRclone.uploadCalls) != 2 || !strings.HasSuffix(mockRclone.uploadCalls[1].Local, ".manifest.json") {
		t.Errorf("uploadCalls = %v, want exactly 2 (archive then manifest)", mockRclone.uploadCalls)
	}
	if len(mockDocker.execCalls) != 1 {
		t.Fatalf("expected exactly one Exec call, got %d", len(mockDocker.execCalls))
	}
	call := mockDocker.execCalls[0]
	if call.Container != "test-postgres" {
		t.Errorf("Exec container = %q, want %q", call.Container, "test-postgres")
	}
	if call.Cmd[0] != "pg_dump" {
		t.Errorf("Exec cmd[0] = %q, want %q", call.Cmd[0], "pg_dump")
	}
	if call.Env["PGPASSWORD"] != "testpass123" {
		t.Errorf("Exec env PGPASSWORD = %q, want %q", call.Env["PGPASSWORD"], "testpass123")
	}
}

func TestPostgresBackup_MissingCredentials(t *testing.T) {
	executor := postgres.New(&mockDockerClient{}, &mockRcloneClient{})
	job := &backup.Job{Name: "job", ContainerName: "c"}

	if err := executor.Backup(context.Background(), job); err == nil {
		t.Fatal("expected an error when POSTGRES_USER/POSTGRES_DB are missing")
	}
}

func TestMysqlBackup_Success(t *testing.T) {
	mockDocker := &mockDockerClient{execOutput: []byte("-- fake mysqldump output\n")}
	mockRclone := &mockRcloneClient{}
	executor := mysql.New(mockDocker, mockRclone)

	job := &backup.Job{
		Name:            "mysql_shop",
		ContainerName:   "test-mysql",
		Credentials:     map[string]string{"MYSQL_DATABASE": "shop", "MYSQL_ROOT_PASSWORD": "hunter2"},
		GoogleDrivePath: "/stage/host/mysql/mysql_shop/",
	}

	if err := executor.Backup(context.Background(), job); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}
	call := mockDocker.execCalls[0]
	if call.Cmd[0] != "mysqldump" {
		t.Errorf("Exec cmd[0] = %q, want %q", call.Cmd[0], "mysqldump")
	}
	if !strings.Contains(strings.Join(call.Cmd, " "), "--single-transaction") {
		t.Errorf("Exec cmd = %v, want --single-transaction (without it, InnoDB tables aren't guaranteed a consistent snapshot under concurrent writes)", call.Cmd)
	}
	if call.Env["MYSQL_PWD"] != "hunter2" {
		t.Errorf("Exec env MYSQL_PWD = %q, want %q", call.Env["MYSQL_PWD"], "hunter2")
	}
	for _, arg := range call.Cmd {
		if strings.Contains(arg, "hunter2") {
			t.Errorf("password leaked onto argv: %v", call.Cmd)
		}
	}
}

func TestMongodbBackup_Success(t *testing.T) {
	mockDocker := &mockDockerClient{execOutput: []byte("fake mongodump archive")}
	mockRclone := &mockRcloneClient{}
	executor := mongodb.New(mockDocker, mockRclone)

	job := &backup.Job{
		Name:            "mongodb_appdb",
		ContainerName:   "test-mongo",
		Credentials:     map[string]string{"MONGO_INITDB_ROOT_USERNAME": "root", "MONGO_INITDB_ROOT_PASSWORD": "s3cret"},
		GoogleDrivePath: "/stage/host/mongodb/mongodb_appdb/",
	}

	if err := executor.Backup(context.Background(), job); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}
	call := mockDocker.execCalls[0]
	if call.Cmd[0] != "mongodump" {
		t.Errorf("Exec cmd[0] = %q, want %q", call.Cmd[0], "mongodump")
	}
	joined := strings.Join(call.Cmd, " ")
	if !strings.Contains(joined, "--username root") {
		t.Errorf("Exec cmd = %v, want --username root", call.Cmd)
	}
	if len(mockRclone.uploadCalls) < 1 || !strings.HasSuffix(mockRclone.uploadCalls[0].Local, ".archive.gz") {
		t.Errorf("first uploaded file = %q, want an .archive.gz suffix", mockRclone.uploadCalls)
	}
}

func TestRedisBackup_SuccessWithPolling(t *testing.T) {
	restore := redis.SetPollIntervalForTest(1)
	defer restore()

	lastsaveCalls := 0
	mockDocker := &mockDockerClient{}
	mockDocker.execFunc = func(ctx context.Context, container string, cmd []string, env map[string]string, stdin io.Reader) ([]byte, error) {
		if len(cmd) >= 2 && cmd[0] == "redis-cli" && cmd[1] == "LASTSAVE" {
			lastsaveCalls++
			if lastsaveCalls == 1 {
				return []byte("1000\n"), nil
			}
			return []byte("2000\n"), nil // BGSAVE "completed" by the second poll
		}
		return nil, nil
	}
	mockRclone := &mockRcloneClient{}
	executor := redis.New(mockDocker, mockRclone)

	job := &backup.Job{
		Name:            "redis_cache",
		ContainerName:   "test-redis",
		Credentials:     map[string]string{"REDIS_PASSWORD": "hunter2"},
		GoogleDrivePath: "/stage/host/redis/redis_cache/",
	}

	if err := executor.Backup(context.Background(), job); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}
	if len(mockDocker.cpCalls) != 1 {
		t.Fatalf("expected exactly one docker cp call, got %d", len(mockDocker.cpCalls))
	}
	if !strings.Contains(mockDocker.cpCalls[0].Src, ":/data/dump.rdb") {
		t.Errorf("cp src = %q, want it to reference /data/dump.rdb", mockDocker.cpCalls[0].Src)
	}
	if len(mockRclone.uploadCalls) < 1 || !strings.HasSuffix(mockRclone.uploadCalls[0].Local, ".rdb") {
		t.Errorf("first uploaded file = %q, want an .rdb suffix", mockRclone.uploadCalls)
	}
}

func TestN8nBackup_RestartsContainerEvenOnFailure(t *testing.T) {
	mockDocker := &mockDockerClient{runErr: errors.New("tar failed")}
	mockRclone := &mockRcloneClient{}
	executor := n8n.New(mockDocker, mockRclone)

	job := &backup.Job{Name: "n8n_data", ContainerName: "test-n8n", VolumeID: "n8n_data", GoogleDrivePath: "/stage/host/n8n/n8n_data/"}

	if err := executor.Backup(context.Background(), job); err == nil {
		t.Fatal("expected the tar failure to propagate")
	}
	if len(mockDocker.stopCalls) != 1 || mockDocker.stopCalls[0] != "test-n8n" {
		t.Errorf("stopCalls = %v, want [test-n8n]", mockDocker.stopCalls)
	}
	if len(mockDocker.startCalls) != 1 || mockDocker.startCalls[0] != "test-n8n" {
		t.Errorf("startCalls = %v, want [test-n8n] (must restart even though tar failed)", mockDocker.startCalls)
	}
}

func TestN8nBackup_Success(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{}
	executor := n8n.New(mockDocker, mockRclone)

	job := &backup.Job{Name: "n8n_data", ContainerName: "test-n8n", VolumeID: "n8n_data", GoogleDrivePath: "/stage/host/n8n/n8n_data/"}

	if err := executor.Backup(context.Background(), job); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}
	if len(mockDocker.stopCalls) != 1 || len(mockDocker.startCalls) != 1 {
		t.Errorf("expected exactly one stop and one start, got stop=%v start=%v", mockDocker.stopCalls, mockDocker.startCalls)
	}
	if !mockRclone.uploadCalled {
		t.Error("expected rclone.Upload to be called")
	}
}

func TestHostdirBackup_Success(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{}
	executor := hostdir.New(mockDocker, mockRclone)

	job := &backup.Job{Name: "nginx_hostdir", HostPath: "/etc/nginx", VolumeIdentifier: "bind-nginx", GoogleDrivePath: "/stage/host/hostdir/nginx_hostdir/"}

	if err := executor.Backup(context.Background(), job); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}
	if mockDocker.gotMounts[0].Source != "/etc/nginx" {
		t.Errorf("mount source = %q, want %q", mockDocker.gotMounts[0].Source, "/etc/nginx")
	}
}

func TestHostdirBackup_NoHostPath(t *testing.T) {
	executor := hostdir.New(&mockDockerClient{}, &mockRcloneClient{})
	job := &backup.Job{Name: "broken"}

	if err := executor.Backup(context.Background(), job); err == nil {
		t.Fatal("expected an error for a job with no HostPath")
	}
}
