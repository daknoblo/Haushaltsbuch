// Package config loads runtime configuration from HB_-prefixed environment
// variables.
package config

import (
	"log/slog"
	"os"
	"strings"
)

// Config holds the runtime configuration of the application.
type Config struct {
	// Addr is the listen address (HB_ADDR), e.g. ":8080".
	Addr string
	// DBPath is the path to the SQLite database file (HB_DB_PATH).
	DBPath string
	// LogLevel is the minimum slog level (HB_LOG_LEVEL).
	LogLevel slog.Level
	// TZ is the configured IANA time zone (TZ). Empty means system default.
	TZ string
}

// Load reads the configuration from the environment, applying sensible
// defaults for any values that are not set.
func Load() Config {
	return Config{
		Addr:     getenv("HB_ADDR", ":8080"),
		DBPath:   getenv("HB_DB_PATH", "appdata/haushaltsbuch.db"),
		LogLevel: parseLevel(getenv("HB_LOG_LEVEL", "info")),
		TZ:       strings.TrimSpace(os.Getenv("TZ")),
	}
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
