package restore

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"dockvault/internal/backup"
)

// downloadAndRead downloads file into a throwaway temp dir, verifies it
// against its integrity manifest (see verifyManifestBytes - a hard
// mismatch aborts here, before any decompression or restore proceeds),
// reads it back into memory (gunzipping first if gunzip is true), and
// cleans the temp dir up before returning - used by restore types that
// feed the dump straight into a client's stdin rather than mounting it
// into a container (postgres psql, mysql, mongorestore).
func downloadAndRead(ctx context.Context, rc backup.RcloneClient, file BackupFile, gunzip bool) (data []byte, warning string, err error) {
	tmpDir, err := os.MkdirTemp("", "dockvault-restore-")
	if err != nil {
		return nil, "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := rc.Download(ctx, file.Path, tmpDir); err != nil {
		return nil, "", fmt.Errorf("downloading %s: %w", file.Path, err)
	}

	data, err = os.ReadFile(filepath.Join(tmpDir, file.Name))
	if err != nil {
		return nil, "", fmt.Errorf("reading downloaded file: %w", err)
	}

	// Verify against the exact bytes as uploaded, before any
	// decompression - a manifest's SHA256 was computed over the original
	// archive, not a derived form of it.
	warning, verr := verifyManifestBytes(ctx, rc, file, tmpDir, data)
	if verr != nil {
		return nil, "", verr
	}

	if !gunzip {
		return data, warning, nil
	}

	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("opening gzip stream: %w", err)
	}
	defer gr.Close()
	out, err := io.ReadAll(gr)
	if err != nil {
		return nil, "", fmt.Errorf("decompressing: %w", err)
	}
	return out, warning, nil
}
