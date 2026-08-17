package generator

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"dockvault/internal/backup"
	"dockvault/internal/detector"
	"dockvault/internal/utils"
	"dockvault/internal/workspace"
)

// Wizard drives the `generate --interactive` flow: scan -> pick an item
// -> fill in any credentials that couldn't be auto-detected -> per-job
// overrides (retention/min-files/webhook) -> confirm -> write, looping
// until the user chooses to finish.
type Wizard struct {
	Workspace *workspace.Workspace
	Detector  detector.Detector
	Generator *Generator
	// Docker is used only for PromptMissingCredentials' post-prompt
	// connectivity probe (docker exec pg_isready/mysqladmin ping/etc.) -
	// nil disables that probe (credentials are still collected, just not
	// tested), which is also what every test using this Wizard without a
	// mock docker client gets.
	Docker backup.DockerClient
}

// NewWizard returns a Wizard bound to ws, using det to (re-)scan the
// Docker host and dc for post-prompt connectivity probes (the same
// *docker.Client already passed to detector.New - see cli/generate.go).
func NewWizard(ws *workspace.Workspace, det detector.Detector, dc backup.DockerClient) *Wizard {
	return &Wizard{Workspace: ws, Detector: det, Generator: New(ws), Docker: dc}
}

// candidate is one scan result the wizard can turn into a job.
type candidate struct {
	label    string
	isVolume bool
	vol      detector.VolumeInfo
	host     detector.HostPathInfo
}

// Run drives the wizard against in/out (typically os.Stdin/os.Stdout).
func (w *Wizard) Run(ctx context.Context, in *bufio.Reader, out io.Writer) error {
	vols, err := w.Detector.ScanVolumes(ctx)
	if err != nil {
		return fmt.Errorf("scanning volumes: %w", err)
	}
	// Host-path (hostdir) discovery is disabled: this deployment backs up
	// Docker volumes/services only, never raw host filesystem paths.
	var hostPaths []detector.HostPathInfo

	var candidates []candidate
	for _, v := range vols {
		candidates = append(candidates, candidate{
			label:    fmt.Sprintf("%s  (%s, volume %s)", v.JobName, v.ServiceType, v.Name),
			isVolume: true,
			vol:      v,
		})
	}
	for _, h := range hostPaths {
		if h.JobName == "" {
			continue // env-file entries aren't jobbable
		}
		candidates = append(candidates, candidate{
			label: fmt.Sprintf("%s  (%s, %s)", h.JobName, h.Kind, h.Path),
			host:  h,
		})
	}
	if len(candidates) == 0 {
		fmt.Fprintln(out, "No volumes or host paths found to generate jobs for.")
		return nil
	}

	created := 0
	for {
		fmt.Fprintln(out)
		options := make([]string, 0, len(candidates)+1)
		options = append(options, "Finish")
		for _, c := range candidates {
			options = append(options, c.label)
		}
		idx, err := utils.SelectOne(out, in, "Select an item to create a job for:", options)
		if err != nil {
			return err
		}
		if idx == 0 {
			break
		}
		cand := candidates[idx-1]

		job, err := w.buildJob(ctx, cand)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			continue
		}

		fmt.Fprintf(out, "\nJob %q -> %s\n", job.Name, job.GoogleDrivePath)
		if err := w.PromptMissingCredentials(ctx, out, in, job); err != nil {
			return err
		}
		if err := promptJobOverrides(out, in, job); err != nil {
			return err
		}

		ok, err := utils.Confirm(out, in, fmt.Sprintf("Create job %q?", job.Name), true)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "Skipped.")
			continue
		}
		if err := w.Workspace.SaveJob(job); err != nil {
			return fmt.Errorf("saving job %q: %w", job.Name, err)
		}
		fmt.Fprintf(out, "Created job %q\n", job.Name)
		created++
	}

	fmt.Fprintf(out, "\n%d job(s) created under %s\n", created, w.Workspace.Root)
	return nil
}

func (w *Wizard) buildJob(ctx context.Context, cand candidate) (*backup.Job, error) {
	if cand.isVolume {
		return w.Generator.CreateFromVolume(ctx, cand.vol, true)
	}
	return w.Generator.CreateFromHostPath(ctx, cand.host, true)
}

