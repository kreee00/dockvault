package tests

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"

	"dockvault/internal/backup"
	"dockvault/internal/detector"
	"dockvault/internal/generator"
	"dockvault/internal/workspace"
)

func newTestWizard(t *testing.T, dc *mockDockerClient) *generator.Wizard {
	t.Helper()
	ws := &workspace.Workspace{Root: t.TempDir()}
	return &generator.Wizard{Workspace: ws, Generator: generator.New(ws), Docker: dc}
}

func TestPromptMissingCredentials_FillsOnlyMissingKeys(t *testing.T) {
	mockDocker := &mockDockerClient{} // Exec succeeds by default (nil execErr)
	wiz := newTestWizard(t, mockDocker)

	job := &backup.Job{
		Name:          "postgres_appdb",
		ServiceType:   detector.ServicePostgres,
		ContainerName: "test-postgres",
		// POSTGRES_USER/POSTGRES_DB already auto-detected; only PASSWORD
		// is missing (e.g. mounted from a Docker secret file, never an
		// env var).
		Credentials: map[string]string{"POSTGRES_USER": "appuser", "POSTGRES_DB": "appdb"},
	}
	in := bufio.NewReader(strings.NewReader("hunter2\n"))
	var out strings.Builder

	if err := wiz.PromptMissingCredentials(context.Background(), &out, in, job); err != nil {
		t.Fatalf("PromptMissingCredentials returned error: %v", err)
	}

	if job.Credentials["POSTGRES_USER"] != "appuser" || job.Credentials["POSTGRES_DB"] != "appdb" {
		t.Errorf("already-present credentials were altered: %+v", job.Credentials)
	}
	if job.Credentials["POSTGRES_PASSWORD"] != "hunter2" {
		t.Errorf("POSTGRES_PASSWORD = %q, want it filled from the prompt", job.Credentials["POSTGRES_PASSWORD"])
	}
	if !strings.Contains(out.String(), "POSTGRES_PASSWORD") {
		t.Errorf("output = %q, want it to mention the missing key it's prompting for", out.String())
	}

	// Exactly one Exec call: the psql connectivity probe, using the
	// now-filled password. (Not pg_isready - see connectivity.go's doc
	// comment for why that doesn't actually check anything useful.)
	if len(mockDocker.execCalls) != 1 {
		t.Fatalf("expected exactly one Exec call (the connectivity probe), got %d: %v", len(mockDocker.execCalls), mockDocker.execCalls)
	}
	call := mockDocker.execCalls[0]
	if call.Cmd[0] != "psql" {
		t.Errorf("Exec cmd = %v, want psql ...", call.Cmd)
	}
	if call.Env["PGPASSWORD"] != "hunter2" {
		t.Errorf("Exec env PGPASSWORD = %q, want the freshly-collected password", call.Env["PGPASSWORD"])
	}
}

func TestPromptMissingCredentials_NoOpWhenNothingMissing(t *testing.T) {
	mockDocker := &mockDockerClient{}
	wiz := newTestWizard(t, mockDocker)

	job := &backup.Job{
		Name:          "redis_cache",
		ServiceType:   detector.ServiceRedis,
		ContainerName: "test-redis",
		Credentials:   map[string]string{"REDIS_PASSWORD": "already-set"},
	}
	in := bufio.NewReader(strings.NewReader(""))
	var out strings.Builder

	if err := wiz.PromptMissingCredentials(context.Background(), &out, in, job); err != nil {
		t.Fatalf("PromptMissingCredentials returned error: %v", err)
	}
	if out.String() != "" {
		t.Errorf("output = %q, want no prompt when nothing is missing", out.String())
	}
	if len(mockDocker.execCalls) != 0 {
		t.Errorf("execCalls = %v, want none - no probe should run when there was nothing to prompt for", mockDocker.execCalls)
	}
}

func TestPromptMissingCredentials_NoOpForServiceTypeWithNoCuratedKeys(t *testing.T) {
	mockDocker := &mockDockerClient{}
	wiz := newTestWizard(t, mockDocker)

	job := &backup.Job{Name: "rabbitmq_broker", ServiceType: detector.ServiceRabbitmq, ContainerName: "test-rabbitmq"}
	in := bufio.NewReader(strings.NewReader(""))
	var out strings.Builder

	if err := wiz.PromptMissingCredentials(context.Background(), &out, in, job); err != nil {
		t.Fatalf("PromptMissingCredentials returned error: %v", err)
	}
	if out.String() != "" {
		t.Errorf("output = %q, want no prompt - rabbitmq has no curated credential keys", out.String())
	}
}

