// Package detector implements dockvault's "smart auto-detection": given a
// running Docker host, it discovers volumes, containers, bind mounts, and
// notable host paths, infers what service each one belongs to, extracts
// credentials from the container environment, and proposes a job name,
// backup strategy, and Google Drive destination for each - the pipeline
// behind `dockvault scan --auto` and `dockvault generate --auto`.
package detector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"dockvault/internal/docker"
)

// VolumeInfo is a fully-analyzed Docker volume: what it is, what's using
// it, what service it belongs to, and what dockvault proposes to do with
// it. This is the primary output of ScanVolumes and mirrors the project
// spec's Detector Interface exactly.
type VolumeInfo struct {
	Name                  string
	Containers            []string // container IDs/names using this volume
	ServiceType           string   // "postgres", "mysql", "redis", "n8n", "generic", ...
	Credentials           map[string]string
	RecommendedBackupType string // "pg_dump", "mysqldump", "tar", ...
	JobName               string // auto-generated sensible name
	GoogleDrivePath       string // auto-derived remote path: /<environment>/<hostname>/<service_type>/<job_name>/
	// VolumeIdentifier is the <volume_identifier> archive-filename
	// component for this volume (see VolumeIdentifier()): the volume name
	// as-is if named, or "vol-<hash8>" if anonymous.
	VolumeIdentifier string
}

// ContainerInfo is a normalized view of a running container, independent
// of the docker package's wire types, so the detector's public API
// doesn't leak `docker inspect` JSON shapes.
type ContainerInfo struct {
	ID      string
	Name    string
	Image   string
	Running bool
	Env     map[string]string
	Ports   []int
}

// HostPathInfo describes a host filesystem path dockvault noticed and
// might want to back up: a bind mount into a container, one of the
// "notable" well-known config directories, or a discovered .env file.
type HostPathInfo struct {
	Path   string
	Kind   string // "bind-mount", "notable-path", "env-file"
	Source string // container name/ID this came from, when Kind == "bind-mount"

	RecommendedBackupType string
	JobName               string
	GoogleDrivePath       string
	// VolumeIdentifier is the <volume_identifier> archive-filename
	// component for this path (see BindMountIdentifier()): "bind-<target
	// directory name>". Unset for Kind == "env-file", which isn't backed
	// up as its own job.
	VolumeIdentifier string
}

// Detector is the auto-detection interface, matching the project spec.
type Detector interface {
	ScanVolumes(ctx context.Context) ([]VolumeInfo, error)
	ScanContainers(ctx context.Context) ([]ContainerInfo, error)
	ScanHostPaths(ctx context.Context) ([]HostPathInfo, error)
	ExtractCredentials(container ContainerInfo) map[string]string
	DetectServiceType(imageName string) string
	GenerateJobName(volume, serviceType string, creds map[string]string) string
}

// envScanDepth implements Phase 1's ".env file discovery": scan
// envScanRootsFor(goos), four directories deep, for files named .env.
const envScanDepth = 4

type detectorImpl struct {
	docker      *docker.Client
	hostname    string
	environment string
}

