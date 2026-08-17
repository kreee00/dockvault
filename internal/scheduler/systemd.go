// Package scheduler manages automated daily backups: a systemd timer on
// native Linux, a launchd job on macOS, or Windows Task Scheduler
// instructions when running natively on Windows (GOOS=windows in
// PowerShell).
package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

const (
	systemdServicePath = "/etc/systemd/system/dockvault-backup.service"
	systemdTimerPath   = "/etc/systemd/system/dockvault-backup.timer"
	systemdUnitName    = "dockvault-backup.timer"
	// DefaultTime is the daily fire time for the timer, per spec ("Daily
	// at 01:00 by default").
	DefaultTime = "01:00"
)

// Platform identifies which scheduling system applies.
type Platform int

const (
	// PlatformLinux is native Linux - use a systemd timer.
	PlatformLinux Platform = iota
	// PlatformWindows is a native Windows binary running in PowerShell
	// (GOOS=windows) - use Windows Task Scheduler.
	PlatformWindows
	// PlatformDarwin is native macOS - use a launchd user agent.
	PlatformDarwin
)

// DetectPlatform picks the right scheduling system for the current process.
func DetectPlatform() Platform {
	switch runtime.GOOS {
	case "windows":
		return PlatformWindows
	case "darwin":
		return PlatformDarwin
	default:
		return PlatformLinux
	}
}

const systemdServiceTemplate = `[Unit]
Description=DockVault automated backup
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=oneshot
User=%s
ExecStart=%s --home %s backup --all
`

const systemdTimerTemplate = `[Unit]
Description=Run DockVault backup daily

[Timer]
OnCalendar=*-*-* %s:00
Persistent=true

[Install]
WantedBy=timers.target
`

// InstallSystemdTimer renders and writes the .service/.timer unit files -
// the service runs as $SUDO_USER (falling back to the current user), not
// root, per spec, even though writing the unit files themselves under
// /etc/systemd/system typically requires root - then runs
// `systemctl daemon-reload` and `systemctl enable --now` the timer.
func InstallSystemdTimer(ctx context.Context, dockvaultHome string, at string) error {
	if at == "" {
		at = DefaultTime
	}
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving dockvault binary path: %w", err)
	}

	serviceContent := fmt.Sprintf(systemdServiceTemplate, targetUser(), exePath, dockvaultHome)
	timerContent := fmt.Sprintf(systemdTimerTemplate, at)

	if err := os.WriteFile(systemdServicePath, []byte(serviceContent), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w\n  hint: this needs root - try `sudo dockvault schedule --install`", systemdServicePath, err)
	}
	if err := os.WriteFile(systemdTimerPath, []byte(timerContent), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", systemdTimerPath, err)
	}
	if _, err := runCmd(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := runCmd(ctx, "systemctl", "enable", "--now", systemdUnitName); err != nil {
		return err
	}
	return nil
}

// UninstallSystemdTimer disables the timer and removes both unit files.
func UninstallSystemdTimer(ctx context.Context) error {
	// Best-effort: an already-uninstalled timer shouldn't block cleanup
	// of whatever unit files remain.
	_, _ = runCmd(ctx, "systemctl", "disable", "--now", systemdUnitName)

	for _, p := range []string{systemdTimerPath, systemdServicePath} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s: %w\n  hint: this needs root - try `sudo dockvault schedule --uninstall`", p, err)
		}
	}
	_, err := runCmd(ctx, "systemctl", "daemon-reload")
	return err
}

// CheckSystemdTimer reports status via `systemctl status
// dockvault-backup.timer`. The raw output is returned even when
// systemctl exits non-zero (e.g. "inactive (dead)" is a valid, useful
// status, not a dockvault-level error), so callers should print it
// regardless of whether err is also set.
func CheckSystemdTimer(ctx context.Context) (string, error) {
	return runCmd(ctx, "systemctl", "status", systemdUnitName)
}

// targetUser returns the user the backup service should run as: the
// user who invoked sudo, if any (so an install run as `sudo dockvault
// schedule --install` still runs backups as the real user, not root),
// falling back to the current user.
func targetUser() string {
	if u := os.Getenv("SUDO_USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "root"
}

// runCmd shells out to name with args, returning captured stdout - even
// on a non-zero exit, since some callers (CheckSystemdTimer,
// CheckLaunchdJob) still want the output.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}
