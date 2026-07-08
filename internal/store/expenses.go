package store

import (
	"database/sql"
	"errors"
)

type scanner interface {
	Scan(dest ...any) error
}

const expenseColumns = `id, household_id, section_id, category_id, name, amount_cents,
	frequency, cost_nature, budget_class, is_oneoff, occurred_on,
	active_from, active_until, split_mode, sort_order, created_at, updated_at`

func scanExpense(sc scanner) (Expense, error) {
	var (
		e         Expense
		sectionID sql.NullInt64
		catID     sql.NullInt64
		oneoff    int
	)
	err := sc.Scan(
		&e.ID, &e.HouseholdID, &sectionID, &catID, &e.Name, &e.AmountCents,
		&e.Frequency, &e.CostNature, &e.BudgetClass, &oneoff, &e.OccurredOn,
		&e.ActiveFrom, &e.ActiveUntil, &e.SplitMode, &e.SortOrder, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return Expense{}, err
	}
	if sectionID.Valid {
		v := sectionID.Int64
		e.SectionID = &v
	}
	if catID.Valid {
		v := catID.Int64
		e.CategoryID = &v
	}
	e.IsOneOff = oneoff != 0
	return e, nil
}

// ListExpenses returns all expenses of a household.
func (s *Store) ListExpenses(householdID int64) ([]Expense, error) {
	rows, err := s.db.Query(
		`SELECT `+expenseColumns+` FROM expenses
		 WHERE household_id = ?
		 ORDER BY sort_order, id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Expense
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetExpense returns a single expense by id.
func (s *Store) GetExpense(id int64) (Expense, error) {
	e, err := scanExpense(s.db.QueryRow(
		`SELECT `+expenseColumns+` FROM expenses WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Expense{}, ErrNotFound
	}
	return e, err
}

// CreateExpense inserts a new expense and returns it. CreatedAt/UpdatedAt and
// SortOrder are assigned automatically.
func (s *Store) CreateExpense(e Expense) (Expense, error) {
	ts := now()
	res, err := s.db.Exec(
		`INSERT INTO expenses
			(household_id, section_id, category_id, name, amount_cents, frequency,
			 cost_nature, budget_class, is_oneoff, occurred_on, active_from,
			 active_until, split_mode, sort_order, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			(SELECT COALESCE(MAX(sort_order)+1, 0) FROM expenses WHERE household_id = ?), ?, ?)`,
		e.HouseholdID, nullInt(e.SectionID), nullInt(e.CategoryID), e.Name, e.AmountCents,
		string(e.Frequency), string(e.CostNature), string(e.BudgetClass), boolToInt(e.IsOneOff),
		e.OccurredOn, e.ActiveFrom, e.ActiveUntil, string(e.SplitMode),
		e.HouseholdID, ts, ts,
	)
	if err != nil {
		return Expense{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Expense{}, err
	}
	return s.GetExpense(id)
}

// UpdateExpense persists all mutable fields of e (identified by e.ID).
func (s *Store) UpdateExpense(e Expense) error {
	_, err := s.db.Exec(
		`UPDATE expenses SET
			section_id = ?, category_id = ?, name = ?, amount_cents = ?, frequency = ?,
			cost_nature = ?, budget_class = ?, is_oneoff = ?, occurred_on = ?,
			active_from = ?, active_until = ?, split_mode = ?, updated_at = ?
		 WHERE id = ?`,
		nullInt(e.SectionID), nullInt(e.CategoryID), e.Name, e.AmountCents, string(e.Frequency),
		string(e.CostNature), string(e.BudgetClass), boolToInt(e.IsOneOff), e.OccurredOn,
		e.ActiveFrom, e.ActiveUntil, string(e.SplitMode), now(), e.ID,
	)
	return err
}

// DeleteExpense removes an expense and its splits (cascade).
func (s *Store) DeleteExpense(id int64) error {
	_, err := s.db.Exec(`DELETE FROM expenses WHERE id = ?`, id)
	return err
}

// ListSplits returns the splits of a single expense.
func (s *Store) ListSplits(expenseID int64) ([]ExpenseSplit, error) {
	rows, err := s.db.Query(
		`SELECT id, expense_id, member_id, value FROM expense_splits
		 WHERE expense_id = ? ORDER BY id`, expenseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ExpenseSplit
	for rows.Next() {
		var sp ExpenseSplit
		if err := rows.Scan(&sp.ID, &sp.ExpenseID, &sp.MemberID, &sp.Value); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ListSplitsForHousehold returns all splits of a household's expenses keyed by
// expense id.
func (s *Store) ListSplitsForHousehold(householdID int64) (map[int64][]ExpenseSplit, error) {
	rows, err := s.db.Query(
		`SELECT sp.id, sp.expense_id, sp.member_id, sp.value
		 FROM expense_splits sp
		 JOIN expenses e ON e.id = sp.expense_id
		 WHERE e.household_id = ?
		 ORDER BY sp.id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64][]ExpenseSplit)
	for rows.Next() {
		var sp ExpenseSplit
		if err := rows.Scan(&sp.ID, &sp.ExpenseID, &sp.MemberID, &sp.Value); err != nil {
			return nil, err
		}
		out[sp.ExpenseID] = append(out[sp.ExpenseID], sp)
	}
	return out, rows.Err()
}

// AddSplitMember adds a member to an expense's split (no-op if already present).
func (s *Store) AddSplitMember(expenseID, memberID int64) error {
	_, err := s.db.Exec(
		`INSERT INTO expense_splits (expense_id, member_id, value) VALUES (?, ?, 0)
		 ON CONFLICT(expense_id, member_id) DO NOTHING`,
		expenseID, memberID)
	return err
}

// RemoveSplitMember removes a member from an expense's split.
func (s *Store) RemoveSplitMember(expenseID, memberID int64) error {
	_, err := s.db.Exec(
		`DELETE FROM expense_splits WHERE expense_id = ? AND member_id = ?`,
		expenseID, memberID)
	return err
}

// SetSplitValue upserts the value for a member's split (percent or cents).
func (s *Store) SetSplitValue(expenseID, memberID int64, value float64) error {
	_, err := s.db.Exec(
		`INSERT INTO expense_splits (expense_id, member_id, value) VALUES (?, ?, ?)
		 ON CONFLICT(expense_id, member_id) DO UPDATE SET value = excluded.value`,
		expenseID, memberID, value)
	return err
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
