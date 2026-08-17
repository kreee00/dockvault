package tests

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dockvault/internal/backup"
	"dockvault/internal/backup/rabbitmq"
)

func TestRabbitmqBackup_ExportsDefinitionsAndUploads(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{}
	executor := rabbitmq.New(mockDocker, mockRclone)

	job := &backup.Job{
		Name:            "rabbitmq_broker",
		ContainerName:   "test-rabbitmq",
		GoogleDrivePath: "/stage/host/rabbitmq/rabbitmq_broker/",
	}

	if err := executor.Backup(context.Background(), job); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}

	// One Exec for export_definitions, one for best-effort cleanup.
	if len(mockDocker.execCalls) != 2 {
		t.Fatalf("expected exactly 2 Exec calls (export + cleanup), got %d: %v", len(mockDocker.execCalls), mockDocker.execCalls)
	}
	exportCall := mockDocker.execCalls[0]
	if exportCall.Container != "test-rabbitmq" {
		t.Errorf("Exec container = %q, want %q", exportCall.Container, "test-rabbitmq")
	}
	if exportCall.Cmd[0] != "rabbitmqctl" || exportCall.Cmd[1] != "export_definitions" {
		t.Errorf("Exec cmd = %v, want rabbitmqctl export_definitions ...", exportCall.Cmd)
	}
	cleanupCall := mockDocker.execCalls[1]
	if cleanupCall.Cmd[0] != "rm" {
		t.Errorf("cleanup Exec cmd = %v, want an rm of the scratch file", cleanupCall.Cmd)
	}

	if len(mockDocker.cpCalls) != 1 {
		t.Fatalf("expected exactly one docker cp call, got %d", len(mockDocker.cpCalls))
	}
	if !strings.Contains(mockDocker.cpCalls[0].Src, "test-rabbitmq:") {
		t.Errorf("cp src = %q, want it to copy out of the container", mockDocker.cpCalls[0].Src)
	}

	if !mockRclone.uploadCalled {
		t.Fatal("expected rclone.Upload to be called")
	}
	if len(mockRclone.uploadCalls) < 1 || !strings.HasSuffix(mockRclone.uploadCalls[0].Local, ".json.gz") {
		t.Errorf("first uploaded file = %v, want a .json.gz suffix", mockRclone.uploadCalls)
	}
}

func TestRabbitmqBackup_NoContainer(t *testing.T) {
	executor := rabbitmq.New(&mockDockerClient{}, &mockRcloneClient{})
	job := &backup.Job{Name: "broken"}

	if err := executor.Backup(context.Background(), job); err == nil {
		t.Fatal("expected an error for a job with no ContainerName")
	}
}

func TestRabbitmqBackup_CleansUpEvenWhenExportFails(t *testing.T) {
	mockDocker := &mockDockerClient{execErr: errors.New("rabbitmqctl failed")}
	mockRclone := &mockRcloneClient{}
	executor := rabbitmq.New(mockDocker, mockRclone)

	job := &backup.Job{Name: "rabbitmq_broker", ContainerName: "test-rabbitmq", GoogleDrivePath: "/stage/host/rabbitmq/rabbitmq_broker/"}

	if err := executor.Backup(context.Background(), job); err == nil {
		t.Fatal("expected the export_definitions failure to propagate")
	}
	// Cleanup is deferred before the export attempt (rm -f is a no-op on
	// a path that was never created, so it's safe to always attempt) -
	// both the failed export and the cleanup attempt should be recorded.
	if len(mockDocker.execCalls) != 2 {
		t.Errorf("execCalls = %v, want 2 (export attempt + cleanup attempt, even though export failed)", mockDocker.execCalls)
	}
	if mockRclone.uploadCalled {
		t.Error("expected no upload after export_definitions failed")
	}
}
