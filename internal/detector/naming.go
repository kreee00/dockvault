package detector

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// anonymousVolumeRE matches Docker's auto-generated 64-character hex
// anonymous-volume name (e.g. from `docker run -v /data` with no name
// given) - the shape used to tell an anonymous volume apart from a
// human-named one.
var anonymousVolumeRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// VolumeIdentifier returns the <volume_identifier> archive-filename
// component for a Docker volume, per the DevOps naming convention: a
// named volume is used as-is (e.g. "pgdata"); an anonymous volume (a
// 64-hex-char auto-generated name) is truncated to its first 8 characters
// and prefixed "vol-" (e.g. "vol-1b3f2395") so archive names stay short
// and readable.
func VolumeIdentifier(volumeName string) string {
	if anonymousVolumeRE.MatchString(volumeName) {
		return "vol-" + volumeName[:8]
	}
	return volumeName
}

// BindMountIdentifier returns the <volume_identifier> archive-filename
// component for a bind mount: "bind-" plus the mount's target directory
// name (e.g. a container mount at /usr/share/nginx/html -> "bind-html").
// target is always a container-internal path (POSIX-style regardless of
// host OS), so this uses "path", not "path/filepath".
func BindMountIdentifier(target string) string {
	base := path.Base(path.Clean(target))
	if base == "" || base == "." || base == "/" {
		base = "root"
	}
	return "bind-" + base
}

// RemotePath builds the standardized Google Drive remote directory:
//
//	/<environment>/<hostname>/<service_type>/<job_name>/
//
// e.g. "/stage/scada-db/redis/n8n-redis-1/". All four components are
// lowercased here (not just trusted from the caller) so the hierarchy
// strictly adheres to the convention regardless of input casing.
func RemotePath(environment, hostname, serviceType, jobName string) string {
	return fmt.Sprintf("/%s/%s/%s/%s/",
		strings.ToLower(environment),
		strings.ToLower(hostname),
		strings.ToLower(serviceType),
		strings.ToLower(jobName),
	)
}

// NormalizeEnvironment lowercases env and falls back to "stage" when it's
// empty (or all whitespace) - the default when no configuration or CLI
// flag supplies one.
func NormalizeEnvironment(env string) string {
	env = strings.ToLower(strings.TrimSpace(env))
	if env == "" {
		return "stage"
	}
	return env
}
