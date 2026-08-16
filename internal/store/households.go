package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

const stateActiveHousehold = "active_household_id"

// ListHouseholds returns all households ordered for display.
func (s *Store) ListHouseholds(ctx context.Context) ([]Household, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, name, sort_order, created_at FROM households
		 ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Household
	for rows.Next() {
		var h Household
		if err := rows.Scan(&h.ID, &h.Name, &h.SortOrder, &h.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetHousehold returns a single household by id.
func (s *Store) GetHousehold(ctx context.Context, id int64) (Household, error) {
	var h Household
	err := s.q.QueryRowContext(ctx,
		`SELECT id, name, sort_order, created_at FROM households WHERE id = ?`, id,
	).Scan(&h.ID, &h.Name, &h.SortOrder, &h.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	return h, err
}

// CreateHousehold inserts a new household and returns it.
func (s *Store) CreateHousehold(ctx context.Context, name string) (Household, error) {
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO households (name, sort_order, created_at)
		 VALUES (?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM households), ?)`,
		name, now(),
	)
	if err != nil {
		return Household{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Household{}, err
	}
	return s.GetHousehold(ctx, id)
}

// RenameHousehold updates a household's name.
func (s *Store) RenameHousehold(ctx context.Context, id int64, name string) error {
	return affected(s.q.ExecContext(ctx,
		`UPDATE households SET name = ? WHERE id = ?`, name, id))
}

// DeleteHousehold removes a household and all of its data (cascade).
func (s *Store) DeleteHousehold(ctx context.Context, id int64) error {
	return affected(s.q.ExecContext(ctx, `DELETE FROM households WHERE id = ?`, id))
}

// CountHouseholds returns the number of households.
func (s *Store) CountHouseholds(ctx context.Context) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(1) FROM households`).Scan(&n)
	return n, err
}

// ActiveHouseholdID returns the currently active household id, or 0 if none is
// set or the referenced household no longer exists.
func (s *Store) ActiveHouseholdID(ctx context.Context) (int64, error) {
	v, err := s.GetState(ctx, stateActiveHousehold)
	if err != nil {
		return 0, err
	}
	if v == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, nil
	}
	// Verify it still exists.
	if _, err := s.GetHousehold(ctx, id); errors.Is(err, ErrNotFound) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	return id, nil
}

// SetActiveHousehold marks the given household as active.
func (s *Store) SetActiveHousehold(ctx context.Context, id int64) error {
	return s.SetState(ctx, stateActiveHousehold, strconv.FormatInt(id, 10))
}
