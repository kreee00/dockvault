// Command dockvault is a Docker volume backup orchestration tool.
//
// It discovers Docker volumes, containers, and host paths, infers the
// service running on top of them (PostgreSQL, MySQL, MongoDB, Redis, n8n,
// or a generic volume/host directory), and backs them up - and restores
// them - to/from Google Drive via rclone. dockvault executes backups and
// restores directly; it does not generate scripts for cron/systemd to run.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"dockvault/internal/cli"
)

func main() {
	// Cancelling on SIGINT/SIGTERM lets in-flight `docker`/`rclone` child
	// processes (started via exec.CommandContext) get killed cleanly on
	// Ctrl+C instead of continuing to run - e.g. an in-progress `docker
	// run ... tar` - after dockvault itself has exited.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "dockvault: %v\n", err)
		os.Exit(1)
	}
}
