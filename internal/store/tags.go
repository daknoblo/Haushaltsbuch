package store

import (
	"context"
	"strings"
)

// ListTags returns all tags of a household in alphabetical order.
func (s *Store) ListTags(ctx context.Context, householdID int64) ([]Tag, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, household_id, name, color FROM tags
		 WHERE household_id = ? ORDER BY name COLLATE NOCASE`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.HouseholdID, &t.Name, &t.Color); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateTag adds a tag, or returns the existing one when the name is taken.
func (s *Store) CreateTag(ctx context.Context, householdID int64, name, color string) (Tag, error) {
	var t Tag
	err := s.withTx(ctx, func(tx *Store) error {
		if _, err := tx.q.ExecContext(ctx,
			`INSERT INTO tags (household_id, name, color) VALUES (?, ?, ?)
			 ON CONFLICT(household_id, name) DO UPDATE SET color = excluded.color`,
			householdID, name, color,
		); err != nil {
			return err
		}
		return tx.q.QueryRowContext(ctx,
			`SELECT id, household_id, name, color FROM tags
			 WHERE household_id = ? AND name = ?`, householdID, name,
		).Scan(&t.ID, &t.HouseholdID, &t.Name, &t.Color)
	})
	if err != nil {
		return Tag{}, err
	}
	return t, nil
}

// RenameTag changes a tag's name and color within a household.
func (s *Store) RenameTag(ctx context.Context, householdID, id int64, name, color string) error {
	return affected(s.q.ExecContext(ctx,
		`UPDATE tags SET name = ?, color = ? WHERE id = ? AND household_id = ?`,
		name, color, id, householdID))
}

// DeleteTag removes a tag of a household and detaches it from all bookings.
func (s *Store) DeleteTag(ctx context.Context, householdID, id int64) error {
	return affected(s.q.ExecContext(ctx,
		`DELETE FROM tags WHERE id = ? AND household_id = ?`, id, householdID))
}

// ListTagIDs returns the tag ids of a single booking of a household.
func (s *Store) ListTagIDs(ctx context.Context, householdID, bookingID int64) ([]int64, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT bt.tag_id
		 FROM booking_tags bt
		 JOIN bookings b ON b.id = bt.booking_id
		 JOIN tags t ON t.id = bt.tag_id
		 WHERE b.id = ? AND b.household_id = ?
		 ORDER BY t.name COLLATE NOCASE`, bookingID, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListBookingTags returns the tag ids of a household's bookings keyed by
// booking id.
func (s *Store) ListBookingTags(ctx context.Context, householdID int64) (map[int64][]int64, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT bt.booking_id, bt.tag_id
		 FROM booking_tags bt
		 JOIN bookings b ON b.id = bt.booking_id
		 JOIN tags t ON t.id = bt.tag_id
		 WHERE b.household_id = ?
		 ORDER BY t.name COLLATE NOCASE`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64][]int64)
	for rows.Next() {
		var bookingID, tagID int64
		if err := rows.Scan(&bookingID, &tagID); err != nil {
			return nil, err
		}
		out[bookingID] = append(out[bookingID], tagID)
	}
	return out, rows.Err()
}

// replaceTags makes the stored tags of a booking match tagIDs. Tag ids outside
// householdID are silently skipped.
func (s *Store) replaceTags(ctx context.Context, bookingID, householdID int64, tagIDs []int64) error {
	args := make([]any, 0, len(tagIDs)+1)
	args = append(args, bookingID)
	marks := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		marks = append(marks, "?")
		args = append(args, id)
	}

	del := `DELETE FROM booking_tags WHERE booking_id = ?`
	if len(marks) > 0 {
		del += ` AND tag_id NOT IN (` + strings.Join(marks, ", ") + `)`
	}
	if _, err := s.q.ExecContext(ctx, del, args...); err != nil {
		return err
	}

	for _, id := range tagIDs {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO booking_tags (booking_id, tag_id)
			 SELECT ?, id FROM tags WHERE id = ? AND household_id = ?
			 ON CONFLICT(booking_id, tag_id) DO NOTHING`,
			bookingID, id, householdID,
		); err != nil {
			return err
		}
	}
	return nil
}
