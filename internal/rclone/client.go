// Package rclone wraps the `rclone` CLI for uploading, listing, and
// pruning backups on the configured remote (Google Drive by default, per
// remote name "dockvault_backup").
package rclone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Client shells out to an rclone binary on PATH (or an overridden path)
// against a fixed remote name.
type Client struct {
	BinPath string // defaults to "rclone"
	Remote  string // e.g. "dockvault_backup"
}

// New returns a Client for the given remote name.
func New(remote string) *Client {
	return &Client{BinPath: "rclone", Remote: remote}
}

func (c *Client) bin() string {
	if c.BinPath == "" {
		return "rclone"
	}
	return c.BinPath
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("rclone %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// remotePath prefixes path with "<remote>:".
func (c *Client) remotePath(path string) string {
	return c.Remote + ":" + strings.TrimPrefix(path, "/")
}

// ManifestSuffix is appended to an archive's filename to name its
// integrity-manifest sidecar (see internal/backup.ManifestPathFor) -
// shared here, not in internal/backup, because ListFiles/Prune (this
// package) need it to filter/co-delete manifests, and internal/backup
// already imports internal/rclone (for RcloneClient's FileInfo), so the
// reverse import would cycle.
const ManifestSuffix = ".manifest.json"

// CheckConfigured verifies the remote exists in rclone's config, so
// callers can give a clear "run `rclone config`" hint instead of an opaque
// failure deep into a backup run.
func (c *Client) CheckConfigured(ctx context.Context) error {
	out, err := c.run(ctx, "listremotes")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSuffix(strings.TrimSpace(line), ":") == c.Remote {
			return nil
		}
	}
	return fmt.Errorf("rclone remote %q is not configured\n  hint: run `rclone config` and create a remote named %q", c.Remote, c.Remote)
}

// Mkdir ensures remotePath exists on the configured remote via `rclone
// mkdir`, creating any missing parent directories - idempotent if the
// directory already exists.
//
// This exists to be called sequentially, once per unique destination
// directory, before any parallel job execution starts (see
// cli.runBackup) - NOT to be called concurrently for directories that
// might share an ancestor. Google Drive (unlike a POSIX filesystem) has
// no atomic "create directory if missing": two rclone processes racing
// to create the same not-yet-existing ancestor (e.g. two jobs both
// needing /stage/<host>/ for the first time) each independently see it
// missing and each create their own copy, leaving duplicate
// same-named folders behind. That's silently tolerated by uploads
// (`rclone copy` just picks one), but breaks path-based lookups like
// Hashsum deterministically ("directory not found"), since the fresh
// upload might have landed in the sibling nobody's looking in - found
// via real Shared Drive testing, not mocks: backing up 3 jobs at the
// default --parallel 3 against a real Google Shared Drive reliably
// produced duplicate `stage`/`archlinux`/... folders and post-upload
// hash verification failures; the same run at --parallel 1 was clean.
func (c *Client) Mkdir(ctx context.Context, remotePath string) error {
	if _, err := c.run(ctx, "mkdir", c.remotePath(remotePath)); err != nil {
		return fmt.Errorf("creating remote directory %s: %w", remotePath, err)
	}
	return nil
}

// Upload copies localPath to remotePath (a directory) on the configured
// remote via `rclone copy`, which - given a single source file - copies it
// into the destination directory under its own basename.
func (c *Client) Upload(ctx context.Context, localPath, remotePath string) error {
	if _, err := c.run(ctx, "copy", localPath, c.remotePath(remotePath)); err != nil {
		return fmt.Errorf("uploading %s: %w", localPath, err)
	}
	return nil
}

// lsjsonEntry mirrors the fields dockvault needs from `rclone lsjson`.
type lsjsonEntry struct {
	Path    string `json:"Path"`
	Name    string `json:"Name"`
	Size    int64  `json:"Size"`
	ModTime string `json:"ModTime"`
	IsDir   bool   `json:"IsDir"`
}

// ListFiles lists files (not directories) under remotePath, newest first,
// via `rclone lsjson`, for restore's file picker and `tree`.
func (c *Client) ListFiles(ctx context.Context, remotePath string) ([]FileInfo, error) {
	out, err := c.run(ctx, "lsjson", c.remotePath(remotePath))
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", remotePath, err)
	}
	var entries []lsjsonEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parsing rclone lsjson output: %w", err)
	}

	base := strings.TrimSuffix(remotePath, "/")
	files := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		// Manifests are a backup's integrity sidecar, not a backup
		// themselves - restore's file picker and Prune's retention math
		// both call ListFiles, and neither should ever see or count one.
		if strings.HasSuffix(e.Name, ManifestSuffix) {
			continue
		}
		files = append(files, FileInfo{
			Name:    e.Name,
			Size:    e.Size,
			ModTime: e.ModTime,
			Path:    base + "/" + e.Name,
		})
	}
	// ModTime is RFC3339, so a lexical string sort is also a chronological
	// sort - no need to parse it just to order these.
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime > files[j].ModTime })
	return files, nil
}

