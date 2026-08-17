package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"dockvault/internal/detector"
	"dockvault/internal/docker"
)

func init() {
	register(&command{
		name:    "scan",
		summary: "Discover Docker volumes, containers, and host paths",
		run:     runScan,
	})
}

func runScan(ctx context.Context, g Global, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	auto := fs.Bool("auto", false, "full auto-detection without prompts")
	output := fs.String("output", "text", "output format: json|text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *output != "json" && *output != "text" {
		return fmt.Errorf("--output must be \"json\" or \"text\", got %q", *output)
	}

	dc := docker.New()
	if err := dc.Ping(ctx); err != nil {
		return fmt.Errorf("%w\n  hint: is Docker running? (Docker Desktop, or `sudo systemctl start docker`)", err)
	}
	det := detector.New(dc, g.Environment)

	vols, err := det.ScanVolumes(ctx)
	if err != nil {
		return err
	}
	// Host-path (hostdir) discovery is disabled: this deployment backs up
	// Docker volumes/services only, never raw host filesystem paths.
	var hostPaths []detector.HostPathInfo

	if !*auto && g.Verbose {
		fmt.Fprintln(os.Stderr, "note: scan currently always performs full detection; --auto is accepted for spec compatibility and future interactive-vs-auto branching")
	}

	if *output == "json" {
		return printScanJSON(vols, hostPaths)
	}
	printScanText(vols, hostPaths)
	return nil
}

func printScanJSON(vols []detector.VolumeInfo, hostPaths []detector.HostPathInfo) error {
	payload := struct {
		Volumes   []detector.VolumeInfo   `json:"volumes"`
		HostPaths []detector.HostPathInfo `json:"host_paths"`
	}{vols, hostPaths}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func printScanText(vols []detector.VolumeInfo, hostPaths []detector.HostPathInfo) {
	fmt.Printf("Found %d volume(s):\n\n", len(vols))
	for i, v := range vols {
		fmt.Printf(" [%d] %s\n", i+1, v.JobName)
		fmt.Printf("     Volume: %s\n", v.Name)
		fmt.Printf("     Type: %s\n", v.ServiceType)
		if len(v.Containers) > 0 {
			fmt.Printf("     Container(s): %s\n", joinComma(v.Containers))
		}
		if len(v.Credentials) > 0 {
			fmt.Printf("     Credentials found: %s\n", maskedCredKeys(v.Credentials))
		}
		fmt.Printf("     Recommended backup: %s\n", v.RecommendedBackupType)
		fmt.Printf("     Proposed job name: %s\n", v.JobName)
		fmt.Printf("     Google Drive path: %s\n", v.GoogleDrivePath)
		fmt.Println()
	}

	if len(hostPaths) > 0 {
		fmt.Printf("Found %d notable host path(s):\n\n", len(hostPaths))
		for i, h := range hostPaths {
			fmt.Printf(" [%d] %s (%s)\n", i+1, h.Path, h.Kind)
			if h.Source != "" {
				fmt.Printf("     From container: %s\n", h.Source)
			}
			if h.JobName != "" {
				fmt.Printf("     Proposed job name: %s\n", h.JobName)
				fmt.Printf("     Google Drive path: %s\n", h.GoogleDrivePath)
			}
			fmt.Println()
		}
	}
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// maskedCredKeys renders "KEY1, KEY2=value, KEY3" the way the spec's
// sample output does: user/db-name-shaped keys are shown in full since
// they aren't secret, while anything password/token/secret-shaped is
// masked with ***.
func maskedCredKeys(creds map[string]string) string {
	out := ""
	first := true
	for k, v := range creds {
		if !first {
			out += ", "
		}
		first = false
		if looksSecret(k) {
			out += k + "=***"
		} else {
			out += k + "=" + v
		}
	}
	return out
}

func looksSecret(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"PASSWORD", "TOKEN", "SECRET", "API_KEY"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}
