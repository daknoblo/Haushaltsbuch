// Package store provides SQLite-backed persistence for Haushaltsbuch using the
// pure-Go modernc.org/sqlite driver (no CGO).
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store wraps the SQLite database connection.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path, applies
// pending migrations and returns a ready-to-use Store.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite is a single-writer database; serializing access avoids
	// SQLITE_BUSY errors in this low-traffic personal application.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var count int
		if err := s.db.QueryRow(
			`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, name,
		).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}

		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			name, now(),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// GetState returns the value for key from app_state, or "" if unset.
func (s *Store) GetState(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM app_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetState upserts a key/value pair in app_state.
func (s *Store) SetState(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO app_state (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// now returns the current time formatted as RFC3339 for storage.
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