func TestPromptMissingCredentials_FailedProbeWarnsButDoesNotError(t *testing.T) {
	mockDocker := &mockDockerClient{execErr: errors.New("connection refused")}
	wiz := newTestWizard(t, mockDocker)

	job := &backup.Job{
		Name:          "redis_cache",
		ServiceType:   detector.ServiceRedis,
		ContainerName: "test-redis",
	}
	in := bufio.NewReader(strings.NewReader("wrongpass\n"))
	var out strings.Builder

	// A failed connectivity probe must not fail the whole prompt step -
	// the job should still be usable/savable afterward.
	if err := wiz.PromptMissingCredentials(context.Background(), &out, in, job); err != nil {
		t.Fatalf("PromptMissingCredentials returned error on a failed probe (should warn, not fail): %v", err)
	}
	if job.Credentials["REDIS_PASSWORD"] != "wrongpass" {
		t.Errorf("REDIS_PASSWORD = %q, want the value entered even though the probe failed", job.Credentials["REDIS_PASSWORD"])
	}
	if !strings.Contains(out.String(), "failed") {
		t.Errorf("output = %q, want it to report the connectivity failure", out.String())
	}
}

// TestPromptMissingCredentials_AuthFailureWithZeroExitIsStillDetected locks
// in a real bug found via live-container testing: `redis-cli`/`mysqladmin
// ping` both exit 0 even when the credentials are wrong, printing an
// AUTH/Access-denied error to stdout instead of failing the process - see
// connectivity.go's doc comment. err == nil is not sufficient to call
// this a success; the output has to actually look like success too.
func TestPromptMissingCredentials_AuthFailureWithZeroExitIsStillDetected(t *testing.T) {
	// execErr is nil (the mock's zero value) - this is exactly what a
	// real `docker exec ... redis-cli -a wrongpass ping` returns: no Go
	// error, just wrong-looking output.
	mockDocker := &mockDockerClient{execOutput: []byte("AUTH failed: WRONGPASS invalid username-password pair\nNOAUTH Authentication required.\n")}
	wiz := newTestWizard(t, mockDocker)

	job := &backup.Job{
		Name:          "redis_cache",
		ServiceType:   detector.ServiceRedis,
		ContainerName: "test-redis",
	}
	in := bufio.NewReader(strings.NewReader("wrongpass\n"))
	var out strings.Builder

	if err := wiz.PromptMissingCredentials(context.Background(), &out, in, job); err != nil {
		t.Fatalf("PromptMissingCredentials returned error: %v", err)
	}
	if !strings.Contains(out.String(), "failed") {
		t.Errorf("output = %q, want the connectivity check reported as failed despite the probe process exiting 0", out.String())
	}
	if strings.Contains(out.String(), "reached redis") {
		t.Errorf("output = %q, want it to NOT report success just because err was nil", out.String())
	}
}

func TestPromptMissingCredentials_BlankInputSkipsKey(t *testing.T) {
	mockDocker := &mockDockerClient{execOutput: []byte("PONG\n")}
	wiz := newTestWizard(t, mockDocker)

	job := &backup.Job{
		Name:          "redis_cache",
		ServiceType:   detector.ServiceRedis,
		ContainerName: "test-redis",
	}
	in := bufio.NewReader(strings.NewReader("\n")) // blank line: skip
	var out strings.Builder

	if err := wiz.PromptMissingCredentials(context.Background(), &out, in, job); err != nil {
		t.Fatalf("PromptMissingCredentials returned error: %v", err)
	}
	if _, ok := job.Credentials["REDIS_PASSWORD"]; ok {
		t.Errorf("REDIS_PASSWORD = %q, want it left unset when the user skips with a blank line", job.Credentials["REDIS_PASSWORD"])
	}
	// redis-cli without -a, since no password was ever collected.
	if len(mockDocker.execCalls) != 1 || mockDocker.execCalls[0].Cmd[0] != "redis-cli" {
		t.Fatalf("execCalls = %v, want exactly one redis-cli probe", mockDocker.execCalls)
	}
	for _, arg := range mockDocker.execCalls[0].Cmd {
		if arg == "-a" {
			t.Errorf("Exec cmd = %v, want no -a flag when no password was collected", mockDocker.execCalls[0].Cmd)
		}
	}
}
