package store

import (
	"context"
	"database/sql"
	"errors"
)

// ListSections returns all sections of a household, ordered for display.
func (s *Store) ListSections(ctx context.Context, householdID int64) ([]Section, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, household_id, name, sort_order
		 FROM sections WHERE household_id = ?
		 ORDER BY sort_order, id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Section
	for rows.Next() {
		var sec Section
		if err := rows.Scan(&sec.ID, &sec.HouseholdID, &sec.Name, &sec.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// GetSection returns a single section of a household.
func (s *Store) GetSection(ctx context.Context, householdID, id int64) (Section, error) {
	var sec Section
	err := s.q.QueryRowContext(ctx,
		`SELECT id, household_id, name, sort_order FROM sections
		 WHERE id = ? AND household_id = ?`, id, householdID,
	).Scan(&sec.ID, &sec.HouseholdID, &sec.Name, &sec.SortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return Section{}, ErrNotFound
	}
	return sec, err
}

// CreateSection inserts a new section and returns it.
func (s *Store) CreateSection(ctx context.Context, householdID int64, name string) (Section, error) {
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO sections (household_id, name, sort_order)
		 VALUES (?, ?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM sections WHERE household_id = ?))`,
		householdID, name, householdID,
	)
	if err != nil {
		return Section{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Section{}, err
	}
	return s.GetSection(ctx, householdID, id)
}

// RenameSection updates a section's name within a household.
func (s *Store) RenameSection(ctx context.Context, householdID, id int64, name string) error {
	return affected(s.q.ExecContext(ctx,
		`UPDATE sections SET name = ? WHERE id = ? AND household_id = ?`, name, id, householdID))
}

// DeleteSection removes a section of a household; its expenses are kept but
// become unassigned.
func (s *Store) DeleteSection(ctx context.Context, householdID, id int64) error {
	return affected(s.q.ExecContext(ctx,
		`DELETE FROM sections WHERE id = ? AND household_id = ?`, id, householdID))
}
