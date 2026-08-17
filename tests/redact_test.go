package tests

import (
	"strings"
	"testing"

	"dockvault/internal/docker"
)

// TestRedactArgs locks in the fix for a security finding: docker.Client's
// error messages used to embed the full command line verbatim, including
// -e PGPASSWORD=... / MYSQL_PWD=... / REDISCLI_AUTH=... and mongodump's
// --password <value>, which then flowed into logs, manifest.json, and
// webhook payloads on any failure.
func TestRedactArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantAbsent  string // a secret value that must not appear anywhere in the output
		wantPresent string // something that should still be visible (command stays debuggable)
	}{
		{
			name:        "docker exec -e PGPASSWORD",
			args:        []string{"exec", "-i", "-e", "PGPASSWORD=hunter2", "test-postgres", "pg_dump", "-U", "appuser", "appdb"},
			wantAbsent:  "hunter2",
			wantPresent: "PGPASSWORD=***",
		},
		{
			name:        "docker exec -e MYSQL_PWD",
			args:        []string{"exec", "-e", "MYSQL_PWD=s3cret", "test-mysql", "mysqldump", "-u", "root", "shop"},
			wantAbsent:  "s3cret",
			wantPresent: "MYSQL_PWD=***",
		},
		{
			name:        "mongodump --password on argv",
			args:        []string{"exec", "test-mongo", "mongodump", "--archive", "--gzip", "--username", "root", "--password", "topsecret", "--authenticationDatabase", "admin"},
			wantAbsent:  "topsecret",
			wantPresent: "--username root",
		},
		{
			name:        "docker run has no credentials to redact",
			args:        []string{"run", "--rm", "-v", "pgdata:/source:ro", "alpine", "tar", "czf", "/backup/x.tar.gz"},
			wantAbsent:  "",
			wantPresent: "pgdata:/source:ro",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(docker.RedactArgs(c.args), " ")
			if c.wantAbsent != "" && strings.Contains(got, c.wantAbsent) {
				t.Errorf("RedactArgs(%v) = %q, still contains the secret %q", c.args, got, c.wantAbsent)
			}
			if !strings.Contains(got, c.wantPresent) {
				t.Errorf("RedactArgs(%v) = %q, want it to still contain %q", c.args, got, c.wantPresent)
			}
			// The original args slice must be untouched - RedactArgs is
			// only for formatting error messages, never for the real
			// command that actually gets executed.
			if c.name == "docker exec -e PGPASSWORD" && c.args[3] != "PGPASSWORD=hunter2" {
				t.Error("RedactArgs must not mutate the input slice")
			}
		})
	}
}
