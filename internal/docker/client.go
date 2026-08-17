// Package docker wraps the `docker` CLI. Per the project's "standard
// library only, preferred for portability" guidance, this shells out to
// the docker binary instead of pulling in the official Docker SDK -
// dockvault only ever needs volume/container listing and inspection,
// which the CLI covers without adding a dependency.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Client shells out to a docker binary on PATH (or an overridden path).
type Client struct {
	// BinPath is the docker executable to invoke. Defaults to "docker".
	BinPath string
}

// New returns a Client that invokes "docker" on PATH.
func New() *Client {
	return &Client{BinPath: "docker"}
}

func (c *Client) bin() string {
	if c.BinPath == "" {
		return "docker"
	}
	return c.BinPath
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	return c.runWithStdin(ctx, nil, args...)
}

func (c *Client) runWithStdin(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		// RedactArgs, not args: Exec passes DB credentials via `-e
		// KEY=VALUE` (PGPASSWORD, MYSQL_PWD, REDISCLI_AUTH) and mongodump/
		// mongorestore via a literal `--password <value>` argument. This
		// error string is what ends up in job.LastBackupStatus, which in
		// turn is logged, written into manifest.json, and sent in webhook
		// payloads on failure - embedding the raw command here would leak
		// the credential to all three. Found via security audit, not by
		// design: an earlier version of this line used strings.Join(args,
		// " ") directly.
		return nil, fmt.Errorf("docker %s: %s", strings.Join(RedactArgs(args), " "), msg)
	}
	return stdout.Bytes(), nil
}

// RedactArgs returns a copy of args with values following known
// credential-bearing flags replaced by "***" - used only for error
// messages, never for the actual command invocation. Covers every way
// this codebase's executors pass a secret on the command line today:
// `-e`/`--env KEY=VALUE` (docker exec's env vars) and `--password VALUE`
// (mongodump/mongorestore, which docker exec's CLI has no env-var
// equivalent for).
func RedactArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i < len(out); i++ {
		switch out[i] {
		case "-e", "--env":
			if i+1 < len(out) {
				if k, _, ok := strings.Cut(out[i+1], "="); ok {
					out[i+1] = k + "=***"
				} else {
					out[i+1] = "***"
				}
			}
		case "--password":
			if i+1 < len(out) {
				out[i+1] = "***"
			}
		}
	}
	return out
}

// Ping verifies the docker daemon is reachable. Callers should surface a
// remediation hint (start Docker Desktop / check the socket / permissions)
// when this fails, per the project's error-message conventions.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.run(ctx, "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		return fmt.Errorf("docker daemon not reachable: %w", err)
	}
	return nil
}

// VolumeSummary is one row of `docker volume ls`.
type VolumeSummary struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Mountpoint string `json:"Mountpoint"`
}

// ListVolumes returns all named Docker volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]VolumeSummary, error) {
	out, err := c.run(ctx, "volume", "ls", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	var vols []VolumeSummary
	for _, line := range splitLines(out) {
		var v VolumeSummary
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("parsing `docker volume ls` output: %w", err)
		}
		vols = append(vols, v)
	}
	return vols, nil
}

// ContainerRef is a lightweight container identity, used to fan out to
// InspectContainer for full detail.
type ContainerRef struct {
	ID    string `json:"ID"`
	Names string `json:"Names"`
	Image string `json:"Image"`
}

// ListContainers returns container refs. When all is false, only running
// containers are returned (matches `docker ps`); when true, stopped
// containers are included too (`docker ps -a`).
func (c *Client) ListContainers(ctx context.Context, all bool) ([]ContainerRef, error) {
	args := []string{"ps", "--format", "{{json .}}"}
	if all {
		args = append(args, "--all")
	}
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var refs []ContainerRef
	for _, line := range splitLines(out) {
		var r ContainerRef
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("parsing `docker ps` output: %w", err)
		}
		refs = append(refs, r)
	}
	return refs, nil
}

// Mount describes one mount point on a container, normalized from
// `docker inspect`.
type Mount struct {
	// Type is "volume", "bind", or "tmpfs".
	Type string
	// Name is the volume name; only set when Type == "volume".
	Name        string
	Source      string
	Destination string
}

// ContainerDetail is the subset of `docker inspect` output dockvault's
// detector needs: identity, env (for credential extraction), mounts (for
// volume/bind-mount discovery), published ports, and run state.
type ContainerDetail struct {
	ID      string
	Name    string
	Image   string
	Running bool
	Env     map[string]string
	Mounts  []Mount
	// Ports is the set of container-side ports published to the host,
	// used for the optional "port suggests service type" heuristic.
	Ports []int
}