// PromptMissingCredentials fills any of job.ServiceType's known
// credential keys (detector.CredentialEnvKeysFor) that are still empty in
// job.Credentials, then runs a lightweight connectivity probe against the
// container. This closes the gap where a container's real credentials
// aren't fully recoverable from its own environment variables (a secret
// mounted from a file, a *_PASSWORD never actually set as an env var,
// etc.) - without it, generate silently creates a job with partial
// credentials that only fails downstream, at backup time.
//
// A no-op for service types with no curated credential key list (n8n,
// rabbitmq, hostdir/standard, generic) and for jobs with no
// ContainerName (host-path jobs) - there's nothing to prompt for either
// way.
//
// Exported (rather than a package-private step of Run) so it's directly
// testable without needing a full Detector/scan seam - mirrors this
// project's precedent of exporting an otherwise-internal piece purely for
// testability (e.g. backup.RunAndRecord's sibling helpers, rclone.
// FilesToPrune).
func (w *Wizard) PromptMissingCredentials(ctx context.Context, out io.Writer, in *bufio.Reader, job *backup.Job) error {
	keys := detector.CredentialEnvKeysFor(job.ServiceType)
	if len(keys) == 0 || job.ContainerName == "" {
		return nil
	}
	if job.Credentials == nil {
		job.Credentials = map[string]string{}
	}

	missing := false
	for _, k := range keys {
		if job.Credentials[k] == "" {
			missing = true
			break
		}
	}
	if !missing {
		return nil
	}

	fmt.Fprintf(out, "\n%q is missing credentials that couldn't be auto-detected from the container's environment:\n", job.Name)
	for _, k := range keys {
		if job.Credentials[k] != "" {
			continue
		}
		val, err := promptString(out, in, "  "+k+" (blank to skip)", "")
		if err != nil {
			return err
		}
		if val != "" {
			job.Credentials[k] = val
		}
	}

	if w.Docker == nil {
		return nil
	}
	// Best-effort, not blocking: the container might legitimately not be
	// reachable yet, or the probe itself might not fit a nonstandard
	// image - a failure warns and still lets the job be saved, matching
	// this project's existing warn-then-proceed philosophy (e.g. a
	// missing integrity manifest at restore time).
	ok, msg := testConnectivity(ctx, w.Docker, job)
	switch {
	case msg == "":
	case ok:
		fmt.Fprintf(out, "  connectivity check: %s\n", msg)
	default:
		fmt.Fprintf(out, "  connectivity check: %s (job will still be saved)\n", msg)
	}
	return nil
}

// promptJobOverrides lets the user accept or change job's retention/
// min-files/webhook defaults, re-prompting on invalid input rather than
// failing the whole wizard over one bad answer.
func promptJobOverrides(out io.Writer, in *bufio.Reader, job *backup.Job) error {
	for {
		days, err := promptInt(out, in, "Retention days", job.RetentionDays)
		if err != nil {
			return err
		}
		if verr := utils.ValidateRetentionDays(days); verr != nil {
			fmt.Fprintln(out, verr)
			continue
		}
		job.RetentionDays = days
		break
	}
	for {
		n, err := promptInt(out, in, "Min files to keep", job.MinFilesToKeep)
		if err != nil {
			return err
		}
		if verr := utils.ValidateMinFilesToKeep(n); verr != nil {
			fmt.Fprintln(out, verr)
			continue
		}
		job.MinFilesToKeep = n
		break
	}
	for {
		n, err := promptInt(out, in, "Local backup copies to retain (0 disables)", job.LocalRetentionCount)
		if err != nil {
			return err
		}
		if verr := utils.ValidateLocalRetentionCount(n); verr != nil {
			fmt.Fprintln(out, verr)
			continue
		}
		job.LocalRetentionCount = n
		break
	}
	for {
		url, err := promptString(out, in, "Webhook URL (blank for none)", job.WebhookURL)
		if err != nil {
			return err
		}
		if verr := utils.ValidateWebhookURL(url); verr != nil {
			fmt.Fprintln(out, verr)
			continue
		}
		job.WebhookURL = url
		break
	}
	return nil
}

func promptInt(out io.Writer, in *bufio.Reader, label string, def int) (int, error) {
	fmt.Fprintf(out, "%s [%d]: ", label, def)
	line, err := in.ReadString('\n')
	if err != nil {
		return 0, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		fmt.Fprintf(out, "enter a whole number\n")
		return promptInt(out, in, label, def)
	}
	return n, nil
}

func promptString(out io.Writer, in *bufio.Reader, label, def string) (string, error) {
	fmt.Fprintf(out, "%s [%s]: ", label, def)
	line, err := in.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}
