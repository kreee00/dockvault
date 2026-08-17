// Package docksec is the planned FYP follow-on to dockvault: a container
// vulnerability scanner wired in through internal/plugins.SecurityScanner.
// This is currently a skeleton establishing the shape; scanning logic
// lands in scanner.go once the FYP phase starts.
package docksec

import (
	"context"
	"fmt"

	"dockvault/internal/plugins"
)

const Name = "docksec"

// Plugin implements plugins.SecurityScanner.
type Plugin struct {
	config plugins.PluginConfig
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Name() string { return Name }

func (p *Plugin) Init(config plugins.PluginConfig) error {
	p.config = config
	return nil
}

func (p *Plugin) Run(ctx context.Context, target interface{}) (interface{}, error) {
	containerID, ok := target.(string)
	if !ok {
		return nil, fmt.Errorf("docksec.Run: expected a container ID string, got %T", target)
	}
	return p.ScanContainer(containerID)
}
