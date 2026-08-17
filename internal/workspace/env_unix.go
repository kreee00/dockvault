//go:build !windows

package workspace

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// saveEnv writes env as KEY=VALUE lines, mode 600 since it may hold
// credentials.
func (w *Workspace) saveEnv(jobName string, env map[string]string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&buf, "%s=%s\n", k, env[k])
	}
	return os.WriteFile(w.EnvPath(jobName), []byte(buf.String()), 0o600)
}

// loadEnv parses a job's .env, returning an empty map (no error) if it
// doesn't exist - most jobs won't have one (no credentials, no webhook).
func (w *Workspace) loadEnv(jobName string) (map[string]string, error) {
	data, err := os.ReadFile(w.EnvPath(jobName))
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading .env for %q: %w", jobName, err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}
