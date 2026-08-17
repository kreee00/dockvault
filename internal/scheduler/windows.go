package scheduler

import "fmt"

// NativeWindowsScheduledTaskInstructions returns the PowerShell snippet
// `schedule --install` prints when dockvault is a native Windows binary
// (GOOS=windows) running in PowerShell. It invokes dockvault.exe directly
// with no bash wrapper - dockvaultExePath and dockvaultHome are plain
// Windows paths (e.g. "C:\Program Files\dockvault\dockvault.exe",
// "C:\dockvault_scripts"). at is a 24-hour "HH:MM" (DefaultTime if
// empty), converted to the 12-hour form PowerShell's -At expects.
func NativeWindowsScheduledTaskInstructions(dockvaultExePath, dockvaultHome, at string) string {
	if at == "" {
		at = DefaultTime
	}
	return fmt.Sprintf(`# Run in PowerShell (as Administrator):
$action = New-ScheduledTaskAction -Execute "%s" `+"`"+`
  -Argument "backup --all --home `+"`"+`"%s`+"`"+`"" `+"`"+`
  -WorkingDirectory "%s"
$trigger = New-ScheduledTaskTrigger -Daily -At %s
Register-ScheduledTask -TaskName "DockVault-Backup" -Action $action -Trigger $trigger -RunLevel Highest

# Logs land in %s\logs\ - dockvault writes there itself, there's no
# separate redirect to wire up (unlike bash-based scripts).
`, dockvaultExePath, dockvaultHome, dockvaultHome, to12Hour(at), dockvaultHome)
}

// to12Hour converts a 24-hour "HH:MM" into the "H:MMAM"/"H:MMPM" form
// PowerShell's -At parameter accepts. Falls back to returning hhmm
// unchanged if it doesn't parse, rather than failing outright - these are
// instructions for the user to review, not something dockvault executes
// itself.
func to12Hour(hhmm string) string {
	var hour, minute int
	if _, err := fmt.Sscanf(hhmm, "%d:%d", &hour, &minute); err != nil {
		return hhmm
	}
	suffix := "AM"
	displayHour := hour
	switch {
	case hour == 0:
		displayHour = 12
	case hour == 12:
		suffix = "PM"
	case hour > 12:
		displayHour = hour - 12
		suffix = "PM"
	}
	return fmt.Sprintf("%d:%02d%s", displayHour, minute, suffix)
}
