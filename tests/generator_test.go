package tests

import (
	"context"
	"testing"

	"dockvault/internal/detector"
	"dockvault/internal/generator"
	"dockvault/internal/workspace"
)

func TestExecutorNameForServiceType(t *testing.T) {
	cases := []struct {
		serviceType string
		want        string
	}{
		{detector.ServicePostgres, "postgres"},
		{detector.ServiceMySQL, "mysql"},
		{detector.ServiceMongoDB, "mongodb"},
		{detector.ServiceRedis, "redis"},
		{detector.ServiceN8N, "n8n"},
		{detector.ServiceHostDir, "hostdir"},
		{detector.ServiceGeneric, "standard"},
	}
	for _, c := range cases {
		if got := generator.ExecutorNameForServiceType(c.serviceType); got != c.want {
			t.Errorf("ExecutorNameForServiceType(%q) = %q, want %q", c.serviceType, got, c.want)
		}
	}
}

func TestCreateFromVolume_DryRunDoesNotWrite(t *testing.T) {
	ws := &workspace.Workspace{Root: t.TempDir()}
	gen := generator.New(ws)

	v := detector.VolumeInfo{
		Name:                  "pgdata_test",
		Containers:            []string{"test-postgres"},
		ServiceType:           detector.ServicePostgres,
		Credentials:           map[string]string{"POSTGRES_USER": "appuser", "POSTGRES_DB": "appdb"},
		RecommendedBackupType: detector.BackupPgDump,
		JobName:               "postgres_appdb",
		GoogleDrivePath:       "/stage/host/postgres/postgres_appdb/",
		VolumeIdentifier:      "pgdata_test",
	}

	job, err := gen.CreateFromVolume(context.Background(), v, true)
	if err != nil {
		t.Fatalf("CreateFromVolume returned error: %v", err)
	}
	if job.Name != "postgres_appdb" || job.BackupType != "postgres" || job.ContainerName != "test-postgres" {
		t.Errorf("job = %+v, unexpected fields", job)
	}

	names, err := ws.ListJobNames()
	if err != nil {
		t.Fatalf("ListJobNames returned error: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("dry-run wrote job(s) to disk: %v", names)
	}
}

func TestCreateFromVolume_WritesJobAndAppliesConfigDefaults(t *testing.T) {
	ws := &workspace.Workspace{Root: t.TempDir()}
	if err := ws.SaveConfig(workspace.Config{
		RcloneRemote: "dockvault_backup", Environment: "stage",
		RetentionDays: 14, MinFilesToKeep: 7, WebhookURL: "https://example.com/hook",
		LocalBackupDir: "/mnt/backups", LocalRetentionCount: 3,
	}); err != nil {
		t.Fatalf("SaveConfig returned error: %v", err)
	}
	gen := generator.New(ws)

	v := detector.VolumeInfo{
		Name: "redis_data", ServiceType: detector.ServiceRedis,
		JobName: "redis_backup", GoogleDrivePath: "/stage/host/redis/redis_backup/",
		VolumeIdentifier: "redis_data",
	}

	job, err := gen.CreateFromVolume(context.Background(), v, false)
	if err != nil {
		t.Fatalf("CreateFromVolume returned error: %v", err)
	}
	if job.RetentionDays != 14 || job.MinFilesToKeep != 7 || job.WebhookURL != "https://example.com/hook" {
		t.Errorf("job didn't pick up config defaults: %+v", job)
	}
	if job.LocalBackupDir != "/mnt/backups" || job.LocalRetentionCount != 3 {
		t.Errorf("job didn't pick up local-retention config defaults: %+v", job)
	}

	reloaded, err := ws.LoadJob("redis_backup")
	if err != nil {
		t.Fatalf("LoadJob returned error: %v", err)
	}
	if reloaded.BackupType != "redis" || reloaded.GoogleDrivePath != v.GoogleDrivePath {
		t.Errorf("reloaded job = %+v, want it to match what was generated", reloaded)
	}
	if reloaded.LocalBackupDir != "/mnt/backups" || reloaded.LocalRetentionCount != 3 {
		t.Errorf("reloaded job local-retention fields = %+v, want them to survive the job.json round-trip", reloaded)
	}
}

func TestCreateFromHostPath_RejectsEnvFileEntries(t *testing.T) {
	ws := &workspace.Workspace{Root: t.TempDir()}
	gen := generator.New(ws)

	h := detector.HostPathInfo{Path: "/opt/app/.env", Kind: "env-file"} // no JobName - not jobbable

	if _, err := gen.CreateFromHostPath(context.Background(), h, false); err == nil {
		t.Fatal("expected an error for a host path with no proposed job name")
	}
}
