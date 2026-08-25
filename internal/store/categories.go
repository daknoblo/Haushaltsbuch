package store

import (
	"context"
	"database/sql"
	"errors"
)

const categoryColumns = `id, household_id, name, classification, color, sort_order`

func scanCategory(sc scanner) (Category, error) {
	var c Category
	err := sc.Scan(&c.ID, &c.HouseholdID, &c.Name, &c.Classification, &c.Color, &c.SortOrder)
	return c, err
}

// ListCategories returns all categories of a household, expense categories
// first and alphabetical within a classification.
func (s *Store) ListCategories(ctx context.Context, householdID int64) ([]Category, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+categoryColumns+` FROM categories
		 WHERE household_id = ?
		 ORDER BY classification DESC, sort_order, name COLLATE NOCASE`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Category
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCategory returns a single category of a household.
func (s *Store) GetCategory(ctx context.Context, householdID, id int64) (Category, error) {
	c, err := scanCategory(s.q.QueryRowContext(ctx,
		`SELECT `+categoryColumns+` FROM categories WHERE id = ? AND household_id = ?`,
		id, householdID))
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

// CreateCategory inserts a new category and returns it.
func (s *Store) CreateCategory(ctx context.Context, householdID int64, name string, class Direction, color string) (Category, error) {
	var out Category
	err := s.withTx(ctx, func(tx *Store) error {
		res, err := tx.q.ExecContext(ctx,
			`INSERT INTO categories (household_id, name, classification, color, sort_order)
			 VALUES (?, ?, ?, ?,
				(SELECT COALESCE(MAX(sort_order)+1, 0) FROM categories WHERE household_id = ?))`,
			householdID, name, string(class), color, householdID,
		)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		out, err = tx.GetCategory(ctx, householdID, id)
		return err
	})
	if err != nil {
		return Category{}, err
	}
	return out, nil
}

// UpdateCategory changes a category's name, classification and color.
func (s *Store) UpdateCategory(ctx context.Context, householdID, id int64, name string, class Direction, color string) error {
	return affected(s.q.ExecContext(ctx,
		`UPDATE categories SET name = ?, classification = ?, color = ?
		 WHERE id = ? AND household_id = ?`,
		name, string(class), color, id, householdID))
}

// DeleteCategory removes a category of a household. It refuses while bookings
// still reference it, because a booking cannot exist without a category.
func (s *Store) DeleteCategory(ctx context.Context, householdID, id int64) error {
	return s.withTx(ctx, func(tx *Store) error {
		var used int
		if err := tx.q.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM bookings WHERE category_id = ? AND household_id = ?`,
			id, householdID,
		).Scan(&used); err != nil {
			return err
		}
		if used > 0 {
			return ErrCategoryInUse
		}
		return affected(tx.q.ExecContext(ctx,
			`DELETE FROM categories WHERE id = ? AND household_id = ?`, id, householdID))
	})
}

// CountCategoryUsage returns how many bookings reference each category of a
// household, keyed by category id.
func (s *Store) CountCategoryUsage(ctx context.Context, householdID int64) (map[int64]int, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT category_id, COUNT(1) FROM bookings
		 WHERE household_id = ? GROUP BY category_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]int)
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
