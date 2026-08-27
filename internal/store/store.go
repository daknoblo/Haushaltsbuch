// Package store provides SQLite-backed persistence for Haushaltsbuch using the
// pure-Go modernc.org/sqlite driver (no CGO).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrBadSnapshot is returned for a backup file this version cannot read.
var ErrBadSnapshot = errors.New("store: unreadable snapshot")

// ErrCategoryInUse is returned when a category still carries bookings. A
// booking cannot exist without a category, so the reference must be moved
// before the category can go.
var ErrCategoryInUse = errors.New("store: category still in use")

// dbtx is the subset of *sql.DB and *sql.Tx used by the queries, so that every
// method can run standalone or inside a transaction.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store wraps the SQLite database connection.
type Store struct {
	db *sql.DB
	q  dbtx
}

// Open opens (creating if necessary) the SQLite database at path, applies
// pending migrations and returns a ready-to-use Store.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	dsn := "file:" + escapeDSNPath(path) +
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

	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	s := &Store{db: db, q: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// escapeDSNPath percent-encodes only the characters that would otherwise
// terminate or alter the file part of a SQLite URI. Path separators must stay
// intact, which rules out url.PathEscape.
func escapeDSNPath(path string) string {
	return strings.NewReplacer(
		"%", "%25",
		"?", "%3f",
		"#", "%23",
	).Replace(path)
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping verifies that the database is reachable.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// withTx runs fn inside a transaction, rolling back on error. Nested calls
// reuse the transaction already in progress.
func (s *Store) withTx(ctx context.Context, fn func(*Store) error) error {
	if _, ok := s.q.(*sql.Tx); ok {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(&Store{db: s.db, q: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

// affected turns a statement that matched no row into ErrNotFound.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return err
	}

	// Rebuilding a table means dropping it, and DROP TABLE with enforcement on
	// runs an implicit DELETE that fires the children's ON DELETE CASCADE. The
	// pragma is a no-op inside a transaction, so it has to bracket the loop
	// rather than sit in a migration; foreign_key_check takes over the job.
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer func() { _, _ = s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`) }()

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
		if err := s.db.QueryRowContext(ctx,
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

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			name, now(),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if err := s.checkForeignKeys(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// checkForeignKeys reports rows a migration left pointing at nothing, which is
// the price of running the migrations with enforcement turned off.
func (s *Store) checkForeignKeys(ctx context.Context, migration string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()

	if rows.Next() {
		var table, rowID, parent, fkID any
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return err
		}
		return fmt.Errorf("migration %s left %v pointing at a missing %v", migration, table, parent)
	}
	return rows.Err()
}

// GetState returns the value for key from app_state, or "" if unset.
func (s *Store) GetState(ctx context.Context, key string) (string, error) {
	var v string
	err := s.q.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetState upserts a key/value pair in app_state.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	_, err := s.q.ExecContext(ctx,
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