// TreeEntry is one file or directory entry from ListTree.
type TreeEntry struct {
	Path    string // relative to the queried root, e.g. "stage/host/postgres/job/file.tar.gz"
	Name    string
	Size    int64
	ModTime string
	IsDir   bool
}

// ListTree recursively lists every file and directory under remotePath
// via `rclone lsjson --recursive`, for `dockvault tree`'s remote folder
// view - unlike ListFiles, directories are included since the point here
// is to render the /<environment>/<hostname>/<service_type>/<job_name>/
// hierarchy itself, not just pick a backup file.
func (c *Client) ListTree(ctx context.Context, remotePath string) ([]TreeEntry, error) {
	out, err := c.run(ctx, "lsjson", "--recursive", c.remotePath(remotePath))
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", remotePath, err)
	}
	var entries []lsjsonEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parsing rclone lsjson output: %w", err)
	}

	tree := make([]TreeEntry, 0, len(entries))
	for _, e := range entries {
		tree = append(tree, TreeEntry{Path: e.Path, Name: e.Name, Size: e.Size, ModTime: e.ModTime, IsDir: e.IsDir})
	}
	return tree, nil
}

// Download copies a remote file to localDest (a directory) via
// `rclone copy`.
func (c *Client) Download(ctx context.Context, remotePath, localDest string) error {
	if _, err := c.run(ctx, "copy", c.remotePath(remotePath), localDest); err != nil {
		return fmt.Errorf("downloading %s: %w", remotePath, err)
	}
	return nil
}

// Prune deletes individual files older than retentionDays under
// remotePath, per the spec's Retention & Pruning rules - but never deletes
// into the newest minFilesToKeep files, no matter how old they are.
//
// This used to run a single blanket `rclone delete <path> --min-age Xd`,
// gated only on "does the directory currently have more than
// minFilesToKeep files at all". That gate does not bound how many files
// the subsequent delete actually removes: if a backlog of old files ever
// built up (e.g. retention_days was lowered, or backups paused for a
// while then resumed), a single Prune call could delete far more than
// intended and leave fewer than minFilesToKeep backups - the exact
// "destructive cleanup without verifying enough backups remain" failure
// mode a backup tool must never allow. Found via security/reliability
// audit, fixed by computing the exact set of files eligible for deletion
// up front (FilesToPrune - everything past the newest minFilesToKeep,
// intersected with "older than retentionDays") and deleting only those,
// one by one, via `rclone deletefile`.
func (c *Client) Prune(ctx context.Context, remotePath string, retentionDays, minFilesToKeep int) ([]string, error) {
	files, err := c.ListFiles(ctx, remotePath)
	if err != nil {
		return nil, fmt.Errorf("checking file count before prune: %w", err)
	}

	toDelete := FilesToPrune(files, retentionDays, minFilesToKeep)
	pruned := make([]string, 0, len(toDelete))
	for _, f := range toDelete {
		if _, err := c.run(ctx, "deletefile", c.remotePath(f.Path)); err != nil {
			return pruned, fmt.Errorf("deleting %s (kept %d of the newest %d backups intact so far): %w", f.Path, len(pruned), minFilesToKeep, err)
		}
		pruned = append(pruned, f.Name)
		// Best-effort: an archive's manifest sidecar (see ManifestSuffix)
		// isn't in files at all (ListFiles filters it out), so it'd
		// otherwise be orphaned on the remote forever once its archive is
		// gone. A failure here is silently ignored - it's at most a few
		// hundred bytes of harmless leftover JSON, not worth failing or
		// even warning about an otherwise-successful prune over.
		_, _ = c.run(ctx, "deletefile", c.remotePath(f.Path+ManifestSuffix))
	}
	return pruned, nil
}

