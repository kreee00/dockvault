package cli

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dockvault/internal/backup"
	"dockvault/internal/docker"
	"dockvault/internal/logger"
	"dockvault/internal/rclone"
	"dockvault/internal/workspace"
)

func init() {
	register(&command{
		name:    "backup",
		summary: "Execute backup jobs",
		run:     runBackup,
	})
}

// runBackup selects jobs under workspace.Home() (by --job/--jobs/--all/
// --auto), runs each through its internal/backup executor with bounded
// parallelism (--parallel), logs to logs/master_YYYY-MM-DD.log, records
// results in manifest.json, and fires webhook notifications per job (each
// executor does that itself - see backup.RunAndRecord).
func runBackup(ctx context.Context, g Global, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.Bool("auto", false, "detect all volumes and backup only those with generated jobs (currently equivalent to --all - see doc comment)")
	fs.Bool("all", false, "run all existing jobs under $DOCKVAULT_HOME (default if no job specified)")
	jobFlag := fs.String("job", "", "run a single job by name")
	jobsFlag := fs.String("jobs", "", "run multiple jobs, comma-separated")
	dryRun := fs.Bool("dry-run", g.DryRun, "show what would run, don't execute")
	parallel := fs.Int("parallel", 3, "run up to N jobs in parallel")
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
	// Prevent two `dockvault backup`/`restore`/`generate` invocations from
	// running against the same workspace concurrently - see
	// workspace.Lock's doc comment for the races this closes (manifest.json
	// lost updates, two runs touching the same volume/container at once).
	unlock, err := ws.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	names, err := selectJobNames(ws, *jobFlag, *jobsFlag)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("No jobs found. Run `dockvault generate` first.")
		return nil
	}

	jobs := make([]*backup.Job, 0, len(names))
	for _, name := range names {
		job, err := ws.LoadJob(name)
		if err != nil {
			return err
		}
		jobs = append(jobs, job)
	}

	dryRunEffective := *dryRun || g.DryRun
	if dryRunEffective {
		for _, job := range jobs {
			fmt.Printf("[dry-run] would run job %q (%s) -> %s\n", job.Name, job.BackupType, job.GoogleDrivePath)
		}
		return nil
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
	registry := buildBackupRegistry(dc, rc)

	// Pre-create every job's destination directory sequentially, before
	// any parallel job execution starts. Google Drive has no atomic
	// "create directory if missing" - if this were left to each job's
	// own `rclone copy` racing concurrently, jobs sharing an ancestor
	// directory (e.g. the first backups of the day, all needing
	// /stage/<host>/ for the first time) can each create their own
	// duplicate copy of it, silently breaking path-based lookups like the
	// post-upload hash verification. See rclone.Client.Mkdir's doc
	// comment for how this was found (real Shared Drive testing, not
	// mocks).
	for _, dir := range backup.UniqueRemoteDirs(jobs) {
		if err := rc.Mkdir(ctx, dir); err != nil {
			return err
		}
	}

	logPath := filepath.Join(ws.LogsDir(), "master_"+time.Now().UTC().Format("2006-01-02")+".log")
	lg, err := logger.New(logPath)
	if err != nil {
		return err
	}
	lg.Verbose = g.Verbose

	n := *parallel
	if n < 1 {
		n = 1
	}

	results := runJobs(ctx, registry, jobs, n, lg)

	if err := recordResults(ws, results, lg); err != nil {
		lg.Warn("saving manifest: %v", err)
	}

	return printBackupSummary(results)
}

// selectJobNames resolves which jobs to run per --job/--jobs, defaulting
// to every job under the workspace when neither is given (matching the
// spec's "--all (default if no job specified)"). --auto and --all both
// land here too - see runBackup's --auto flag doc comment.
func selectJobNames(ws *workspace.Workspace, jobFlag, jobsFlag string) ([]string, error) {
	switch {
	case jobFlag != "":
		return []string{jobFlag}, nil
	case jobsFlag != "":
		parts := strings.Split(jobsFlag, ",")
		names := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				names = append(names, p)
			}
		}
		return names, nil
	default:
		return ws.ListJobNames()
	}
}

type jobResult struct {
	job     *backup.Job
	err     error
	elapsed time.Duration
}

// runJobs runs jobs through registry with up to parallel running
// concurrently, logging each start/finish, and returns one result per
// job (same order as jobs).
func runJobs(ctx context.Context, registry backup.Registry, jobs []*backup.Job, parallel int, lg *logger.Logger) []jobResult {
	results := make([]jobResult, len(jobs))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup

	for i, job := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, job *backup.Job) {
			defer wg.Done()
			defer func() { <-sem }()

			start := time.Now()
			exec, ok := registry[job.BackupType]
			var runErr error
			if !ok {
				runErr = fmt.Errorf("no executor registered for backup type %q", job.BackupType)
			} else {
				lg.Info("%s: starting backup", job.Name)
				runErr = exec.Backup(ctx, job)
			}
			elapsed := time.Since(start)

			if runErr != nil {
				lg.Error("%s: FAILED: %v", job.Name, runErr)
			} else {
				lg.Success("%s: %s (%s)", job.Name, job.LastBackupStatus, elapsed.Round(time.Millisecond))
			}
			results[i] = jobResult{job: job, err: runErr, elapsed: elapsed}
		}(i, job)
	}
	wg.Wait()
	return results
}

// recordResults upserts every job's outcome into manifest.json.
func recordResults(ws *workspace.Workspace, results []jobResult, lg *logger.Logger) error {
	manifest, err := ws.LoadManifest()
	if err != nil {
		return err
	}
	for _, r := range results {
		manifest.Upsert(workspace.ManifestEntry{
			JobName:          r.job.Name,
			ServiceType:      r.job.ServiceType,
			LastBackup:       r.job.LastBackup,
			LastBackupStatus: r.job.LastBackupStatus,
		})
	}
	return ws.SaveManifest(manifest)
}

// printBackupSummary prints the "X/Y jobs succeeded" line and returns a
// non-nil error (so the process exits non-zero) if any job failed.
func printBackupSummary(results []jobResult) error {
	succeeded := 0
	for _, r := range results {
		if r.err == nil {
			succeeded++
		}
	}
	if succeeded == len(results) {
		fmt.Printf("\n✓ %d/%d job(s) succeeded\n", succeeded, len(results))
		return nil
	}
	fmt.Printf("\n✗ %d/%d job(s) succeeded, %d failed\n", succeeded, len(results), len(results)-succeeded)
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("  - %s: %v\n", r.job.Name, r.err)
		}
	}
	return fmt.Errorf("%d of %d backup jobs failed", len(results)-succeeded, len(results))
}
