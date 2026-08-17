// Package plugins defines the extension interface dockvault's core
// doesn't depend on directly - it exists so DockSec (a Docker security
// scanner, planned as FYP follow-on work) and future tools can hook into
// dockvault without dockvault's core importing them.
package plugins

import "context"

// Plugin is the base interface every dockvault plugin implements.
type Plugin interface {
	Name() string
	Init(config PluginConfig) error
	Run(ctx context.Context, target interface{}) (interface{}, error)
}

// PluginConfig is passed to Init. It's intentionally a loose bag of
// settings (rather than a fixed struct) since different plugin kinds will
// need very different configuration shapes; a plugin defines and
// validates its own expected keys.
type PluginConfig map[string]interface{}

// SecurityScanner extends Plugin with vulnerability scanning - this is the
// interface DockSec will implement.
type SecurityScanner interface {
	Plugin
	ScanContainer(containerID string) ([]Vulnerability, error)
	Remediate(vuln Vulnerability) error
}

// Vulnerability describes one finding from a SecurityScanner.
type Vulnerability struct {
	CVE              string
	Severity         string // "CRITICAL", "HIGH", "MEDIUM", "LOW"
	Component        string
	RemediationSteps []string
}
