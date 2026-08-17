package tests

import (
	"testing"

	"dockvault/internal/detector"
)

func TestDetectServiceType(t *testing.T) {
	cases := []struct {
		image string
		want  string
	}{
		{"postgres:15.2", detector.ServicePostgres},
		{"POSTGRES:latest", detector.ServicePostgres}, // case-insensitive
		{"mysql:8.0", detector.ServiceMySQL},
		{"mongo:6", detector.ServiceMongoDB},
		{"redis:7.0-alpine", detector.ServiceRedis},
		{"n8nio/n8n:latest", detector.ServiceN8N},
		{"rabbitmq:3-management", detector.ServiceRabbitmq},
		{"rabbitmq:3-management-alpine", detector.ServiceRabbitmq},
		{"nginx:stable", detector.ServiceGeneric},
		{"myregistry.internal/custom-app:1.0", detector.ServiceGeneric},
	}
	for _, c := range cases {
		if got := detector.DetectServiceType(c.image); got != c.want {
			t.Errorf("DetectServiceType(%q) = %q, want %q", c.image, got, c.want)
		}
	}
}

func TestDetectServiceTypeFromPort(t *testing.T) {
	cases := []struct {
		port int
		want string
	}{
		{5432, detector.ServicePostgres},
		{3306, detector.ServiceMySQL},
		{27017, detector.ServiceMongoDB},
		{6379, detector.ServiceRedis},
		{9999, ""},
	}
	for _, c := range cases {
		if got := detector.DetectServiceTypeFromPort(c.port); got != c.want {
			t.Errorf("DetectServiceTypeFromPort(%d) = %q, want %q", c.port, got, c.want)
		}
	}
}

func TestRecommendedBackupType(t *testing.T) {
	cases := []struct {
		service string
		want    string
	}{
		{detector.ServicePostgres, detector.BackupPgDump},
		{detector.ServiceMySQL, detector.BackupMysqldump},
		{detector.ServiceMongoDB, detector.BackupMongodump},
		{detector.ServiceRedis, detector.BackupBGSave},
		{detector.ServiceN8N, detector.BackupN8NLife},
		{detector.ServiceRabbitmq, detector.BackupRabbitmqDefinitions},
		{detector.ServiceGeneric, detector.BackupTar},
		{detector.ServiceHostDir, detector.BackupTar},
	}
	for _, c := range cases {
		if got := detector.RecommendedBackupType(c.service); got != c.want {
			t.Errorf("RecommendedBackupType(%q) = %q, want %q", c.service, got, c.want)
		}
	}
}

func TestCredentialEnvKeysFor(t *testing.T) {
	cases := []struct {
		service string
		want    []string
	}{
		{detector.ServicePostgres, []string{"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"}},
		{detector.ServiceMySQL, []string{"MYSQL_ROOT_PASSWORD", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_DATABASE"}},
		{detector.ServiceMongoDB, []string{"MONGO_INITDB_ROOT_USERNAME", "MONGO_INITDB_ROOT_PASSWORD", "MONGO_INITDB_DATABASE"}},
		{detector.ServiceRedis, []string{"REDIS_PASSWORD"}},
		{detector.ServiceN8N, nil},
		{detector.ServiceRabbitmq, nil},
		{detector.ServiceGeneric, nil},
	}
	for _, c := range cases {
		got := detector.CredentialEnvKeysFor(c.service)
		if len(got) != len(c.want) {
			t.Errorf("CredentialEnvKeysFor(%q) = %v, want %v", c.service, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("CredentialEnvKeysFor(%q) = %v, want %v", c.service, got, c.want)
				break
			}
		}
	}
}

func TestGenerateJobName(t *testing.T) {
	d := detector.New(nil, "stage") // no docker calls made by GenerateJobName

	cases := []struct {
		name        string
		volume      string
		serviceType string
		creds       map[string]string
		want        string
	}{
		{
			name:        "postgres with db name",
			volume:      "myapp_db",
			serviceType: detector.ServicePostgres,
			creds:       map[string]string{"POSTGRES_DB": "myapp_db", "POSTGRES_USER": "myapp"},
			want:        "postgres_myapp_db",
		},
		{
			name:        "mysql with db name",
			volume:      "app_data",
			serviceType: detector.ServiceMySQL,
			creds:       map[string]string{"MYSQL_DATABASE": "shop"},
			want:        "mysql_shop",
		},
		{
			name:        "postgres user only, no db",
			volume:      "pg_data",
			serviceType: detector.ServicePostgres,
			creds:       map[string]string{"POSTGRES_USER": "admin"},
			want:        "postgres_user_backup",
		},
		{
			name:        "redis with no creds, cache-named volume",
			volume:      "redis_cache",
			serviceType: detector.ServiceRedis,
			creds:       map[string]string{},
			want:        "redis_cache",
		},
		{
			name:        "redis with generic _data volume falls back",
			volume:      "redis_data",
			serviceType: detector.ServiceRedis,
			creds:       map[string]string{},
			want:        "redis_backup",
		},
		{
			name:        "generic volume falls back to service_backup",
			volume:      "some_volume",
			serviceType: detector.ServiceGeneric,
			creds:       map[string]string{},
			want:        "generic_backup",
		},
		{
			// Regression test: an anonymous volume's 64-hex-char name used
			// to pass straight through into the job name (e.g.
			// "redis_a100a7135f7dd02fa1b9df4dd76e0c3802af5d1e7fded5376ec6bd6f1c8b45e2"),
			// discovered by running a real anonymous-volume redis container
			// through `dockvault scan --auto`. It must be truncated the
			// same way ArchiveFileName's volume_identifier is.
			name:        "anonymous volume truncates hash instead of embedding it whole",
			volume:      "a100a7135f7dd02fa1b9df4dd76e0c3802af5d1e7fded5376ec6bd6f1c8b45e2",
			serviceType: detector.ServiceRedis,
			creds:       map[string]string{},
			want:        "redis_vol_a100a713",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := d.GenerateJobName(c.volume, c.serviceType, c.creds)
			if got != c.want {
				t.Errorf("GenerateJobName(%q, %q, %v) = %q, want %q", c.volume, c.serviceType, c.creds, got, c.want)
			}
		})
	}
}

func TestExtractCredentials(t *testing.T) {
	d := detector.New(nil, "stage")

	ci := detector.ContainerInfo{
		Image: "postgres:15",
		Env: map[string]string{
			"POSTGRES_USER":     "admin",
			"POSTGRES_PASSWORD": "hunter2",
			"POSTGRES_DB":       "myapp",
			"PATH":              "/usr/local/bin",
			"CUSTOM_API_KEY":    "abc123", // generic secret marker, not postgres-specific
		},
	}

	creds := d.ExtractCredentials(ci)

	want := map[string]string{
		"POSTGRES_USER":     "admin",
		"POSTGRES_PASSWORD": "hunter2",
		"POSTGRES_DB":       "myapp",
		"CUSTOM_API_KEY":    "abc123",
	}
	if len(creds) != len(want) {
		t.Fatalf("ExtractCredentials returned %d keys, want %d: got %v", len(creds), len(want), creds)
	}
	for k, v := range want {
		if creds[k] != v {
			t.Errorf("ExtractCredentials()[%q] = %q, want %q", k, creds[k], v)
		}
	}
	if _, ok := creds["PATH"]; ok {
		t.Errorf("ExtractCredentials should not have captured PATH")
	}
}
