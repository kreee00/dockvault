// Package version holds build-time version metadata for dockvault.
package version

// These are overridden at build time via -ldflags, e.g.:
//
//	go build -ldflags "\
//	  -X dockvault/internal/version.Version=$(git describe --tags --always) \
//	  -X dockvault/internal/version.Commit=$(git rev-parse --short HEAD) \
//	  -X dockvault/internal/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// String returns a human-readable version line, e.g.:
//
//	dockvault v4.1.0 (commit a1b2c3d, built 2026-08-15T00:00:00Z)
func String() string {
	return "dockvault " + Version + " (commit " + Commit + ", built " + BuildDate + ")"
}
