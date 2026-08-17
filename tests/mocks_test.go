package tests

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // matching Google Drive's own exposed checksum format in the mock, not for anything security-sensitive.
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dockvault/internal/docker"
	"dockvault/internal/rclone"
)

// gzipBytes gzip-compresses s, for tests that need to hand a restore
// executor's downloadAndRead(..., gunzip: true) path something to decompress.
func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte(s)); err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzipBytes: %v", err)
	}
	return buf.Bytes()
}

// mockDockerClient fakes docker.Client for tests - no docker daemon is
// available in every environment this runs in, per the project's testing
// notes, and a real daemon would make these tests slow/flaky anyway.
type mockDockerClient struct {
	runErr   error
	execErr  error
	stopErr  error
	startErr error
	cpErr    error

	// execOutput is returned by every Exec call unless execFunc is set.
	execOutput []byte
	// execFunc, if set, takes over Exec entirely (still recorded in
	// execCalls first) - used when a test needs per-call behavior, e.g.
	// redis's LASTSAVE value changing between polls.
	execFunc func(ctx context.Context, container string, cmd []string, env map[string]string, stdin io.Reader) ([]byte, error)

	gotImage  string
	gotMounts []docker.MountSpec
	gotCmd    []string

	execCalls  []execCall
	stopCalls  []string
	startCalls []string
	cpCalls    []cpCall
}

type execCall struct {
	Container string
	Cmd       []string
	Env       map[string]string
	Stdin     []byte
}

type cpCall struct {
	Src string
	Dst string
}

