//go:build windows

package workspace

import "dockvault/internal/secrets"

// credStore is the single Windows Credential Manager Store instance every
// Workspace on this platform saves/loads job credentials through - see
// internal/secrets/store_windows.go for the real implementation and its
// documented limitations (per-user/DPAPI-scoped, unverified at runtime on
// this project since it was built from a Linux-only environment).
var credStore = secrets.New()

// saveEnv is saveEnv's Windows counterpart: rather than writing a
// plaintext .env file (env_unix.go's approach, which stays the mechanism
// on Linux/macOS - see this project's design decision to keep both, not
// dual-write), job credentials + webhook URL go to Windows Credential
// Manager only. No .env file is created for a job with credentials on
// this platform - writing the real secret to both a Credential Manager
// entry and a plaintext file alongside it would defeat the point of using
// Credential Manager at all.
func (w *Workspace) saveEnv(jobName string, env map[string]string) error {
	return credStore.Save(jobName, env)
}

// loadEnv is loadEnv's Windows counterpart. found=false (no entry ever
// saved - e.g. a job with no credentials and no webhook URL) returns an
// empty map with no error, same as env_unix.go's "file doesn't exist"
// case. A real read failure is returned as-is - see
// windowsCredentialManagerStore.Load's doc comment for why this is
// deliberately not silently swallowed into an empty-credentials result.
func (w *Workspace) loadEnv(jobName string) (map[string]string, error) {
	values, found, err := credStore.Load(jobName)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]string{}, nil
	}
	return values, nil
}
