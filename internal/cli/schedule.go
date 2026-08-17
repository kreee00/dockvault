package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dockvault/internal/scheduler"
	"dockvault/internal/workspace"
)

func init() {
	register(&command{
		name:    "schedule",
		summary: "Install/check/uninstall the daily automated backup",
		run:     runSchedule,
	})
}

// runSchedule calls scheduler.DetectPlatform() and dispatches three ways:
// a systemd timer on native Linux, a launchd user agent on native macOS,
// or printed Windows Task Scheduler instructions when dockvault is a
// native Windows binary running in PowerShell. All three ultimately just
// point at `dockvault backup --all` - there's no wrapper script.
func runSchedule(ctx context.Context, g Global, args []string) error {
	fs := flag.NewFlagSet("schedule", flag.ContinueOnError)
	install := fs.Bool("install", false, "install systemd timer / launchd job / print Windows Task Scheduler instructions")
	fs.BoolVar(install, "i", false, "shorthand for --install")
	check := fs.Bool("check", false, "check if the scheduled backup is active")
	fs.BoolVar(check, "c", false, "shorthand for --check")
	logs := fs.Bool("logs", false, "tail the most recent backup log")
	fs.BoolVar(logs, "l", false, "shorthand for --logs")
	uninstall := fs.Bool("uninstall", false, "remove the scheduled backup")
	at := fs.String("at", scheduler.DefaultTime, "daily fire time, 24-hour HH:MM (Linux/macOS/Windows-instructions only)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ws, err := workspace.Open(g.Home)
	if err != nil {
		return err
	}

	switch {
	case *install:
		return installSchedule(ctx, ws, *at)
	case *uninstall:
		return uninstallSchedule(ctx)
	case *check:
		return checkSchedule(ctx)
	case *logs:
		return tailScheduleLogs(ws)
	default:
		fmt.Println("Specify one of --install, --check, --logs, or --uninstall. See `dockvault schedule --help`.")
		return nil
	}
}

func installSchedule(ctx context.Context, ws *workspace.Workspace, at string) error {
	if err := ws.EnsureLayout(); err != nil {
		return err
	}

	switch scheduler.DetectPlatform() {
	case scheduler.PlatformLinux:
		if err := scheduler.InstallSystemdTimer(ctx, ws.Root, at); err != nil {
			return err
		}
		fmt.Printf("Installed and enabled dockvault-backup.timer (daily at %s).\n", at)
		return nil
	case scheduler.PlatformDarwin:
		if err := scheduler.InstallLaunchdJob(ctx, ws.Root, at); err != nil {
			return err
		}
		fmt.Printf("Installed and loaded the DockVault launchd job (daily at %s).\n", at)
		return nil
	case scheduler.PlatformWindows:
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolving dockvault binary path: %w", err)
		}
		fmt.Println(scheduler.NativeWindowsScheduledTaskInstructions(exePath, ws.Root, at))
		return nil
	default:
		return fmt.Errorf("schedule --install: unsupported platform")
	}
}

func uninstallSchedule(ctx context.Context) error {
	switch scheduler.DetectPlatform() {
	case scheduler.PlatformLinux:
		if err := scheduler.UninstallSystemdTimer(ctx); err != nil {
			return err
		}
		fmt.Println("Removed dockvault-backup.timer.")
		return nil
	case scheduler.PlatformDarwin:
		if err := scheduler.UninstallLaunchdJob(ctx); err != nil {
			return err
		}
		fmt.Println("Removed the DockVault launchd job.")
		return nil
	case scheduler.PlatformWindows:
		fmt.Println(`Remove it manually in PowerShell (as Administrator):`)
		fmt.Println(`Unregister-ScheduledTask -TaskName "DockVault-Backup" -Confirm:$false`)
		return nil
	default:
		return fmt.Errorf("schedule --uninstall: unsupported platform")
	}
}

func checkSchedule(ctx context.Context) error {
	var out string
	var err error
	switch scheduler.DetectPlatform() {
	case scheduler.PlatformLinux:
		out, err = scheduler.CheckSystemdTimer(ctx)
	case scheduler.PlatformDarwin:
		out, err = scheduler.CheckLaunchdJob(ctx)
	case scheduler.PlatformWindows:
		fmt.Println(`Check manually in PowerShell:`)
		fmt.Println(`Get-ScheduledTask -TaskName "DockVault-Backup" | Get-ScheduledTaskInfo`)
		return nil
	default:
		return fmt.Errorf("schedule --check: unsupported platform")
	}
	if strings.TrimSpace(out) != "" {
		fmt.Println(out)
	}
	return err
}

// tailScheduleLogs prints the last 50 lines of the most recently modified
// log under logs/ (there's one per day, master_YYYY-MM-DD.log).
func tailScheduleLogs(ws *workspace.Workspace) error {
	entries, err := os.ReadDir(ws.LogsDir())
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No logs yet - run a backup first.")
			return nil
		}
		return err
	}

	var latestName string
	var latestMod time.Time
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestName = e.Name()
		}
	}
	if latestName == "" {
		fmt.Println("No logs yet - run a backup first.")
		return nil
	}

	path := filepath.Join(ws.LogsDir(), latestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	const maxLines = 50
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start := 0
	if len(lines) > maxLines {
		start = len(lines) - maxLines
	}

	fmt.Printf("== %s ==\n", path)
	for _, line := range lines[start:] {
		fmt.Println(line)
	}
	return nil
}
