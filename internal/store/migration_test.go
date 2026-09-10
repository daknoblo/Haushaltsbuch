package store

import (
	"strings"
	"testing"
)

func TestMigrationForeignKeyFailureRollsBackSchemaDataAndVersion(t *testing.T) {
	s, ctx, h := seededStore(t)
	if _, err := s.q.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := s.q.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
			t.Error(err)
		}
	}()
	const migration = "test_atomic_migration.sql"
	err := s.applyMigration(ctx, migration, []byte(`
		ALTER TABLE households ADD COLUMN migration_probe TEXT;
		UPDATE households SET name = 'must roll back';
		CREATE TABLE migration_child (household_id INTEGER REFERENCES households(id));
		INSERT INTO migration_child VALUES (-1);
	`))
	if err == nil || !strings.Contains(err.Error(), "missing households") {
		t.Fatalf("migration error=%v, want foreign-key failure", err)
	}
	saved, err := s.GetHousehold(ctx, h.ID)
	if err != nil || saved.Name != h.Name {
		t.Fatalf("household after rollback=%+v, error=%v", saved, err)
	}
	var count int
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, migration).Scan(&count); err != nil || count != 0 {
		t.Fatalf("version after rollback=%d, error=%v", count, err)
	}
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'migration_child'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("child table after rollback=%d, error=%v", count, err)
	}
	// The same ALTER succeeds only if the first migration really rolled back.
	if err := s.applyMigration(ctx, migration, []byte(`ALTER TABLE households ADD COLUMN migration_probe TEXT`)); err != nil {
		t.Fatalf("corrected migration: %v", err)
	}
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, migration).Scan(&count); err != nil || count != 1 {
		t.Fatalf("version after retry=%d, error=%v", count, err)
	}
}

func TestMigrationSQLFailureRollsBackEarlierStatements(t *testing.T) {
	s, ctx, h := seededStore(t)
	err := s.applyMigration(ctx, "test_bad_sql.sql", []byte(`
		UPDATE households SET name = 'must roll back';
		INSERT INTO nonexistent_migration_table VALUES (1);
	`))
	if err == nil {
		t.Fatal("invalid SQL succeeded")
	}
	saved, err := s.GetHousehold(ctx, h.ID)
	if err != nil || saved.Name != h.Name {
		t.Fatalf("household after rollback=%+v, error=%v", saved, err)
	}
}
