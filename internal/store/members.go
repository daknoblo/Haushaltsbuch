package store

import (
	"context"
	"database/sql"
	"errors"
)

// ListMembers returns all members of a household, ordered for display.
func (s *Store) ListMembers(ctx context.Context, householdID int64) ([]Member, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, household_id, name, color, sort_order
		 FROM members WHERE household_id = ?
		 ORDER BY sort_order, id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.HouseholdID, &m.Name, &m.Color, &m.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMember returns a single member by id.
func (s *Store) GetMember(ctx context.Context, id int64) (Member, error) {
	var m Member
	err := s.q.QueryRowContext(ctx,
		`SELECT id, household_id, name, color, sort_order FROM members WHERE id = ?`, id,
	).Scan(&m.ID, &m.HouseholdID, &m.Name, &m.Color, &m.SortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNotFound
	}
	return m, err
}

// CreateMember inserts a new member and returns it.
func (s *Store) CreateMember(ctx context.Context, householdID int64, name, color string) (Member, error) {
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO members (household_id, name, color, sort_order)
		 VALUES (?, ?, ?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM members WHERE household_id = ?))`,
		householdID, name, color, householdID,
	)
	if err != nil {
		return Member{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Member{}, err
	}
	return s.GetMember(ctx, id)
}

// UpdateMember updates a member's name and color within a household.
func (s *Store) UpdateMember(ctx context.Context, householdID, id int64, name, color string) error {
	return affected(s.q.ExecContext(ctx,
		`UPDATE members SET name = ?, color = ? WHERE id = ? AND household_id = ?`,
		name, color, id, householdID))
}

// DeleteMember removes a member of a household.
func (s *Store) DeleteMember(ctx context.Context, householdID, id int64) error {
	return affected(s.q.ExecContext(ctx,
		`DELETE FROM members WHERE id = ? AND household_id = ?`, id, householdID))
}
