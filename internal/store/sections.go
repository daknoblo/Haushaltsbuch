package store

import (
	"database/sql"
	"errors"
)

// ListSections returns all sections of a household, ordered for display.
func (s *Store) ListSections(householdID int64) ([]Section, error) {
	rows, err := s.db.Query(
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

// GetSection returns a single section by id.
func (s *Store) GetSection(id int64) (Section, error) {
	var sec Section
	err := s.db.QueryRow(
		`SELECT id, household_id, name, sort_order FROM sections WHERE id = ?`, id,
	).Scan(&sec.ID, &sec.HouseholdID, &sec.Name, &sec.SortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return Section{}, ErrNotFound
	}
	return sec, err
}

// CreateSection inserts a new section and returns it.
func (s *Store) CreateSection(householdID int64, name string) (Section, error) {
	res, err := s.db.Exec(
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
	return s.GetSection(id)
}

// RenameSection updates a section's name.
func (s *Store) RenameSection(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE sections SET name = ? WHERE id = ?`, name, id)
	return err
}

// DeleteSection removes a section; expenses in it are kept but unassigned.
func (s *Store) DeleteSection(id int64) error {
	_, err := s.db.Exec(`DELETE FROM sections WHERE id = ?`, id)
	return err
}