// Hashsum returns remotePath's MD5 checksum as reported by the remote
// backend, via `rclone hashsum md5`. Google Drive natively exposes MD5
// for every object, so this needs no local re-download to compute it.
// Used right after an upload to catch corruption introduced in transit -
// see internal/backup.UploadArchive.
func (c *Client) Hashsum(ctx context.Context, remotePath string) (string, error) {
	out, err := c.run(ctx, "hashsum", "md5", c.remotePath(remotePath))
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", remotePath, err)
	}
	// Output is "<hash>  <path>" per line (rclone's own hashsum format).
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return "", fmt.Errorf("hashing %s: empty output from rclone hashsum", remotePath)
	}
	return fields[0], nil
}

// FilesToPrune decides which of files (expected newest-first, as
// ListFiles returns them) Prune should delete: only files beyond the
// newest minFilesToKeep, and only those older than retentionDays. This is
// the actual safety guarantee - the newest minFilesToKeep files are never
// candidates for deletion here, regardless of their age, unlike an
// age-only `--min-age` filter which has no concept of a minimum-count
// floor. Exported (rather than a package-private helper) so it can be
// tested directly without a real rclone binary/remote.
func FilesToPrune(files []FileInfo, retentionDays, minFilesToKeep int) []FileInfo {
	if len(files) <= minFilesToKeep {
		return nil
	}
	candidates := files[minFilesToKeep:]

	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var out []FileInfo
	for _, f := range candidates {
		t, err := time.Parse(time.RFC3339, f.ModTime)
		if err == nil && t.Before(cutoff) {
			out = append(out, f)
		}
	}
	return out
}

// FileInfo is one file entry from ListFiles.
type FileInfo struct {
	Name    string
	Size    int64
	ModTime string
	Path    string
}

// ConfigureServiceAccountRemote writes (or updates) an rclone remote named
// name, pointing at a Google Shared Drive via a service account JSON key,
// through `rclone config create <name> drive scope=drive
// service_account_file=... team_drive=... --non-interactive`.
//
// Confirmed against a real remote (not assumed from docs): `config
// create --non-interactive` against an *already-existing* remote name
// still emits an informational JSON "state machine" continuation blob on
// stdout (e.g. asking whether to change the Shared Drive ID) even though
// it applies the given values immediately and exits 0 either way - this
// method deliberately ignores that stdout content and relies on the exit
// code alone (via run's existing error handling), same as every other
// method in this file. It also does NOT validate serviceAccountFile or
// teamDriveID are actually correct - rclone accepts nonexistent paths and
// bogus drive IDs at config-write time without complaint; call
// ListSharedDrives afterward for the real test.
func (c *Client) ConfigureServiceAccountRemote(ctx context.Context, name, serviceAccountFile, teamDriveID string) error {
	_, err := c.run(ctx, "config", "create", name, "drive",
		"scope=drive",
		"service_account_file="+serviceAccountFile,
		"team_drive="+teamDriveID,
		"--non-interactive",
	)
	if err != nil {
		return fmt.Errorf("configuring rclone remote %q: %w", name, err)
	}
	return nil
}

// SharedDrive is one Google Shared Drive a service account can see, as
// reported by ListSharedDrives.
type SharedDrive struct {
	ID   string
	Name string
}

// backendDrivesEntry mirrors one element of `rclone backend drives`'s
// JSON array output (confirmed against a real Shared Drive: `[{"id":
// "...", "kind": "drive#drive", "name": "..."}]`) - only id/name are
// used, kind is always "drive#drive" and not otherwise meaningful here.
type backendDrivesEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListSharedDrives lists every Shared Drive remoteName's service account
// can see, via `rclone backend drives <remote>:`. Unlike
// ConfigureServiceAccountRemote, this genuinely exercises the service
// account credentials against the real Drive API - a remote configured
// with a nonexistent file or wrong team_drive fails here, not at config
// time. Used both to let a user pick a Shared Drive interactively and to
// confirm end-to-end access before considering setup complete.
func (c *Client) ListSharedDrives(ctx context.Context, remoteName string) ([]SharedDrive, error) {
	out, err := c.run(ctx, "backend", "drives", remoteName+":")
	if err != nil {
		return nil, fmt.Errorf("listing Shared Drives visible to remote %q: %w", remoteName, err)
	}
	return ParseBackendDrives(out)
}

// ParseBackendDrives is ListSharedDrives's JSON parsing split out as a
// pure, exported function (mirrors FilesToPrune's precedent), so it can
// be tested directly without a real rclone binary/remote.
func ParseBackendDrives(out []byte) ([]SharedDrive, error) {
	var entries []backendDrivesEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parsing rclone backend drives output: %w", err)
	}
	drives := make([]SharedDrive, 0, len(entries))
	for _, e := range entries {
		drives = append(drives, SharedDrive{ID: e.ID, Name: e.Name})
	}
	return drives, nil
}
