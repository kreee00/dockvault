package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"dockvault/internal/backup"
	"dockvault/internal/workspace"
)

func newTestWorkspace(t *testing.T) *workspace.Workspace {
	t.Helper()
	return &workspace.Workspace{Root: t.TempDir()}
}

func TestSaveLoadJob_RoundTrip(t *testing.T) {
	ws := newTestWorkspace(t)

	job := &backup.Job{
		Name:                "postgres_appdb",
		BackupType:          "postgres",
		ServiceType:         "postgres",
		VolumeID:            "pgdata_test",
		VolumeIdentifier:    "pgdata_test",
		ContainerName:       "test-postgres",
		Credentials:         map[string]string{"POSTGRES_USER": "appuser", "POSTGRES_PASSWORD": "testpass123", "POSTGRES_DB": "appdb"},
		GoogleDrivePath:     "/stage/host/postgres/postgres_appdb/",
		RetentionDays:       30,
		MinFilesToKeep:      30,
		WebhookURL:          "https://example.com/hook",
		LocalBackupDir:      "/mnt/backups",
		LocalRetentionCount: 2,
	}

	if err := ws.SaveJob(job); err != nil {
		t.Fatalf("SaveJob returned error: %v", err)
	}

	got, err := ws.LoadJob("postgres_appdb")
	if err != nil {
		t.Fatalf("LoadJob returned error: %v", err)
	}

	if got.Name != job.Name || got.BackupType != job.BackupType || got.VolumeID != job.VolumeID ||
		got.ContainerName != job.ContainerName || got.GoogleDrivePath != job.GoogleDrivePath ||
		got.RetentionDays != job.RetentionDays || got.MinFilesToKeep != job.MinFilesToKeep ||
		got.LocalBackupDir != job.LocalBackupDir || got.LocalRetentionCount != job.LocalRetentionCount {
		t.Errorf("LoadJob round-trip mismatch: got %+v, want %+v", got, job)
	}
	if got.WebhookURL != job.WebhookURL {
		t.Errorf("WebhookURL = %q, want %q", got.WebhookURL, job.WebhookURL)
	}
	for k, v := range job.Credentials {
		if got.Credentials[k] != v {
			t.Errorf("Credentials[%q] = %q, want %q", k, got.Credentials[k], v)
		}
	}
	if _, ok := got.Credentials["DOCKVAULT_WEBHOOK_URL"]; ok {
		t.Error("webhook URL leaked into Credentials")
	}

	// .env must be mode 600 since it can hold secrets.
	info, err := os.Stat(ws.EnvPath("postgres_appdb"))
	if err != nil {
		t.Fatalf("stat .env: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestSaveJob_NoCredentialsNoEnvFile(t *testing.T) {
	ws := newTestWorkspace(t)
	job := &backup.Job{Name: "hostdir_etc_nginx", BackupType: "hostdir", HostPath: "/etc/nginx", GoogleDrivePath: "/stage/host/hostdir/hostdir_etc_nginx/"}

	if err := ws.SaveJob(job); err != nil {
		t.Fatalf("SaveJob returned error: %v", err)
	}
	if _, err := os.Stat(ws.EnvPath("hostdir_etc_nginx")); err == nil {
		t.Error("expected no .env file to be written when there are no credentials/webhook")
	}
}

func TestListJobNames(t *testing.T) {
	ws := newTestWorkspace(t)
	for _, name := range []string{"b_job", "a_job", "c_job"} {
		if err := ws.SaveJob(&backup.Job{Name: name, BackupType: "standard"}); err != nil {
			t.Fatalf("SaveJob(%q) returned error: %v", name, err)
		}
	}

	names, err := ws.ListJobNames()
	if err != nil {
		t.Fatalf("ListJobNames returned error: %v", err)
	}
	want := []string{"a_job", "b_job", "c_job"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q (expected alphabetical order)", i, names[i], want[i])
		}
	}
}

func TestConfig_DefaultsWhenMissing(t *testing.T) {
	ws := newTestWorkspace(t)
	cfg, err := ws.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.Environment != "stage" || cfg.RcloneRemote != "dockvault_backup" || cfg.RetentionDays != 30 {
		t.Errorf("LoadConfig() with no config.json = %+v, want the documented defaults", cfg)
	}
	if cfg.LocalRetentionCount != 14 {
		t.Errorf("LocalRetentionCount = %d, want the documented default of 14", cfg.LocalRetentionCount)
	}
	wantDir := filepath.Join(ws.Root, "local_backups")
	if cfg.LocalBackupDir != wantDir {
		t.Errorf("LocalBackupDir = %q, want %q (resolved under the workspace root)", cfg.LocalBackupDir, wantDir)
	}
}

func TestConfig_ExplicitLocalBackupDirIsPreserved(t *testing.T) {
	ws := newTestWorkspace(t)
	if err := ws.SaveConfig(workspace.Config{LocalBackupDir: "/mnt/nas/backups", LocalRetentionCount: 5}); err != nil {
		t.Fatalf("SaveConfig returned error: %v", err)
	}

	cfg, err := ws.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.LocalBackupDir != "/mnt/nas/backups" {
		t.Errorf("LocalBackupDir = %q, want the explicitly configured path left untouched", cfg.LocalBackupDir)
	}
	if cfg.LocalRetentionCount != 5 {
		t.Errorf("LocalRetentionCount = %d, want 5", cfg.LocalRetentionCount)
	}
}

func TestManifest_UpsertRoundTrip(t *testing.T) {
	ws := newTestWorkspace(t)
	now := time.Now().UTC()

	m, err := ws.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest returned error: %v", err)
	}
	m.Upsert(workspace.ManifestEntry{JobName: "job_a", LastBackup: &now, LastBackupStatus: "success (1.0 MB)"})
	if err := ws.SaveManifest(m); err != nil {
		t.Fatalf("SaveManifest returned error: %v", err)
	}

	got, err := ws.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest (reload) returned error: %v", err)
	}
	if len(got.Jobs) != 1 || got.Jobs[0].JobName != "job_a" || got.Jobs[0].LastBackupStatus != "success (1.0 MB)" {
		t.Errorf("reloaded manifest = %+v, want one entry for job_a", got.Jobs)
	}
}
