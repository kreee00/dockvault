package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// launchdLabel identifies the user agent to launchd.
const launchdLabel = "com.dockvault.backup"

const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>--home</string>
		<string>%s</string>
		<string>backup</string>
		<string>--all</string>
	</array>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Hour</key>
		<integer>%d</integer>
		<key>Minute</key>
		<integer>%d</integer>
	</dict>
	<key>StandardOutPath</key>
	<string>%s/logs/launchd.log</string>
	<key>StandardErrorPath</key>
	<string>%s/logs/launchd.log</string>
</dict>
</plist>
`

func launchdPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

// InstallLaunchdJob renders and writes a launchd user-agent plist under
// ~/Library/LaunchAgents (no root needed, unlike the systemd path - a
// launchd user agent installs and runs entirely as the invoking user),
// then `launchctl load`s it.
func InstallLaunchdJob(ctx context.Context, dockvaultHome, at string) error {
	if at == "" {
		at = DefaultTime
	}
	hour, minute, err := parseHHMM(at)
	if err != nil {
		return err
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving dockvault binary path: %w", err)
	}

	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(plistPath), err)
	}

	content := fmt.Sprintf(launchdPlistTemplate, launchdLabel, exePath, dockvaultHome, hour, minute, dockvaultHome, dockvaultHome)
	if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", plistPath, err)
	}

	if _, err := runCmd(ctx, "launchctl", "load", "-w", plistPath); err != nil {
		return err
	}
	return nil
}

// UninstallLaunchdJob unloads and removes the plist.
func UninstallLaunchdJob(ctx context.Context) error {
	plistPath, err := launchdPlistPath()
	if err != nil {
		return err
	}
	// Best-effort: an already-unloaded job shouldn't block removing the
	// plist file itself.
	_, _ = runCmd(ctx, "launchctl", "unload", plistPath)

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing %s: %w", plistPath, err)
	}
	return nil
}

// CheckLaunchdJob reports status via `launchctl list <label>`.
func CheckLaunchdJob(ctx context.Context) (string, error) {
	return runCmd(ctx, "launchctl", "list", launchdLabel)
}

func parseHHMM(at string) (hour, minute int, err error) {
	if _, err := fmt.Sscanf(at, "%d:%d", &hour, &minute); err != nil {
		return 0, 0, fmt.Errorf("invalid time %q, want HH:MM: %w", at, err)
	}
	return hour, minute, nil
}
