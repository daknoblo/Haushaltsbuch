package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrCopyTargetNotEmpty is returned when income lines would be copied into a
// month that already has entries.
var ErrCopyTargetNotEmpty = errors.New("store: target month already has income lines")

// ListIncomes returns all income lines of a household for a given month.
func (s *Store) ListIncomes(ctx context.Context, householdID int64, yearMonth string) ([]Income, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, household_id, member_id, year_month, name, amount_cents, sort_order
		 FROM incomes WHERE household_id = ? AND year_month = ?
		 ORDER BY member_id, sort_order, id`, householdID, yearMonth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Income
	for rows.Next() {
		var in Income
		if err := rows.Scan(&in.ID, &in.HouseholdID, &in.MemberID, &in.YearMonth,
			&in.Name, &in.AmountCents, &in.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// ListIncomesRange returns all income lines of a household for the inclusive
// month range [fromMonth, toMonth], keyed by month. It lets callers build
// reports for several months from a single query.
func (s *Store) ListIncomesRange(ctx context.Context, householdID int64, fromMonth, toMonth string) (map[string][]Income, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, household_id, member_id, year_month, name, amount_cents, sort_order
		 FROM incomes WHERE household_id = ? AND year_month BETWEEN ? AND ?
		 ORDER BY year_month, member_id, sort_order, id`, householdID, fromMonth, toMonth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]Income)
	for rows.Next() {
		var in Income
		if err := rows.Scan(&in.ID, &in.HouseholdID, &in.MemberID, &in.YearMonth,
			&in.Name, &in.AmountCents, &in.SortOrder); err != nil {
			return nil, err
		}
		out[in.YearMonth] = append(out[in.YearMonth], in)
	}
	return out, rows.Err()
}

// GetIncome returns a single income line by id.
func (s *Store) GetIncome(ctx context.Context, id int64) (Income, error) {
	var in Income
	err := s.q.QueryRowContext(ctx,
		`SELECT id, household_id, member_id, year_month, name, amount_cents, sort_order
		 FROM incomes WHERE id = ?`, id,
	).Scan(&in.ID, &in.HouseholdID, &in.MemberID, &in.YearMonth, &in.Name, &in.AmountCents, &in.SortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return Income{}, ErrNotFound
	}
	return in, err
}

// CreateIncome inserts a new income line and returns it. The member must belong
// to the household.
func (s *Store) CreateIncome(ctx context.Context, householdID, memberID int64, yearMonth, name string, amountCents int64) (Income, error) {
	var created Income
	err := s.withTx(ctx, func(tx *Store) error {
		ts := now()
		res, err := tx.q.ExecContext(ctx,
			`INSERT INTO incomes (household_id, member_id, year_month, name, amount_cents, sort_order, created_at, updated_at)
			 SELECT ?, id, ?, ?, ?,
				(SELECT COALESCE(MAX(sort_order)+1, 0) FROM incomes WHERE household_id = ? AND member_id = ? AND year_month = ?),
				?, ?
			 FROM members WHERE id = ? AND household_id = ?`,
			householdID, yearMonth, name, amountCents,
			householdID, memberID, yearMonth, ts, ts,
			memberID, householdID,
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		created, err = tx.GetIncome(ctx, id)
		return err
	})
	if err != nil {
		return Income{}, err
	}
	return created, nil
}

// UpdateIncome updates the name and amount of an income line of a household.
func (s *Store) UpdateIncome(ctx context.Context, householdID, id int64, name string, amountCents int64) error {
	return affected(s.q.ExecContext(ctx,
		`UPDATE incomes SET name = ?, amount_cents = ?, updated_at = ?
		 WHERE id = ? AND household_id = ?`,
		name, amountCents, now(), id, householdID))
}

// DeleteIncome removes an income line of a household.
func (s *Store) DeleteIncome(ctx context.Context, householdID, id int64) error {
	return affected(s.q.ExecContext(ctx,
		`DELETE FROM incomes WHERE id = ? AND household_id = ?`, id, householdID))
}

// CopyIncomes copies all income lines of a household from one month to another
// and returns the number of lines copied. Copying onto itself or into a month
// that already has lines is rejected, so repeating the action cannot silently
// duplicate entries.
func (s *Store) CopyIncomes(ctx context.Context, householdID int64, fromMonth, toMonth string) (int, error) {
	if fromMonth == toMonth {
		return 0, ErrCopyTargetNotEmpty
	}
	var n int64
	err := s.withTx(ctx, func(tx *Store) error {
		var existing int
		if err := tx.q.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM incomes WHERE household_id = ? AND year_month = ?`,
			householdID, toMonth,
		).Scan(&existing); err != nil {
			return err
		}
		if existing > 0 {
			return ErrCopyTargetNotEmpty
		}

		ts := now()
		res, err := tx.q.ExecContext(ctx,
			`INSERT INTO incomes (household_id, member_id, year_month, name, amount_cents, sort_order, created_at, updated_at)
			 SELECT household_id, member_id, ?, name, amount_cents, sort_order, ?, ?
			 FROM incomes WHERE household_id = ? AND year_month = ?`,
			toMonth, ts, ts, householdID, fromMonth,
		)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
