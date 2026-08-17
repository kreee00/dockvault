package backup

import (
	"compress/gzip"
	"fmt"
	"os"
	"strings"
	"time"
)

// ArchiveFileName builds the standardized backup archive filename:
//
//	<job_name>_<volume_identifier>_<ISO8601-UTC-TIMESTAMP>.<ext>
//
// e.g. "n8n-app_n8n_data_20260817-021300Z.tar.gz". volumeIdentifier should
// already be the sanitized form internal/detector's VolumeIdentifier/
// BindMountIdentifier produce (named volume name as-is, "vol-<hash8>" for
// an anonymous volume, "bind-<target>" for a bind mount). The timestamp is
// always the current moment in UTC - callers can't pass one in, since it
// must reflect when the backup actually ran.
func ArchiveFileName(jobName, volumeIdentifier, ext string) string {
	ts := time.Now().UTC().Format("20060102-150405") + "Z"
	return fmt.Sprintf("%s_%s_%s.%s", jobName, volumeIdentifier, ts, strings.TrimPrefix(ext, "."))
}

// GzipToFile writes data to path, gzip-compressed, mode 0600 - not 0644,
// since this is typically a full database/definitions dump and may
// contain sensitive rows or hashed credentials. It lives in an
// os.MkdirTemp directory (already 0700, so other local users can't
// traverse to it), but the file itself shouldn't default to 0666&umask
// either, for defense in depth and consistency with .env/job.json.
//
// Shared by every executor that dumps a text-based export locally rather
// than relying on gzip being present inside the target container
// (postgres, mysql, rabbitmq) - extracted here once a third
// near-identical copy would otherwise have existed.
func GzipToFile(data []byte, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	if _, err := gw.Write(data); err != nil {
		gw.Close()
		return err
	}
	return gw.Close()
}
