package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dockvault/internal/backup"
	"dockvault/internal/backup/standard"
)

func TestStandardBackup_Success(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{}
	executor := standard.New(mockDocker, mockRclone)

	job := &backup.Job{
		Name:            "postgres_data_hostdir",
		VolumeID:        "postgres_data",
		GoogleDrivePath: "/backups/myhost/hostdir/postgres_data_hostdir/",
		RetentionDays:   30,
		MinFilesToKeep:  30,
	}

	if err := executor.Backup(context.Background(), job); err != nil {
		t.Fatalf("Backup returned error: %v", err)
	}
	if !mockRclone.uploadCalled {
		t.Error("expected rclone.Upload to be called")
	}
	if !mockRclone.pruneCalled {
		t.Error("expected rclone.Prune to be called (RetentionDays > 0)")
	}
	if mockRclone.gotRemote != job.GoogleDrivePath {
		t.Errorf("Upload remotePath = %q, want %q", mockRclone.gotRemote, job.GoogleDrivePath)
	}
	if !strings.Contains(mockDocker.gotImage, "alpine") {
		t.Errorf("Run image = %q, want an alpine image", mockDocker.gotImage)
	}
	if job.LastBackup == nil {
		t.Error("expected job.LastBackup to be set")
	}
	if !strings.HasPrefix(job.LastBackupStatus, "success") {
		t.Errorf("job.LastBackupStatus = %q, want a success message", job.LastBackupStatus)
	}
}

func TestStandardBackup_NoVolumeID(t *testing.T) {
	executor := standard.New(&mockDockerClient{}, &mockRcloneClient{})
	job := &backup.Job{Name: "broken_job"}

	if err := executor.Backup(context.Background(), job); err == nil {
		t.Fatal("expected an error for a job with no VolumeID")
	}
}

func TestStandardBackup_UploadFailure(t *testing.T) {
	mockDocker := &mockDockerClient{}
	mockRclone := &mockRcloneClient{uploadErr: fmt.Errorf("upload failed")}
	executor := standard.New(mockDocker, mockRclone)

	job := &backup.Job{Name: "job", VolumeID: "vol", GoogleDrivePath: "/backups/host/generic/job/"}

	if err := executor.Backup(context.Background(), job); err == nil {
		t.Fatal("expected upload failure to propagate")
	}
	if !strings.HasPrefix(job.LastBackupStatus, "failed:") {
		t.Errorf("job.LastBackupStatus = %q, want a failed: prefix", job.LastBackupStatus)
	}
}
