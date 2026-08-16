package store

import (
	"context"
	"database/sql"
	"errors"
)

// ListCategories returns all categories of a household, ordered by name.
func (s *Store) ListCategories(ctx context.Context, householdID int64) ([]Category, error) {
	rows, err := s.q.QueryContext(ctx,
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
func (s *Store) GetCategory(ctx context.Context, id int64) (Category, error) {
	var c Category
	err := s.q.QueryRowContext(ctx,
		`SELECT id, household_id, name FROM categories WHERE id = ?`, id,
	).Scan(&c.ID, &c.HouseholdID, &c.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

// CreateCategory inserts a new category and returns it.
func (s *Store) CreateCategory(ctx context.Context, householdID int64, name string) (Category, error) {
	res, err := s.q.ExecContext(ctx,
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
	return s.GetCategory(ctx, id)
}

// RenameCategory updates a category's name within a household.
func (s *Store) RenameCategory(ctx context.Context, householdID, id int64, name string) error {
	return affected(s.q.ExecContext(ctx,
		`UPDATE categories SET name = ? WHERE id = ? AND household_id = ?`, name, id, householdID))
}

// DeleteCategory removes a category of a household; expenses keep their
// reference cleared.
func (s *Store) DeleteCategory(ctx context.Context, householdID, id int64) error {
	return affected(s.q.ExecContext(ctx,
		`DELETE FROM categories WHERE id = ? AND household_id = ?`, id, householdID))
}
