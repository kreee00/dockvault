package tests

import (
	"testing"

	"dockvault/internal/rclone"
)

func TestParseBackendDrives_RealSample(t *testing.T) {
	// Captured live from `rclone backend drives dockvault_gdrive_stage:`
	// against the actual configured service account/Shared Drive
	// (2026-08-17), not hand-assumed.
	sample := []byte(`[
	{
		"id": "0AAIOG4rPAqiYUk9PVA",
		"kind": "drive#drive",
		"name": "Company-DockVault-Backups-STAGE"
	}
]`)

	drives, err := rclone.ParseBackendDrives(sample)
	if err != nil {
		t.Fatalf("ParseBackendDrives returned error: %v", err)
	}
	if len(drives) != 1 {
		t.Fatalf("got %d drives, want 1", len(drives))
	}
	if drives[0].ID != "0AAIOG4rPAqiYUk9PVA" || drives[0].Name != "Company-DockVault-Backups-STAGE" {
		t.Errorf("drives[0] = %+v, unexpected", drives[0])
	}
}

func TestParseBackendDrives_MultipleAndEmpty(t *testing.T) {
	drives, err := rclone.ParseBackendDrives([]byte(`[]`))
	if err != nil {
		t.Fatalf("ParseBackendDrives(empty) returned error: %v", err)
	}
	if len(drives) != 0 {
		t.Errorf("got %d drives for an empty array, want 0", len(drives))
	}

	drives, err = rclone.ParseBackendDrives([]byte(`[{"id":"a","kind":"drive#drive","name":"A"},{"id":"b","kind":"drive#drive","name":"B"}]`))
	if err != nil {
		t.Fatalf("ParseBackendDrives returned error: %v", err)
	}
	if len(drives) != 2 || drives[0].ID != "a" || drives[1].ID != "b" {
		t.Errorf("drives = %+v, unexpected", drives)
	}
}

func TestParseBackendDrives_MalformedJSON(t *testing.T) {
	if _, err := rclone.ParseBackendDrives([]byte(`not json`)); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}
