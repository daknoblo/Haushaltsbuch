package store

import (
	"database/sql"
	"errors"
	"strings"
)

// ListCategories returns all categories of a household, ordered by name.
func (s *Store) ListCategories(householdID int64) ([]Category, error) {
	rows, err := s.db.Query(
		`SELECT id, household_id, name FROM categories
		 WHERE household_id = ? ORDER BY name`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.HouseholdID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCategory returns a single category by id.
func (s *Store) GetCategory(id int64) (Category, error) {
	var c Category
	err := s.db.QueryRow(
		`SELECT id, household_id, name FROM categories WHERE id = ?`, id,
	).Scan(&c.ID, &c.HouseholdID, &c.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

// CreateCategory inserts a new category and returns it.
func (s *Store) CreateCategory(householdID int64, name string) (Category, error) {
	res, err := s.db.Exec(
		`INSERT INTO categories (household_id, name) VALUES (?, ?)`,
		householdID, name,
	)
	if err != nil {
		return Category{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Category{}, err
	}
	return s.GetCategory(id)
}

// GetOrCreateCategory returns the id of a category matching name (case
// insensitive) in the household, creating it if it does not yet exist. An empty
// name yields a nil id.
func (s *Store) GetOrCreateCategory(householdID int64, name string) (*int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var id int64
	err := s.db.QueryRow(
		`SELECT id FROM categories WHERE household_id = ? AND name = ? COLLATE NOCASE`,
		householdID, name,
	).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		c, err := s.CreateCategory(householdID, name)
		if err != nil {
			return nil, err
		}
		return &c.ID, nil
	case err != nil:
		return nil, err
	default:
		return &id, nil
	}
}

// RenameCategory updates a category's name.
func (s *Store) RenameCategory(id int64, name string) error {
	_, err := s.db.Exec(`UPDATE categories SET name = ? WHERE id = ?`, name, id)
	return err
}

// DeleteCategory removes a category; expenses keep their reference cleared.
func (s *Store) DeleteCategory(id int64) error {
	_, err := s.db.Exec(`DELETE FROM categories WHERE id = ?`, id)
	return err
}
