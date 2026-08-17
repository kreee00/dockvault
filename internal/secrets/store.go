// Package secrets abstracts where a job's credentials (and webhook URL)
// actually live on disk/OS, beyond the plain .env file every platform
// used until this package existed. Today it has exactly one real
// implementation: Windows Credential Manager (store_windows.go) - see
// internal/workspace/env_windows.go for how it's wired in. Linux/macOS
// keep using the existing .env-file mechanism directly (env_unix.go),
// which never touches this package at all.
package secrets

// Store saves and loads one job's full credential set (including its
// webhook URL, if any - same bundling as today's .env file) as a single
// named entry. jobName is used as-is as (part of) the entry's identifier;
// callers don't need to sanitize it beyond what workspace.SaveJob already
// requires of a job name.
type Store interface {
	// Save writes values for jobName, overwriting any existing entry.
	Save(jobName string, values map[string]string) error
	// Load reads values back for jobName. found is false (with a nil
	// error) when no entry exists yet - e.g. a job with no credentials
	// and no webhook URL, which never gets an entry written at all.
	Load(jobName string) (values map[string]string, found bool, err error)
}