// New returns the default Detector, backed by the given docker client. If
// dockerClient is nil, docker.New() is used. environment is normalized via
// NormalizeEnvironment (lowercased, defaulting to "stage" when empty) and
// becomes the leading component of every GoogleDrivePath this detector
// produces.
func New(dockerClient *docker.Client, environment string) Detector {
	if dockerClient == nil {
		dockerClient = docker.New()
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return &detectorImpl{
		docker:      dockerClient,
		hostname:    sanitizeComponent(host),
		environment: NormalizeEnvironment(environment),
	}
}

// ScanContainers lists containers (running only - matches the spec's
// "docker ps → all running containers") and normalizes each into a
// ContainerInfo, including env vars for later credential extraction.
func (d *detectorImpl) ScanContainers(ctx context.Context) ([]ContainerInfo, error) {
	refs, err := d.docker.ListContainers(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	var out []ContainerInfo
	for _, ref := range refs {
		detail, err := d.docker.InspectContainer(ctx, ref.ID)
		if err != nil {
			// A container that disappears mid-scan (race with `docker ps`)
			// shouldn't fail the whole scan.
			continue
		}
		out = append(out, ContainerInfo{
			ID:      detail.ID,
			Name:    detail.Name,
			Image:   detail.Image,
			Running: detail.Running,
			Env:     detail.Env,
			Ports:   detail.Ports,
		})
	}
	return out, nil
}

// ScanVolumes implements Phases 1-5: discover volumes, match them to
// containers, infer service type, extract credentials, recommend a backup
// strategy, and propose a job name and Google Drive path for each.
func (d *detectorImpl) ScanVolumes(ctx context.Context) ([]VolumeInfo, error) {
	vols, err := d.docker.ListVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}

	refs, err := d.docker.ListContainers(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.ID)
	}
	details, _ := d.docker.InspectContainers(ctx, ids)

	// Index containers by the volume names they mount, so each volume can
	// find every container using it (a volume can be shared).
	byVolume := map[string][]*docker.ContainerDetail{}
	for _, det := range details {
		for _, m := range det.Mounts {
			if m.Type != "volume" || m.Name == "" {
				continue
			}
			byVolume[m.Name] = append(byVolume[m.Name], det)
		}
	}

	var out []VolumeInfo
	for _, v := range vols {
		users := byVolume[v.Name]

		serviceType := ServiceGeneric
		creds := map[string]string{}
		var containerNames []string

		// Prefer the first non-generic match among the volume's users; a
		// volume mounted into an unrelated sidecar shouldn't override the
		// service the primary container clearly identifies.
		matched := false
		for _, c := range users {
			containerNames = append(containerNames, c.Name)
			t := d.DetectServiceType(c.Image)
			if !matched && t != ServiceGeneric {
				serviceType = t
				matched = true
			}
		}
		// Extract credentials from whichever container matched (or the
		// first user, if none matched a known service by image name).
		if len(users) > 0 {
			target := users[0]
			for _, c := range users {
				if d.DetectServiceType(c.Image) == serviceType {
					target = c
					break
				}
			}
			ci := ContainerInfo{ID: target.ID, Name: target.Name, Image: target.Image, Env: target.Env}
			creds = d.ExtractCredentials(ci)

			// Optional port-based confirmation (Phase 2.3): only used to
			// fill in a service type when the image name didn't already
			// tell us, never to override an image-name match.
			if !matched {
				for _, p := range target.Ports {
					if hint := DetectServiceTypeFromPort(p); hint != "" {
						serviceType = hint
						break
					}
				}
			}
		}

		jobName := d.GenerateJobName(v.Name, serviceType, creds)
		out = append(out, VolumeInfo{
			Name:                  v.Name,
			Containers:            containerNames,
			ServiceType:           serviceType,
			Credentials:           creds,
			RecommendedBackupType: RecommendedBackupType(serviceType),
			JobName:               jobName,
			GoogleDrivePath:       d.googleDrivePath(serviceType, jobName),
			VolumeIdentifier:      VolumeIdentifier(v.Name),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ScanHostPaths implements the bind-mount, notable-path, and .env-file
// discovery described in Phase 1.
func (d *detectorImpl) ScanHostPaths(ctx context.Context) ([]HostPathInfo, error) {
	var out []HostPathInfo
	seen := map[string]bool{}

	add := func(hp HostPathInfo) {
		if seen[hp.Path] {
			return
		}
		seen[hp.Path] = true
		out = append(out, hp)
	}

	// Bind mounts: cross-reference running containers' mounts.
	refs, err := d.docker.ListContainers(ctx, false)
	if err == nil {
		ids := make([]string, 0, len(refs))
		for _, r := range refs {
			ids = append(ids, r.ID)
		}
		details, _ := d.docker.InspectContainers(ctx, ids)
		for _, det := range details {
			for _, m := range det.Mounts {
				if m.Type != "bind" || m.Source == "" {
					continue
				}
				jobName := hostDirJobName(m.Source)
				add(HostPathInfo{
					Path:                  m.Source,
					Kind:                  "bind-mount",
					Source:                det.Name,
					RecommendedBackupType: BackupTar,
					JobName:               jobName,
					GoogleDrivePath:       d.googleDrivePath(ServiceHostDir, jobName),
					VolumeIdentifier:      BindMountIdentifier(m.Destination),
				})
			}
		}
	}

	// Notable well-known paths, if present on this host.
	for _, p := range notableHostPaths(currentGOOS) {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			jobName := hostDirJobName(p)
			add(HostPathInfo{
				Path:                  p,
				Kind:                  "notable-path",
				RecommendedBackupType: BackupTar,
				JobName:               jobName,
				GoogleDrivePath:       d.googleDrivePath(ServiceHostDir, jobName),
				// No container bind-mount destination exists for a
				// notable path found directly on the host, so the
				// identifier is derived from the host path itself.
				VolumeIdentifier: BindMountIdentifier(p),
			})
		}
	}

	// .env files, up to 4 directories deep under each scan root.
	for _, root := range envScanRootsFor(currentGOOS) {
		walkForEnvFiles(root, envScanDepth, func(path string) {
			add(HostPathInfo{
				Path: path,
				Kind: "env-file",
			})
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ExtractCredentials pulls known credential env vars for the container's
// inferred service type, plus a catch-all for anything else that looks
// like a secret (contains PASSWORD, TOKEN, SECRET, or API_KEY), per
// Phase 2.2.
func (d *detectorImpl) ExtractCredentials(container ContainerInfo) map[string]string {
	creds := map[string]string{}
	serviceType := d.DetectServiceType(container.Image)

	known := map[string]bool{}
	for _, key := range credentialEnvKeys[serviceType] {
		known[key] = true
		if v, ok := container.Env[key]; ok {
			creds[key] = v
		}
	}

	for key, val := range container.Env {
		if known[key] {
			continue // already captured above
		}
		upper := strings.ToUpper(key)
		for _, marker := range genericSecretMarkers {
			if strings.Contains(upper, marker) {
				creds[key] = val
				break
			}
		}
	}
	return creds
}

// DetectServiceType infers a service type from an image name.
func (d *detectorImpl) DetectServiceType(imageName string) string {
	return DetectServiceType(imageName)
}

// GenerateJobName proposes a job name per Phase 4:
//
//   - <service>_<db>            when a database name was extracted
//   - <service>_user_backup     when only a user credential was extracted (no db)
//   - <service>_<volume-suffix> otherwise, derived from the volume's own name
//   - <service>_backup          final fallback
//
// The chosen name is always run through sanitizeComponent so it's safe to
// use as a directory name and a Google Drive path segment.
func (d *detectorImpl) GenerateJobName(volume, serviceType string, creds map[string]string) string {
	if db := dbNameFromCreds(serviceType, creds); db != "" {
		return sanitizeComponent(serviceType + "_" + db)
	}
	if hasUserCred(serviceType, creds) {
		return sanitizeComponent(serviceType + "_user_backup")
	}
	// Volume-name-derived suffixes only make sense for services without a
	// database concept to name the job after (e.g. redis_cache from a
	// deliberately-named "redis_cache" volume). ServiceGeneric has no
	// service-specific naming signal at all, so it always falls through
	// to the plain "generic_backup" rather than echoing the volume name
	// back (which would just duplicate the Volume field for no benefit).
	if serviceType != ServiceGeneric {
		// An anonymous volume's name is a 64-char hash with no semantic
		// meaning to derive a suffix from - use the same truncated
		// "vol-<hash8>" form ArchiveFileName uses instead of embedding the
		// full hash in the job name (volumeSuffix's _data/_db/_vol
		// stripping doesn't apply to a hash anyway).
		suffix := volumeSuffix(volume, serviceType)
		if anonymousVolumeRE.MatchString(volume) {
			suffix = VolumeIdentifier(volume)
		}
		if suffix != "" {
			return sanitizeComponent(serviceType + "_" + suffix)
		}
	}
	return sanitizeComponent(serviceType + "_backup")
}

func (d *detectorImpl) googleDrivePath(serviceType, jobName string) string {
	return RemotePath(d.environment, d.hostname, serviceType, jobName)
}

// hostDirJobName implements Phase 4's host-directory pattern:
// "<last-path-component>_hostdir".
func hostDirJobName(path string) string {
	base := filepath.Base(strings.TrimRight(path, "/"))
	if base == "" || base == "." || base == "/" {
		base = "root"
	}
	return sanitizeComponent(base + "_hostdir")
}

func dbNameFromCreds(serviceType string, creds map[string]string) string {
	var key string
	switch serviceType {
	case ServicePostgres:
		key = "POSTGRES_DB"
	case ServiceMySQL:
		key = "MYSQL_DATABASE"
	case ServiceMongoDB:
		key = "MONGO_INITDB_DATABASE"
	default:
		return ""
	}
	return creds[key]
}

func hasUserCred(serviceType string, creds map[string]string) bool {
	var key string
	switch serviceType {
	case ServicePostgres:
		key = "POSTGRES_USER"
	case ServiceMySQL:
		key = "MYSQL_USER"
	case ServiceMongoDB:
		key = "MONGO_INITDB_ROOT_USERNAME"
	default:
		return false
	}
	_, ok := creds[key]
	return ok
}

// volumeSuffix strips common noise (trailing "_data"/"_db"/"_volume"
// suffixes, then a leading "<service>_" prefix) from a volume name so
// "redis_data" -> "" (falls through to "redis_backup") but a
// deliberately-named volume like "redis_cache" -> "cache". Suffixes are
// stripped before the prefix so "redis_data" reduces to "redis" (a bare
// restatement of the service, not a useful name) rather than "data".
func volumeSuffix(volume, serviceType string) string {
	name := strings.ToLower(volume)
	for _, suf := range []string{"_data", "_db", "_volume", "_vol"} {
		name = strings.TrimSuffix(name, suf)
	}
	name = strings.TrimPrefix(name, strings.ToLower(serviceType)+"_")
	name = strings.Trim(name, "_")
	if name == "" || name == strings.ToLower(serviceType) {
		return ""
	}
	return name
}

var nonAlnumRE = regexp.MustCompile(`[^a-z0-9]+`)

// sanitizeComponent lowercases and collapses a string down to
// [a-z0-9_], suitable for use as a directory name or Google Drive path
// segment. Never returns an empty string for non-empty input.
func sanitizeComponent(s string) string {
	s = strings.ToLower(s)
	s = nonAlnumRE.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "unnamed"
	}
	return s
}

// walkForEnvFiles walks root up to maxDepth directories deep, calling fn
// for every regular file named ".env" it finds. Permission errors and
// missing roots are silently skipped - this is a best-effort discovery
// pass, not a required one.
func walkForEnvFiles(root string, maxDepth int, fn func(path string)) {
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth < 0 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if e.IsDir() {
				// Skip virtual filesystems, profile bloat, and other
				// directories that would make this unbounded or pointless
				// on some layouts (see skipDirNames in platform.go).
				if skipDirNames[e.Name()] {
					continue
				}
				walk(full, depth-1)
				continue
			}
			if e.Name() == ".env" {
				fn(full)
			}
		}
	}
	walk(root, maxDepth)
}
