// Package setup drives `dockvault setup`: a guided, interactive one-time
// environment bootstrap - validating a GCP service account JSON, wiring
// it into an rclone remote pointed at a Google Shared Drive, confirming
// real access, and walking through config.json's defaults. It does not
// create backup jobs - see internal/generator for that (generate
// --interactive).
package setup

import (
	"encoding/json"
	"fmt"
	"os"
)

// serviceAccountKey is the subset of a GCP service account JSON key file
// dockvault checks - confirmed against a real downloaded key (2026-08-17):
// full field set is type/project_id/private_key_id/private_key/
// client_email/client_id/auth_uri/token_uri/auth_provider_x509_cert_url/
// client_x509_cert_url/universe_domain; only the fields that actually
// matter for "is this a usable service account key" are unmarshaled here.
type serviceAccountKey struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
}

// ValidateServiceAccountJSON reads path and confirms it's structurally a
// usable GCP service account key - valid JSON, type=="service_account",
// and non-empty project_id/private_key/client_email. It does NOT contact
// GCP or confirm the key is actually still valid/authorized - that's what
// rclone.Client.ListSharedDrives is for, once the key is wired into a
// remote. Returns the project ID and service account email so the wizard
// can show the user what it found before asking them to confirm.
func ValidateServiceAccountJSON(path string) (projectID, clientEmail string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", path, err)
	}

	var key serviceAccountKey
	if err := json.Unmarshal(data, &key); err != nil {
		return "", "", fmt.Errorf("%s doesn't look like valid JSON: %w", path, err)
	}
	if key.Type != "service_account" {
		return "", "", fmt.Errorf(`%s has "type": %q, want "service_account" - is this the right file? (GCP also issues OAuth client JSON, which looks similar but won't work here)`, path, key.Type)
	}
	if key.ProjectID == "" || key.PrivateKey == "" || key.ClientEmail == "" {
		return "", "", fmt.Errorf("%s is missing one of project_id/private_key/client_email - it may be truncated or corrupted", path)
	}
	return key.ProjectID, key.ClientEmail, nil
}
