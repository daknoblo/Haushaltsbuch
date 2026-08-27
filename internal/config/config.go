// Package config loads runtime configuration from HB_-prefixed environment
// variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/api"
)

// DBFileName is the SQLite file created inside the data directory.
const DBFileName = "haushaltsbuch.db"

// Config holds the runtime configuration of the application.
type Config struct {
	// HTTPAddr is the listen address (HB_HTTP_ADDR), e.g. ":8080".
	HTTPAddr string
	// DataDir holds all persistent state (HB_DATA_DIR).
	DataDir string
	// LogLevel is the minimum slog level (HB_LOG_LEVEL).
	LogLevel slog.Level
	// TZ is the configured IANA time zone (TZ). Empty means system default.
	TZ string
	// APIToken enables the machine-facing API (HB_API_TOKEN). Empty keeps it
	// off, which is the right default for an app that has no login.
	APIToken string
}

// DBPath returns the full path of the SQLite database.
func (c Config) DBPath() string {
	return filepath.Join(c.DataDir, DBFileName)
}

// Load reads the configuration from the environment, applying sensible
// defaults for any values that are not set.
func Load() Config {
	return Config{
		HTTPAddr: getenv("HB_HTTP_ADDR", ":8080"),
		DataDir:  getenv("HB_DATA_DIR", "/appdata"),
		LogLevel: parseLevel(getenv("HB_LOG_LEVEL", "info")),
		TZ:       strings.TrimSpace(os.Getenv("TZ")),
		APIToken: strings.TrimSpace(os.Getenv(api.TokenEnv)),
	}
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Validate reports configuration values that would only fail later at startup.
func (c Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.HTTPAddr); err != nil {
		return fmt.Errorf("HB_HTTP_ADDR %q is not a host:port address: %w", c.HTTPAddr, err)
	}
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("HB_DATA_DIR must not be empty")
	}
	return nil
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