// raw mirrors just the fields of `docker inspect` dockvault reads.
type rawInspect struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
	Config struct {
		Image string   `json:"Image"`
		Env   []string `json:"Env"`
	} `json:"Config"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Ports map[string]json.RawMessage `json:"Ports"`
	} `json:"NetworkSettings"`
}

// InspectContainer runs `docker inspect` on a single container ID or name
// and normalizes the result.
func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerDetail, error) {
	out, err := c.run(ctx, "inspect", id)
	if err != nil {
		return nil, err
	}
	var raws []rawInspect
	if err := json.Unmarshal(out, &raws); err != nil {
		return nil, fmt.Errorf("parsing `docker inspect %s` output: %w", id, err)
	}
	if len(raws) == 0 {
		return nil, fmt.Errorf("docker inspect %s: no such object", id)
	}
	r := raws[0]

	detail := &ContainerDetail{
		ID:      r.ID,
		Name:    strings.TrimPrefix(r.Name, "/"),
		Image:   r.Config.Image,
		Running: r.State.Running,
		Env:     map[string]string{},
	}
	for _, kv := range r.Config.Env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			detail.Env[parts[0]] = parts[1]
		}
	}
	for _, m := range r.Mounts {
		detail.Mounts = append(detail.Mounts, Mount{
			Type:        m.Type,
			Name:        m.Name,
			Source:      m.Source,
			Destination: m.Destination,
		})
	}
	for portProto := range r.NetworkSettings.Ports {
		// portProto looks like "5432/tcp"
		p := strings.SplitN(portProto, "/", 2)[0]
		if n, err := strconv.Atoi(p); err == nil {
			detail.Ports = append(detail.Ports, n)
		}
	}
	return detail, nil
}

// InspectContainers inspects multiple containers, continuing past
// individual failures (a container that exits mid-scan shouldn't abort the
// whole scan) and returning the errors alongside successful results.
func (c *Client) InspectContainers(ctx context.Context, ids []string) ([]*ContainerDetail, []error) {
	var details []*ContainerDetail
	var errs []error
	for _, id := range ids {
		d, err := c.InspectContainer(ctx, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		details = append(details, d)
	}
	return details, errs
}

// MountSpec describes one `-v` mount passed to Run.
type MountSpec struct {
	// Source is a volume name or host path.
	Source string
	// Target is the in-container mount path.
	Target   string
	ReadOnly bool
}

func (m MountSpec) flag() string {
	spec := m.Source + ":" + m.Target
	if m.ReadOnly {
		spec += ":ro"
	}
	return spec
}

// Run starts a throwaway container (`docker run --rm`) with the given
// mounts, runs cmd, and returns its stdout. Used by backup executors that
// need a helper container (e.g. alpine tar over a volume) rather than
// executing inside an already-running service container - see Exec for
// that case.
func (c *Client) Run(ctx context.Context, image string, mounts []MountSpec, cmd []string) ([]byte, error) {
	args := []string{"run", "--rm"}
	for _, m := range mounts {
		args = append(args, "-v", m.flag())
	}
	args = append(args, image)
	args = append(args, cmd...)
	out, err := c.run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("%w\n  hint: is Docker running? try `docker ps`", err)
	}
	return out, nil
}

// Exec runs cmd inside an already-running container (`docker exec`), with
// env applied as `-e KEY=VALUE` flags and stdin (if non-nil) piped to the
// process - used both by service-specific backup executors (postgres
// pg_dump, mysql mysqldump, ...) that shell out to a tool already
// installed in the target container, and by restores that feed a
// downloaded dump back in via stdin (psql, mysql, mongorestore).
func (c *Client) Exec(ctx context.Context, container string, cmd []string, env map[string]string, stdin io.Reader) ([]byte, error) {
	args := []string{"exec"}
	if stdin != nil {
		args = append(args, "-i")
	}
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, container)
	args = append(args, cmd...)
	out, err := c.runWithStdin(ctx, stdin, args...)
	if err != nil {
		return nil, fmt.Errorf("%w\n  hint: is container %q running? try `docker ps`", err, container)
	}
	return out, nil
}

// Stop wraps `docker stop` - used by executors that must take a container
// offline to safely touch its data files (redis, n8n).
func (c *Client) Stop(ctx context.Context, container string) error {
	if _, err := c.run(ctx, "stop", container); err != nil {
		return fmt.Errorf("%w\n  hint: is container %q still there? try `docker ps -a`", err, container)
	}
	return nil
}

// Start wraps `docker start` - the counterpart to Stop, also used by
// restore's post-restore "start the container back up?" prompt.
func (c *Client) Start(ctx context.Context, container string) error {
	if _, err := c.run(ctx, "start", container); err != nil {
		return fmt.Errorf("%w\n  hint: is container %q still there? try `docker ps -a`", err, container)
	}
	return nil
}

// Cp wraps `docker cp` in either direction - src or dst uses the
// "container:path" form on whichever side is inside the container. Used
// by redis's dump.rdb extraction and restore.
func (c *Client) Cp(ctx context.Context, src, dst string) error {
	if _, err := c.run(ctx, "cp", src, dst); err != nil {
		return fmt.Errorf("copying %s -> %s: %w", src, dst, err)
	}
	return nil
}

func splitLines(out []byte) []string {
	var lines []string
	for _, l := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimSpace(string(l))
		if s != "" {
			lines = append(lines, s)
		}
	}
	return lines
}
