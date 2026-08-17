package plugins

import "fmt"

// Registry holds Plugins keyed by Name(), populated at process startup by
// whichever plugins are compiled in (Go plugins are registered at
// compile time here, not dynamically loaded - dockvault ships as a single
// static binary per the spec, so "plugin discovery" means "which of the
// compiled-in plugins does the config enable", not .so loading).
type Registry struct {
	plugins map[string]Plugin
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{plugins: map[string]Plugin{}}
}

// Register adds p to the registry. Panics on duplicate names, matching
// internal/cli's command registration convention - both are startup-time
// wiring bugs, not runtime conditions.
func (r *Registry) Register(p Plugin) {
	if _, exists := r.plugins[p.Name()]; exists {
		panic("plugins: duplicate plugin registered: " + p.Name())
	}
	r.plugins[p.Name()] = p
}

// Get returns the named plugin, or an error if it isn't registered.
func (r *Registry) Get(name string) (Plugin, error) {
	p, ok := r.plugins[name]
	if !ok {
		return nil, fmt.Errorf("plugin %q is not registered", name)
	}
	return p, nil
}

// List returns the names of every registered plugin.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.plugins))
	for n := range r.plugins {
		names = append(names, n)
	}
	return names
}
