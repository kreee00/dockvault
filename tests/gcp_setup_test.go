package tests

import (
	"os"
	"path/filepath"
	"testing"

	"dockvault/internal/setup"
)

func writeTempJSON(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sa.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

func TestValidateServiceAccountJSON_Valid(t *testing.T) {
	path := writeTempJSON(t, `{
		"type": "service_account",
		"project_id": "prj-infra-dockvault-stage",
		"private_key": "-----BEGIN PRIVATE KEY-----\nfake\n-----END PRIVATE KEY-----\n",
		"client_email": "sa-dockvault@prj-infra-dockvault-stage.iam.gserviceaccount.com"
	}`)

	projectID, clientEmail, err := setup.ValidateServiceAccountJSON(path)
	if err != nil {
		t.Fatalf("ValidateServiceAccountJSON returned error: %v", err)
	}
	if projectID != "prj-infra-dockvault-stage" {
		t.Errorf("projectID = %q, want %q", projectID, "prj-infra-dockvault-stage")
	}
	if clientEmail != "sa-dockvault@prj-infra-dockvault-stage.iam.gserviceaccount.com" {
		t.Errorf("clientEmail = %q, unexpected", clientEmail)
	}
}

func TestValidateServiceAccountJSON_MalformedJSON(t *testing.T) {
	path := writeTempJSON(t, `not json at all`)

	if _, _, err := setup.ValidateServiceAccountJSON(path); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestValidateServiceAccountJSON_WrongType(t *testing.T) {
	// GCP also issues OAuth client JSON that superficially resembles a
	// service account key - "type" is the field that actually distinguishes
	// them, so this must be rejected, not silently accepted.
	path := writeTempJSON(t, `{
		"type": "authorized_user",
		"client_id": "some-client-id",
		"client_secret": "some-secret",
		"refresh_token": "some-token"
	}`)

	if _, _, err := setup.ValidateServiceAccountJSON(path); err == nil {
		t.Fatal("expected an error for a non-service-account JSON type")
	}
}

func TestValidateServiceAccountJSON_MissingFields(t *testing.T) {
	path := writeTempJSON(t, `{"type": "service_account", "project_id": "prj-x"}`)

	if _, _, err := setup.ValidateServiceAccountJSON(path); err == nil {
		t.Fatal("expected an error when private_key/client_email are missing")
	}
}

func TestValidateServiceAccountJSON_MissingFile(t *testing.T) {
	if _, _, err := setup.ValidateServiceAccountJSON(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatal("expected an error for a nonexistent file")
	}
}
