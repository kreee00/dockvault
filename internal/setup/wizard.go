package setup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"dockvault/internal/rclone"
	"dockvault/internal/utils"
	"dockvault/internal/workspace"
)

// RcloneClient is the subset of *rclone.Client the wizard needs, narrowed
// for testability - same pattern as internal/backup.RcloneClient. Both
// methods take the remote name as an explicit parameter rather than using
// a bound Client.Remote, since the wizard operates on whatever remote
// name the user is in the middle of choosing, not a fixed one.
type RcloneClient interface {
	ConfigureServiceAccountRemote(ctx context.Context, name, serviceAccountFile, teamDriveID string) error
	ListSharedDrives(ctx context.Context, remoteName string) ([]rclone.SharedDrive, error)
}

// Wizard drives `dockvault setup`: guided, interactive one-time
// environment bootstrap. See this package's doc comment for what it does
// and doesn't cover.
type Wizard struct {
	Workspace *workspace.Workspace
	Rclone    RcloneClient
}

// NewWizard returns a Wizard bound to ws, using a real *rclone.Client for
// remote operations. The Client's own bound Remote is irrelevant here
// (see RcloneClient's doc comment), so it's constructed with an empty one.
func NewWizard(ws *workspace.Workspace) *Wizard {
	return &Wizard{Workspace: ws, Rclone: rclone.New("")}
}

// Run drives the full setup flow against in/out (typically os.Stdin/
// os.Stdout): Google service account -> rclone remote -> config.json
// defaults.
func (w *Wizard) Run(ctx context.Context, in *bufio.Reader, out io.Writer) error {
	fmt.Fprintln(out, "DockVault Setup")
	fmt.Fprintln(out, "===============")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "This won't create a GCP service account for you - that needs GCP Console/OAuth")
	fmt.Fprintln(out, "access dockvault shouldn't hold. It will validate one you've already downloaded,")
	fmt.Fprintln(out, "wire it into an rclone remote, and confirm it can actually reach your Shared Drive.")
	fmt.Fprintln(out)

	saPath, err := w.setupServiceAccount(in, out)
	if err != nil {
		return err
	}

	remoteName, err := w.setupRcloneRemote(ctx, in, out, saPath)
	if err != nil {
		return err
	}

	if err := w.setupConfigDefaults(in, out, remoteName); err != nil {
		return err
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Setup complete. Next: `dockvault generate --interactive` (or --auto) to create backup jobs.")
	return nil
}

// setupServiceAccount prompts for a local GCP service account JSON path,
// validates it, and copies it into $DOCKVAULT_HOME/secrets/ - returning
// the copied path (not the original), since that's what gets wired into
// the rclone remote.
func (w *Wizard) setupServiceAccount(in *bufio.Reader, out io.Writer) (string, error) {
	fmt.Fprintln(out, "Step 1: Google service account")
	for {
		path, err := promptString(out, in, "Path to your GCP service account JSON key", "")
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(path) == "" {
			fmt.Fprintln(out, "a path is required")
			continue
		}

		projectID, clientEmail, verr := ValidateServiceAccountJSON(path)
		if verr != nil {
			fmt.Fprintln(out, verr)
			continue
		}
		fmt.Fprintf(out, "  project:          %s\n", projectID)
		fmt.Fprintf(out, "  service account:  %s\n", clientEmail)
		ok, err := utils.Confirm(out, in, "Use this service account?", true)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}

		dest, err := w.copyServiceAccountFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(out, "Saved to %s (0600)\n\n", dest)
		return dest, nil
	}
}

// copyServiceAccountFile copies srcPath into
// $DOCKVAULT_HOME/secrets/gcp-service-account.json (mode 0600) rather
// than referencing srcPath in place - srcPath is typically a Downloads
// folder or similar with weaker ambient permissions than the workspace's
// own secrets directory.
func (w *Wizard) copyServiceAccountFile(srcPath string) (string, error) {
	secretsDir := filepath.Join(w.Workspace.Root, "secrets")
	if err := os.MkdirAll(secretsDir, 0o700); err != nil {
		return "", fmt.Errorf("creating secrets directory: %w", err)
	}
	_ = os.Chmod(secretsDir, 0o700)

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", srcPath, err)
	}
	dest := filepath.Join(secretsDir, "gcp-service-account.json")
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", dest, err)
	}
	tightenACLsBestEffort(dest)
	return dest, nil
}

// tightenACLsBestEffort further restricts path's Windows ACLs via
// `icacls` (an OS-bundled binary, not new syscall surface) - os.Chmod on
// Windows only toggles the read-only attribute, not real ACLs, so the
// 0600 passed to os.WriteFile above doesn't actually restrict access
// there the way it does on Linux/macOS. Best-effort and silent on
// failure, matching workspace.EnsureLayout's existing best-effort-chmod
// convention - a no-op on any non-Windows GOOS.
func tightenACLsBestEffort(path string) {
	if runtime.GOOS != "windows" {
		return
	}
	user := os.Getenv("USERNAME")
	if user == "" {
		return
	}
	_ = exec.Command("icacls", path, "/inheritance:r", "/grant:r", user+":F").Run()
}

