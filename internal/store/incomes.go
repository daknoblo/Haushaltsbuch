package store

import (
	"database/sql"
	"errors"
)

// ListIncomes returns all income lines of a household for a given month.
func (s *Store) ListIncomes(householdID int64, yearMonth string) ([]Income, error) {
	rows, err := s.db.Query(
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

// GetIncome returns a single income line by id.
func (s *Store) GetIncome(id int64) (Income, error) {
	var in Income
	err := s.db.QueryRow(
		`SELECT id, household_id, member_id, year_month, name, amount_cents, sort_order
		 FROM incomes WHERE id = ?`, id,
	).Scan(&in.ID, &in.HouseholdID, &in.MemberID, &in.YearMonth, &in.Name, &in.AmountCents, &in.SortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return Income{}, ErrNotFound
	}
	return in, err
}

// CreateIncome inserts a new income line and returns it.
func (s *Store) CreateIncome(householdID, memberID int64, yearMonth, name string, amountCents int64) (Income, error) {
	ts := now()
	res, err := s.db.Exec(
		`INSERT INTO incomes (household_id, member_id, year_month, name, amount_cents, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?,
			(SELECT COALESCE(MAX(sort_order)+1, 0) FROM incomes WHERE household_id = ? AND member_id = ? AND year_month = ?),
			?, ?)`,
		householdID, memberID, yearMonth, name, amountCents,
		householdID, memberID, yearMonth, ts, ts,
	)
	if err != nil {
		return Income{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Income{}, err
	}
	return s.GetIncome(id)
}

// UpdateIncome updates the name and amount of an income line.
func (s *Store) UpdateIncome(id int64, name string, amountCents int64) error {
	_, err := s.db.Exec(
		`UPDATE incomes SET name = ?, amount_cents = ?, updated_at = ? WHERE id = ?`,
		name, amountCents, now(), id)
	return err
}

// DeleteIncome removes an income line.
func (s *Store) DeleteIncome(id int64) error {
	_, err := s.db.Exec(`DELETE FROM incomes WHERE id = ?`, id)
	return err
}

// CopyIncomes copies all income lines of a household from one month to another,
// appending them to the target month. It returns the number of lines copied.
func (s *Store) CopyIncomes(householdID int64, fromMonth, toMonth string) (int, error) {
	res, err := s.db.Exec(
		`INSERT INTO incomes (household_id, member_id, year_month, name, amount_cents, sort_order, created_at, updated_at)
		 SELECT household_id, member_id, ?, name, amount_cents, sort_order, ?, ?
		 FROM incomes WHERE household_id = ? AND year_month = ?`,
		toMonth, now(), now(), householdID, fromMonth,
	)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
