// Package version holds build metadata injected at build time via -ldflags.
package version

// These values are overridden at build time using -ldflags -X.
var (
	Version = "dev"     // semver tag, e.g. v1.2.3
	Commit  = "unknown" // git commit hash
	Date    = "unknown" // build date (RFC3339)
)