// setupRcloneRemote prompts for a remote name, configures it against
// saPath, lists the Shared Drives that service account can actually see
// (the real proof the credentials work, not just that the JSON parsed),
// lets the user pick one, then finalizes the remote against that choice.
func (w *Wizard) setupRcloneRemote(ctx context.Context, in *bufio.Reader, out io.Writer, saPath string) (string, error) {
	fmt.Fprintln(out, "Step 2: rclone remote")
	remoteName, err := promptString(out, in, "rclone remote name", "dockvault_backup")
	if err != nil {
		return "", err
	}
	if remoteName == "" {
		remoteName = "dockvault_backup"
	}

	// Configure without a Shared Drive ID yet - `rclone backend drives`
	// works fine against a remote with no team_drive set (confirmed for
	// real against this project's actual Shared Drive setup), which is
	// exactly what's needed to list the available drives before the user
	// has to already know an ID.
	if err := w.Rclone.ConfigureServiceAccountRemote(ctx, remoteName, saPath, ""); err != nil {
		return "", fmt.Errorf("configuring rclone remote %q: %w", remoteName, err)
	}

	drives, err := w.Rclone.ListSharedDrives(ctx, remoteName)
	if err != nil {
		return "", fmt.Errorf("listing Shared Drives visible to %q: %w\n"+
			"  hint: has the Drive API been enabled on this service account's GCP project?\n"+
			"  (console.cloud.google.com/apis/api/drive.googleapis.com/overview?project=<id>)", remoteName, err)
	}
	if len(drives) == 0 {
		return "", fmt.Errorf("service account %q can see zero Shared Drives\n"+
			"  hint: a service account has no personal My Drive storage quota and can only use a Shared\n"+
			"  Drive it's been added to (at least Content Manager) - create one and add this service\n"+
			"  account, then run `dockvault setup` again", remoteName)
	}

	options := make([]string, len(drives))
	for i, d := range drives {
		options[i] = fmt.Sprintf("%s  (%s)", d.Name, d.ID)
	}
	idx, err := utils.SelectOne(out, in, "Select the Shared Drive to back up to:", options)
	if err != nil {
		return "", err
	}
	chosen := drives[idx]

	if err := w.Rclone.ConfigureServiceAccountRemote(ctx, remoteName, saPath, chosen.ID); err != nil {
		return "", fmt.Errorf("finalizing rclone remote %q against %q: %w", remoteName, chosen.Name, err)
	}
	// Confirm end-to-end access against the finalized remote, not just the
	// discovery one above - cheap, and it's the last chance to catch a
	// problem before config.json gets written.
	if _, err := w.Rclone.ListSharedDrives(ctx, remoteName); err != nil {
		return "", fmt.Errorf("confirming access to %q after finalizing remote %q: %w", chosen.Name, remoteName, err)
	}
	fmt.Fprintf(out, "Confirmed: remote %q can reach %q\n\n", remoteName, chosen.Name)
	return remoteName, nil
}

// setupConfigDefaults prompts for config.json's global defaults and
// writes it - the same fields generate's per-job overrides start from.
func (w *Wizard) setupConfigDefaults(in *bufio.Reader, out io.Writer, remoteName string) error {
	fmt.Fprintln(out, "Step 3: defaults")
	cfg, err := w.Workspace.LoadConfig()
	if err != nil {
		return fmt.Errorf("loading existing config: %w", err)
	}
	cfg.RcloneRemote = remoteName

	env, err := promptString(out, in, "Environment tag (used in remote paths, e.g. \"stage\" or \"prod\")", cfg.Environment)
	if err != nil {
		return err
	}
	cfg.Environment = env

	for {
		days, err := promptInt(out, in, "Retention days", cfg.RetentionDays)
		if err != nil {
			return err
		}
		if verr := utils.ValidateRetentionDays(days); verr != nil {
			fmt.Fprintln(out, verr)
			continue
		}
		cfg.RetentionDays = days
		break
	}
	for {
		n, err := promptInt(out, in, "Min files to keep", cfg.MinFilesToKeep)
		if err != nil {
			return err
		}
		if verr := utils.ValidateMinFilesToKeep(n); verr != nil {
			fmt.Fprintln(out, verr)
			continue
		}
		cfg.MinFilesToKeep = n
		break
	}
	parallel, err := promptInt(out, in, "Parallel jobs", cfg.ParallelJobs)
	if err != nil {
		return err
	}
	if parallel < 1 {
		parallel = 1
	}
	cfg.ParallelJobs = parallel

	if err := w.Workspace.SaveConfig(cfg); err != nil {
		return fmt.Errorf("saving config.json: %w", err)
	}
	fmt.Fprintf(out, "Saved to %s\n", w.Workspace.ConfigPath())
	return nil
}

func promptInt(out io.Writer, in *bufio.Reader, label string, def int) (int, error) {
	fmt.Fprintf(out, "%s [%d]: ", label, def)
	line, err := in.ReadString('\n')
	if err != nil {
		return 0, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		fmt.Fprintln(out, "enter a whole number")
		return promptInt(out, in, label, def)
	}
	return n, nil
}

func promptString(out io.Writer, in *bufio.Reader, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}
