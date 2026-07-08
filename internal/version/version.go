// Package version holds build metadata injected at build time via -ldflags.
package version

// These values are overridden at build time using -ldflags -X.
var (
	Version = "dev"     // vYYYYMMDD-HHMM at build time
	Channel = "local"   // "stable", "dev" or "local"
	Commit  = "unknown" // git commit hash
	Date    = "unknown" // build date (RFC3339)
)