// Run emulates `docker run --rm ... alpine tar czf /backup/<file> ...` by
// writing a dummy archive into the host directory bind-mounted at
// /backup, since every tar-based executor stats that file immediately
// afterward.
func (m *mockDockerClient) Run(ctx context.Context, image string, mounts []docker.MountSpec, cmd []string) ([]byte, error) {
	m.gotImage, m.gotMounts, m.gotCmd = image, mounts, cmd
	if m.runErr != nil {
		return nil, m.runErr
	}

	var backupDir string
	for _, mnt := range mounts {
		if mnt.Target == "/backup" {
			backupDir = mnt.Source
		}
	}
	for _, arg := range cmd {
		if name, ok := strings.CutPrefix(arg, "/backup/"); ok && backupDir != "" {
			if err := os.WriteFile(filepath.Join(backupDir, name), []byte("fake tar contents"), 0o644); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func (m *mockDockerClient) Exec(ctx context.Context, container string, cmd []string, env map[string]string, stdin io.Reader) ([]byte, error) {
	var stdinBytes []byte
	if stdin != nil {
		stdinBytes, _ = io.ReadAll(stdin)
	}
	m.execCalls = append(m.execCalls, execCall{
		Container: container,
		Cmd:       append([]string(nil), cmd...),
		Env:       env,
		Stdin:     stdinBytes,
	})

	if m.execFunc != nil {
		return m.execFunc(ctx, container, cmd, env, stdin)
	}
	if m.execErr != nil {
		return nil, m.execErr
	}
	return m.execOutput, nil
}

func (m *mockDockerClient) Stop(ctx context.Context, container string) error {
	m.stopCalls = append(m.stopCalls, container)
	return m.stopErr
}

func (m *mockDockerClient) Start(ctx context.Context, container string) error {
	m.startCalls = append(m.startCalls, container)
	return m.startErr
}

// Cp emulates `docker cp`: when dst doesn't look like a "container:path"
// form (no colon), it's a local destination, so a dummy file is written
// there for callers that os.Stat it afterward (redis's backup/restore).
func (m *mockDockerClient) Cp(ctx context.Context, src, dst string) error {
	m.cpCalls = append(m.cpCalls, cpCall{Src: src, Dst: dst})
	if m.cpErr != nil {
		return m.cpErr
	}
	if !strings.Contains(dst, ":") {
		return os.WriteFile(dst, []byte("fake rdb contents"), 0o644)
	}
	return nil
}

// mockRcloneClient fakes rclone.Client's Upload/ListFiles/Download/Prune/Hashsum.
type mockRcloneClient struct {
	uploadErr   error
	pruneErr    error
	downloadErr error

	uploadCalled bool
	pruneCalled  bool
	gotLocal     string // set from the *last* Upload call - see uploadCalls for every call
	gotRemote    string
	uploadCalls  []uploadCall
	// uploadErrOnCall fails one specific 0-based Upload call index (0 =
	// archive, 1 = manifest) rather than every call the way uploadErr
	// does - lets a test isolate "the manifest upload specifically failed"
	// from "every upload failed".
	uploadErrOnCall map[int]error

	// listFiles is returned by ListFiles unless listFilesErr is set.
	listFiles    []rclone.FileInfo
	listFilesErr error

	// downloadInto, if set, is written to <localDest>/<basename(remotePath)>
	// by Download for any remotePath with no more specific entry in
	// downloadByRemote below - so tests that only care about the archive
	// download (not the manifest one UploadArchive/restore now also
	// issue) don't need to change. A remotePath with no entry anywhere
	// (no downloadByRemote, no downloadInto) succeeds without writing
	// anything - this is deliberate: it's what makes the "no manifest
	// available" warm-and-proceed path exercise naturally in tests that
	// never mention manifests at all.
	downloadInto        []byte
	downloadCalls       []string
	downloadByRemote    map[string][]byte
	downloadErrByRemote map[string]error

	// hashsumErr/hashsumOverride force Hashsum's result for tests that
	// need to simulate a remote-side integrity failure. With neither set,
	// Hashsum computes the real MD5 of whichever uploaded local file's
	// basename matches remotePath's - i.e. a well-behaved remote that
	// faithfully reports back the hash of what was actually sent, so
	// tests that don't care about hash verification need no setup for it
	// to "just work".
	hashsumErr      error
	hashsumOverride string
	hashsumCalls    []string
}

type uploadCall struct {
	Local  string
	Remote string
}

func (m *mockRcloneClient) Upload(ctx context.Context, localPath, remotePath string) error {
	m.uploadCalled = true
	m.gotLocal, m.gotRemote = localPath, remotePath
	callIndex := len(m.uploadCalls)
	m.uploadCalls = append(m.uploadCalls, uploadCall{Local: localPath, Remote: remotePath})
	// uploadErrOnCall lets a test fail one specific Upload call (e.g. only
	// the manifest, the 2nd call) without failing every call the way
	// uploadErr does - checked first so a call-specific error always wins.
	if m.uploadErrOnCall != nil {
		if err, ok := m.uploadErrOnCall[callIndex]; ok {
			return err
		}
	}
	return m.uploadErr
}

func (m *mockRcloneClient) ListFiles(ctx context.Context, remotePath string) ([]rclone.FileInfo, error) {
	if m.listFilesErr != nil {
		return nil, m.listFilesErr
	}
	return m.listFiles, nil
}

func (m *mockRcloneClient) Download(ctx context.Context, remotePath, localDest string) error {
	m.downloadCalls = append(m.downloadCalls, remotePath)

	if m.downloadErrByRemote != nil {
		if err, ok := m.downloadErrByRemote[remotePath]; ok {
			return err
		}
	}
	if m.downloadErr != nil {
		return m.downloadErr
	}
	if m.downloadByRemote != nil {
		if data, ok := m.downloadByRemote[remotePath]; ok {
			return os.WriteFile(filepath.Join(localDest, filepath.Base(remotePath)), data, 0o644)
		}
	}
	if m.downloadInto != nil {
		return os.WriteFile(filepath.Join(localDest, filepath.Base(remotePath)), m.downloadInto, 0o644)
	}
	return nil
}

func (m *mockRcloneClient) Prune(ctx context.Context, remotePath string, retentionDays, minFilesToKeep int) ([]string, error) {
	m.pruneCalled = true
	return nil, m.pruneErr
}

func (m *mockRcloneClient) Hashsum(ctx context.Context, remotePath string) (string, error) {
	m.hashsumCalls = append(m.hashsumCalls, remotePath)
	if m.hashsumErr != nil {
		return "", m.hashsumErr
	}
	if m.hashsumOverride != "" {
		return m.hashsumOverride, nil
	}
	base := filepath.Base(remotePath)
	for _, c := range m.uploadCalls {
		if filepath.Base(c.Local) == base {
			data, err := os.ReadFile(c.Local)
			if err != nil {
				return "", err
			}
			sum := md5.Sum(data)
			return hex.EncodeToString(sum[:]), nil
		}
	}
	return "", fmt.Errorf("mock Hashsum: no matching upload recorded for %s", remotePath)
}
