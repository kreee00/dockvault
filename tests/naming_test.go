package tests

import (
	"regexp"
	"strings"
	"testing"

	"dockvault/internal/backup"
	"dockvault/internal/detector"
)

func TestRemotePath(t *testing.T) {
	cases := []struct {
		name        string
		environment string
		hostname    string
		serviceType string
		jobName     string
		want        string
	}{
		{
			name:        "spec example",
			environment: "stage",
			hostname:    "scada-db",
			serviceType: "redis",
			jobName:     "n8n-redis-1",
			want:        "/stage/scada-db/redis/n8n-redis-1/",
		},
		{
			name:        "lowercases every component regardless of input casing",
			environment: "PROD",
			hostname:    "SCADA-DB",
			serviceType: "Postgres",
			jobName:     "Postgres_AppDB",
			want:        "/prod/scada-db/postgres/postgres_appdb/",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detector.RemotePath(c.environment, c.hostname, c.serviceType, c.jobName); got != c.want {
				t.Errorf("RemotePath(%q, %q, %q, %q) = %q, want %q",
					c.environment, c.hostname, c.serviceType, c.jobName, got, c.want)
			}
		})
	}
}

func TestNormalizeEnvironment(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "stage"},
		{"   ", "stage"},
		{"stage", "stage"},
		{"PROD", "prod"},
		{"  Prod  ", "prod"},
	}
	for _, c := range cases {
		if got := detector.NormalizeEnvironment(c.in); got != c.want {
			t.Errorf("NormalizeEnvironment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestVolumeIdentifier(t *testing.T) {
	// A real Docker anonymous-volume name: 64 lowercase hex characters.
	anon := "1b3f2395" + strings.Repeat("a", 56)
	if len(anon) != 64 {
		t.Fatalf("test setup bug: anon volume name is %d chars, want 64", len(anon))
	}

	cases := []struct {
		name       string
		volumeName string
		want       string
	}{
		{"named volume used as-is", "pgdata", "pgdata"},
		{"named volume with underscores", "n8n_data", "n8n_data"},
		{"anonymous volume truncated to vol-<hash8>", anon, "vol-1b3f2395"},
		{"63 hex chars is not anonymous (one short)", anon[:63], anon[:63]},
		{"64 chars but not all hex is not anonymous", strings.Repeat("g", 64), strings.Repeat("g", 64)},
		{"64 uppercase hex chars is not anonymous (docker names are lowercase)", strings.ToUpper(anon), strings.ToUpper(anon)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detector.VolumeIdentifier(c.volumeName); got != c.want {
				t.Errorf("VolumeIdentifier(%q) = %q, want %q", c.volumeName, got, c.want)
			}
		})
	}
}

func TestBindMountIdentifier(t *testing.T) {
	cases := []struct {
		target string
		want   string
	}{
		{"/usr/share/nginx/html", "bind-html"},
		{"/etc/nginx", "bind-nginx"},
		{"/etc/letsencrypt/", "bind-letsencrypt"}, // trailing slash shouldn't change the result
		{"/", "bind-root"},
	}
	for _, c := range cases {
		if got := detector.BindMountIdentifier(c.target); got != c.want {
			t.Errorf("BindMountIdentifier(%q) = %q, want %q", c.target, got, c.want)
		}
	}
}

// TestArchiveFileName checks the <job_name>_<volume_identifier>_<timestamp>.<ext>
// shape and each of the three example cases from the naming convention.
// The timestamp is real wall-clock time, so it's matched by pattern rather
// than an exact value.
func TestArchiveFileName(t *testing.T) {
	pattern := regexp.MustCompile(`^[^_]+(?:_[^_]+)*_\d{8}-\d{6}Z\.[a-z0-9.]+$`)

	cases := []struct {
		name             string
		jobName          string
		volumeIdentifier string
		ext              string
	}{
		{"named volume", "n8n-app", "n8n_data", "tar.gz"},
		{"anonymous volume", "n8n-redis-1", detector.VolumeIdentifier("1b3f2395" + strings.Repeat("a", 56)), "tar.gz"},
		{"bind mount", "nginx", detector.BindMountIdentifier("/usr/share/nginx/html"), "tar.gz"},
		{"sql dump extension", "postgres_appdb", "appdb_data", "sql.gz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := backup.ArchiveFileName(c.jobName, c.volumeIdentifier, c.ext)
			if !pattern.MatchString(got) {
				t.Errorf("ArchiveFileName(%q, %q, %q) = %q, does not match expected shape", c.jobName, c.volumeIdentifier, c.ext, got)
			}
			wantPrefix := c.jobName + "_" + c.volumeIdentifier + "_"
			if !strings.HasPrefix(got, wantPrefix) {
				t.Errorf("ArchiveFileName(...) = %q, want prefix %q", got, wantPrefix)
			}
			wantSuffix := "." + c.ext
			if !strings.HasSuffix(got, wantSuffix) {
				t.Errorf("ArchiveFileName(...) = %q, want suffix %q", got, wantSuffix)
			}
		})
	}
}
