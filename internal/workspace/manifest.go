package workspace

import (
	"encoding/json"
	"os"
	"time"
)

// ManifestEntry is one job's recorded history in manifest.json. This is
// explicitly optional per the spec ("for future enhancements") - jobs work
// fully without it; it exists to support `dockvault list --detailed` and
// similar status reporting without re-running every job's detection logic.
type ManifestEntry struct {
	JobName          string     `json:"job_name"`
	ServiceType      string     `json:"service_type"`
	LastBackup       *time.Time `json:"last_backup,omitempty"`
	LastBackupStatus string     `json:"last_backup_status,omitempty"`
	LastSizeBytes    int64      `json:"last_size_bytes,omitempty"`
}

// Manifest is the top-level manifest.json document.
type Manifest struct {
	Jobs []ManifestEntry `json:"jobs"`
}

// LoadManifest reads manifest.json, returning an empty Manifest (no error)
// if it doesn't exist yet.
func (w *Workspace) LoadManifest() (Manifest, error) {
	data, err := os.ReadFile(w.ManifestPath())
	if os.IsNotExist(err) {
		return Manifest{}, nil
	}
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// SaveManifest writes m to manifest.json, pretty-printed.
func (w *Workspace) SaveManifest(m Manifest) error {
	if err := w.EnsureLayout(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	// Mode 0600: LastBackupStatus can carry failure detail, and this file
	// otherwise lists every job's name/service type - workspace root is
	// already 0700, but keep the file itself consistent rather than
	// relying solely on the enclosing directory.
	return os.WriteFile(w.ManifestPath(), data, 0o600)
}

// Upsert adds or replaces the entry for jobName.
func (m *Manifest) Upsert(entry ManifestEntry) {
	for i, e := range m.Jobs {
		if e.JobName == entry.JobName {
			m.Jobs[i] = entry
			return
		}
	}
	m.Jobs = append(m.Jobs, entry)
}
