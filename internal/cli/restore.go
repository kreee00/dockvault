package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"

	"dockvault/internal/backup"
	"dockvault/internal/docker"
	"dockvault/internal/rclone"
	"dockvault/internal/restore"
	"dockvault/internal/utils"
	"dockvault/internal/workspace"
)

func init() {
	register(&command{
		name:    "restore",
		summary: "Interactive restore from Google Drive backups",
		run:     runRestore,
	})
}

// runRestore lists jobs, lets the user pick a job (or takes --job) and a
// remote backup file (newest first), requires literally typing RESTORE to
// confirm, runs the matching internal/restore executor, then - per the
// resolved design decision on post-restore container state - prompts
// whether to start the container back up rather than doing it silently
// (only relevant for restore types that stop the container at all; see
// restore.StopsContainerDuringRestore).
func runRestore(ctx context.Context, g Global, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	jobFlag := fs.String("job", "", "restore a specific job (skip job selection)")
	dryRun := fs.Bool("dry-run", g.DryRun, "show steps without executing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ws, err := workspace.Open(g.Home)
	if err != nil {
		return err
	}
	if !ws.Exists() {
		return fmt.Errorf("no workspace found at %s\n  hint: run `dockvault generate` first", ws.Root)
	}
	// Prevent two dockvault invocations from touching the same workspace
	// (or, worse, the same Docker volume/container) concurrently - see
	// workspace.Lock's doc comment. This matters even more for restore
	// than backup: two overlapping restores against the same volume could
	// corrupt it far more seriously than two overlapping reads ever could.
	unlock, err := ws.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	in := bufio.NewReader(os.Stdin)

	jobName := *jobFlag
	if jobName == "" {
		names, err := ws.ListJobNames()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Println("No jobs found. Run `dockvault generate` first.")
			return nil
		}
		idx, err := utils.SelectOne(os.Stdout, in, "Select a job to restore:", names)
		if err != nil {
			return err
		}
		jobName = names[idx]
	}

	job, err := ws.LoadJob(jobName)
	if err != nil {
		return err
	}

	cfg, err := ws.LoadConfig()
	if err != nil {
		return err
	}
	dc := docker.New()
	if err := dc.Ping(ctx); err != nil {
		return fmt.Errorf("%w\n  hint: is Docker running? (Docker Desktop, or `sudo systemctl start docker`)", err)
	}
	rc := rclone.New(resolveRcloneRemote(g, cfg))
	if err := rc.CheckConfigured(ctx); err != nil {
		return err
	}
	registry := buildRestoreRegistry(dc, rc)

	exec, ok := registry[job.BackupType]
	if !ok {
		return fmt.Errorf("no restore executor registered for backup type %q", job.BackupType)
	}

	files, err := exec.ListRemoteBackups(ctx, job)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Printf("No backups found for job %q at %s\n", job.Name, job.GoogleDrivePath)
		return nil
	}

	labels := make([]string, len(files))
	for i, f := range files {
		labels[i] = fmt.Sprintf("%s  (%s, %s)", f.Name, backup.FormatBytes(f.Size), f.Modified.Format("2006-01-02 15:04:05 MST"))
	}
	idx, err := utils.SelectOne(os.Stdout, in, fmt.Sprintf("Select a backup to restore for job %q (newest first):", job.Name), labels)
	if err != nil {
		return err
	}
	file := files[idx]

	if *dryRun || g.DryRun {
		fmt.Printf("[dry-run] would restore %q from %s\n", job.Name, file.Path)
		return nil
	}

	fmt.Printf("\nThis will overwrite %q's current data with %s.\n", job.Name, file.Name)
	confirmed, err := utils.RequireTypedConfirmation(os.Stdout, in, "RESTORE")
	if err != nil {
		return err
	}
	if !confirmed {
		fmt.Println(`Confirmation did not match "RESTORE" - aborted.`)
		return nil
	}

	fmt.Printf("Restoring %q from %s ...\n", job.Name, file.Name)
	warning, err := exec.Restore(ctx, job, file)
	if err != nil {
		return fmt.Errorf("restore failed: %w", err)
	}
	if warning != "" {
		fmt.Printf("Warning: %s\n", warning)
	}
	fmt.Println("Restore complete.")

	if !restore.StopsContainerDuringRestore(job.BackupType) {
		return nil
	}
	return maybeRestartContainer(ctx, dc, in, job)
}

// maybeRestartContainer never restarts a container without asking - see
// runRestore's doc comment and DOCKVAULT_CONTEXT.md's resolved design
// decision on post-restore container state.
func maybeRestartContainer(ctx context.Context, dc *docker.Client, in *bufio.Reader, job *backup.Job) error {
	if job.ContainerName == "" {
		return nil
	}
	ok, err := utils.Confirm(os.Stdout, in, fmt.Sprintf("Start container %q back up?", job.ContainerName), false)
	if err != nil {
		// Can't read an answer (e.g. non-interactive stdin) - leave it
		// stopped rather than guessing.
		fmt.Printf("Leaving container %q stopped (couldn't read a confirmation: %v)\n", job.ContainerName, err)
		return nil
	}
	if !ok {
		fmt.Printf("Leaving container %q stopped.\n", job.ContainerName)
		return nil
	}
	if err := dc.Start(ctx, job.ContainerName); err != nil {
		return fmt.Errorf("starting container %q: %w", job.ContainerName, err)
	}
	fmt.Printf("Started container %q\n", job.ContainerName)
	return nil
}
