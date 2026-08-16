package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type scanner interface {
	Scan(dest ...any) error
}

// SplitInput is a member's participation in an expense as submitted by the UI.
type SplitInput struct {
	MemberID int64
	Value    float64
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
func (s *Store) ListExpenses(ctx context.Context, householdID int64) ([]Expense, error) {
	rows, err := s.q.QueryContext(ctx,
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
func (s *Store) GetExpense(ctx context.Context, id int64) (Expense, error) {
	e, err := scanExpense(s.q.QueryRowContext(ctx,
		`SELECT `+expenseColumns+` FROM expenses WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Expense{}, ErrNotFound
	}
	return e, err
}

// These sub-selects resolve to NULL unless the referenced row belongs to the
// same household, so a forged id cannot create a cross-household reference.
const (
	sectionRef  = `(SELECT id FROM sections WHERE id = ? AND household_id = ?)`
	categoryRef = `(SELECT id FROM categories WHERE id = ? AND household_id = ?)`
)

// CreateExpense inserts a new expense together with its splits and returns it.
// CreatedAt/UpdatedAt and SortOrder are assigned automatically.
func (s *Store) CreateExpense(ctx context.Context, e Expense, splits []SplitInput) (Expense, error) {
	var created Expense
	err := s.withTx(ctx, func(tx *Store) error {
		ts := now()
		res, err := tx.q.ExecContext(ctx,
			`INSERT INTO expenses
				(household_id, section_id, category_id, name, amount_cents, frequency,
				 cost_nature, budget_class, is_oneoff, occurred_on, active_from,
				 active_until, split_mode, sort_order, created_at, updated_at)
			 VALUES (?, `+sectionRef+`, `+categoryRef+`, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
				(SELECT COALESCE(MAX(sort_order)+1, 0) FROM expenses WHERE household_id = ?), ?, ?)`,
			e.HouseholdID,
			nullInt(e.SectionID), e.HouseholdID,
			nullInt(e.CategoryID), e.HouseholdID,
			e.Name, e.AmountCents,
			string(e.Frequency), string(e.CostNature), string(e.BudgetClass), boolToInt(e.IsOneOff),
			e.OccurredOn, e.ActiveFrom, e.ActiveUntil, string(e.SplitMode),
			e.HouseholdID, ts, ts,
		)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := tx.replaceSplits(ctx, id, e.HouseholdID, splits); err != nil {
			return err
		}
		created, err = tx.GetExpense(ctx, id)
		return err
	})
	if err != nil {
		return Expense{}, err
	}
	return created, nil
}

// SaveExpense persists all mutable fields of e together with its splits in a
// single transaction. The update is scoped to e.HouseholdID.
func (s *Store) SaveExpense(ctx context.Context, e Expense, splits []SplitInput) error {
	return s.withTx(ctx, func(tx *Store) error {
		err := affected(tx.q.ExecContext(ctx,
			`UPDATE expenses SET
				section_id = `+sectionRef+`, category_id = `+categoryRef+`, name = ?,
				amount_cents = ?, frequency = ?, cost_nature = ?, budget_class = ?,
				is_oneoff = ?, occurred_on = ?, active_from = ?, active_until = ?,
				split_mode = ?, updated_at = ?
			 WHERE id = ? AND household_id = ?`,
			nullInt(e.SectionID), e.HouseholdID,
			nullInt(e.CategoryID), e.HouseholdID,
			e.Name, e.AmountCents, string(e.Frequency),
			string(e.CostNature), string(e.BudgetClass), boolToInt(e.IsOneOff), e.OccurredOn,
			e.ActiveFrom, e.ActiveUntil, string(e.SplitMode), now(), e.ID, e.HouseholdID,
		))
		if err != nil {
			return err
		}
		return tx.replaceSplits(ctx, e.ID, e.HouseholdID, splits)
	})
}

// DeleteExpense removes an expense of a household and its splits (cascade).
func (s *Store) DeleteExpense(ctx context.Context, householdID, id int64) error {
	return affected(s.q.ExecContext(ctx,
		`DELETE FROM expenses WHERE id = ? AND household_id = ?`, id, householdID))
}

// ReplaceSplits makes the stored splits of an expense match splits. Member ids
// outside householdID are silently skipped.
func (s *Store) ReplaceSplits(ctx context.Context, expenseID, householdID int64, splits []SplitInput) error {
	return s.withTx(ctx, func(tx *Store) error {
		return tx.replaceSplits(ctx, expenseID, householdID, splits)
	})
}

func (s *Store) replaceSplits(ctx context.Context, expenseID, householdID int64, splits []SplitInput) error {
	args := make([]any, 0, len(splits)+1)
	args = append(args, expenseID)
	marks := make([]string, 0, len(splits))
	for _, sp := range splits {
		marks = append(marks, "?")
		args = append(args, sp.MemberID)
	}

	del := `DELETE FROM expense_splits WHERE expense_id = ?`
	if len(marks) > 0 {
		del += ` AND member_id NOT IN (` + strings.Join(marks, ", ") + `)`
	}
	if _, err := s.q.ExecContext(ctx, del, args...); err != nil {
		return err
	}

	for _, sp := range splits {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO expense_splits (expense_id, member_id, value)
			 SELECT ?, id, ? FROM members WHERE id = ? AND household_id = ?
			 ON CONFLICT(expense_id, member_id) DO UPDATE SET value = excluded.value`,
			expenseID, sp.Value, sp.MemberID, householdID,
		); err != nil {
			return err
		}
	}
	return nil
}

// ListSplits returns the splits of a single expense.
func (s *Store) ListSplits(ctx context.Context, expenseID int64) ([]ExpenseSplit, error) {
	rows, err := s.q.QueryContext(ctx,
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
func (s *Store) ListSplitsForHousehold(ctx context.Context, householdID int64) (map[int64][]ExpenseSplit, error) {
	rows, err := s.q.QueryContext(ctx,
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
