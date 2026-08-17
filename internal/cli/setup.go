package cli

import (
	"bufio"
	"context"
	"flag"
	"os"

	"dockvault/internal/setup"
	"dockvault/internal/workspace"
)

func init() {
	register(&command{
		name:    "setup",
		summary: "Guided one-time setup: GCP service account, rclone remote, config defaults",
		run:     runSetup,
	})
}

// runSetup drives internal/setup's Wizard, mirroring runGenerate's
// bootstrapping sequence (workspace.Open -> EnsureLayout -> Lock) minus
// the Docker daemon check - setup doesn't touch containers at all.
func runSetup(ctx context.Context, g Global, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
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
	unlock, err := ws.Lock()
	if err != nil {
		return err
	}
	defer unlock()

	wiz := setup.NewWizard(ws)
	return wiz.Run(ctx, bufio.NewReader(os.Stdin), os.Stdout)
}
