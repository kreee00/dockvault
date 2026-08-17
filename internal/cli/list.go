package cli

import (
	"context"
	"flag"
	"fmt"

	"dockvault/internal/workspace"
)

func init() {
	register(&command{
		name:    "list",
		summary: "Show the local $DOCKVAULT_HOME workspace as a tree",
		run:     runList,
	})
}

// runList walks workspace.Home() and renders it as an ASCII tree of job
// directories, optionally with each job's last-backup time/status from
// manifest.json (--detailed).
func runList(ctx context.Context, g Global, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.Bool("tree", true, "ASCII tree view (default)")
	detailed := fs.Bool("detailed", false, "include file sizes, modification times")
	fs.BoolVar(detailed, "d", false, "shorthand for --detailed")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ws, err := workspace.Open(g.Home)
	if err != nil {
		return err
	}
	if !ws.Exists() {
		fmt.Printf("No workspace found at %s\n", ws.Root)
		return nil
	}

	names, err := ws.ListJobNames()
	if err != nil {
		return err
	}
	fmt.Println(ws.Root)
	if len(names) == 0 {
		fmt.Println("  (no jobs - run `dockvault generate` first)")
		return nil
	}

	var manifest workspace.Manifest
	if *detailed {
		if manifest, err = ws.LoadManifest(); err != nil {
			return err
		}
	}

	for i, name := range names {
		last := i == len(names)-1
		branch, detailPrefix := "├── ", "│   "
		if last {
			branch, detailPrefix = "└── ", "    "
		}

		job, err := ws.LoadJob(name)
		if err != nil {
			fmt.Printf("%s%s  (error loading: %v)\n", branch, name, err)
			continue
		}
		fmt.Printf("%s%s  (%s -> %s)\n", branch, name, job.BackupType, job.GoogleDrivePath)

		if !*detailed {
			continue
		}
		entry := findManifestEntry(manifest, name)
		if entry == nil || entry.LastBackup == nil {
			fmt.Printf("%s  last backup: never\n", detailPrefix)
			continue
		}
		fmt.Printf("%s  last backup: %s (%s)\n", detailPrefix, entry.LastBackup.Format("2006-01-02 15:04:05 MST"), entry.LastBackupStatus)
	}
	return nil
}

func findManifestEntry(m workspace.Manifest, jobName string) *workspace.ManifestEntry {
	for i := range m.Jobs {
		if m.Jobs[i].JobName == jobName {
			return &m.Jobs[i]
		}
	}
	return nil
}
