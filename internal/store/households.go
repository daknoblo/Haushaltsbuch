package store

import (
	"database/sql"
	"errors"
	"strconv"
)

const stateActiveHousehold = "active_household_id"

// ListHouseholds returns all households ordered for display.
func (s *Store) ListHouseholds() ([]Household, error) {
	rows, err := s.db.Query(
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
func (s *Store) GetHousehold(id int64) (Household, error) {
	var h Household
	err := s.db.QueryRow(
		`SELECT id, name, sort_order, created_at FROM households WHERE id = ?`, id,
	).Scan(&h.ID, &h.Name, &h.SortOrder, &h.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Household{}, ErrNotFound
	}
	return h, err
}

// CreateHousehold inserts a new household and returns it.
func (s *Store) CreateHousehold(name string) (Household, error) {
	res, err := s.db.Exec(
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
	return s.GetHousehold(id)
}

// RenameHousehold updates a household's name.
func (s *Store) RenameHousehold(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE households SET name = ? WHERE id = ?`, name, id)
	return err
}

// DeleteHousehold removes a household and all of its data (cascade).
func (s *Store) DeleteHousehold(id int64) error {
	_, err := s.db.Exec(`DELETE FROM households WHERE id = ?`, id)
	return err
}

// CountHouseholds returns the number of households.
func (s *Store) CountHouseholds() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM households`).Scan(&n)
	return n, err
}

// ActiveHouseholdID returns the currently active household id, or 0 if none is
// set or the referenced household no longer exists.
func (s *Store) ActiveHouseholdID() (int64, error) {
	v, err := s.GetState(stateActiveHousehold)
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
	if _, err := s.GetHousehold(id); errors.Is(err, ErrNotFound) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	return id, nil
}

// SetActiveHousehold marks the given household as active.
func (s *Store) SetActiveHousehold(id int64) error {
	return s.SetState(stateActiveHousehold, strconv.FormatInt(id, 10))
}
