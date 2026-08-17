package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"

	"dockvault/internal/backup"
	"dockvault/internal/detector"
	"dockvault/internal/docker"
	"dockvault/internal/generator"
	"dockvault/internal/utils"
	"dockvault/internal/workspace"
)

func init() {
	register(&command{
		name:    "generate",
		summary: "Create backup job pairs from detected volumes",
		run:     runGenerate,
	})
}

// runGenerate drives internal/generator to turn detector.VolumeInfo/
// HostPathInfo results into job directories (job metadata + .env), per
// the spec's --auto and --interactive behaviors. Mirrors cli/scan.go's
// precedent of treating --auto as the (currently only) non-interactive
// path, accepted for spec compatibility.
func runGenerate(ctx context.Context, g Global, args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.Bool("auto", false, "auto-detect all volumes, generate jobs without confirmation (unless --confirm)")
	interactive := fs.Bool("interactive", false, "wizard mode: prompt for each field")
	dryRun := fs.Bool("dry-run", g.DryRun, "print what would be created instead of writing files")
	confirm := fs.Bool("confirm", false, "auto-confirm all prompts (use with --auto)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ws, err := workspace.Open(g.Home)
	if err != nil {
		return err
	}
	if err := ws.EnsureLayout(); err != nil {
		return err
	}
	// Prevent two dockvault invocations from writing job.json/.env for
	// the same workspace concurrently - see workspace.Lock's doc comment.
	unlock, err := ws.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	dc := docker.New()
	if err := dc.Ping(ctx); err != nil {
		return fmt.Errorf("%w\n  hint: is Docker running? (Docker Desktop, or `sudo systemctl start docker`)", err)
	}
	det := detector.New(dc, g.Environment)

	if *interactive {
		wiz := generator.NewWizard(ws, det, dc)
		return wiz.Run(ctx, bufio.NewReader(os.Stdin), os.Stdout)
	}

	vols, err := det.ScanVolumes(ctx)
	if err != nil {
		return err
	}
	// Host-path (hostdir) discovery is disabled: this deployment backs up
	// Docker volumes/services only, never raw host filesystem paths.
	var jobbableHostPaths []detector.HostPathInfo

	total := len(vols) + len(jobbableHostPaths)
	if total == 0 {
		fmt.Println("No volumes or host paths found to generate jobs for.")
		return nil
	}

	dryRunEffective := *dryRun || g.DryRun
	if !dryRunEffective && !*confirm {
		ok, err := utils.Confirm(os.Stdout, bufio.NewReader(os.Stdin), fmt.Sprintf("Create %d backup job(s)?", total), true)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Aborted.")
			return nil
		}
	}

	gen := generator.New(ws)
	created := 0
	var incomplete []*backup.Job
	for _, v := range vols {
		job, err := gen.CreateFromVolume(ctx, v, dryRunEffective)
		if err != nil {
			return err
		}
		printGeneratedJob(job, dryRunEffective)
		if missingCredentials(job) {
			incomplete = append(incomplete, job)
		}
		created++
	}
	for _, h := range jobbableHostPaths {
		job, err := gen.CreateFromHostPath(ctx, h, dryRunEffective)
		if err != nil {
			return err
		}
		printGeneratedJob(job, dryRunEffective)
		created++
	}

	if !dryRunEffective {
		fmt.Printf("\n%d job(s) created under %s\n", created, ws.Root)
	}
	// --auto is deliberately non-interactive/scriptable, so a missing
	// credential never blocks here the way generate --interactive's
	// promptMissingCredentials does - just a summary pointing at how to
	// fix it, since the alternative (silence) only surfaces as a
	// downstream backup failure with no context.
	if len(incomplete) > 0 {
		fmt.Printf("\n%d job(s) may be missing credentials that couldn't be auto-detected from their container's environment:\n", len(incomplete))
		for _, job := range incomplete {
			fmt.Printf("  - %s\n", job.Name)
		}
		fmt.Println("  fix: edit the job's .env directly, or re-run with `generate --interactive` to be prompted")
	}
	return nil
}

// missingCredentials reports whether any of job.ServiceType's known
// credential keys (detector.CredentialEnvKeysFor) are empty in
// job.Credentials - used only for --auto's post-run summary; --interactive
// prompts for these directly instead (see generator.Wizard.
// promptMissingCredentials).
func missingCredentials(job *backup.Job) bool {
	for _, k := range detector.CredentialEnvKeysFor(job.ServiceType) {
		if job.Credentials[k] == "" {
			return true
		}
	}
	return false
}

func printGeneratedJob(job *backup.Job, dryRun bool) {
	verb := "Created"
	if dryRun {
		verb = "[dry-run] would create"
	}
	fmt.Printf("%s job %q (%s) -> %s\n", verb, job.Name, job.BackupType, job.GoogleDrivePath)
}
