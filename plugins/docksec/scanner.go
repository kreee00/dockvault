package docksec

import (
	"fmt"

	"dockvault/internal/plugins"
)

// ScanContainer will inspect containerID's image layers/packages against
// NVD/Snyk vulnerability data and return findings. This is FYP scope
// (DockVault+ / DockSec), not part of the current dockvault rewrite - it's
// stubbed here only so the plugin interface boundary exists and compiles.
func (p *Plugin) ScanContainer(containerID string) ([]plugins.Vulnerability, error) {
	return nil, fmt.Errorf("docksec.ScanContainer: not yet implemented (FYP scope, container=%s)", containerID)
}

// Remediate will apply or suggest a fix for vuln (e.g. base image bump,
// package pin). FYP scope; not yet implemented.
func (p *Plugin) Remediate(vuln plugins.Vulnerability) error {
	return fmt.Errorf("docksec.Remediate: not yet implemented (FYP scope, cve=%s)", vuln.CVE)
}
