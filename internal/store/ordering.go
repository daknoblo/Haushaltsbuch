package store

import (
	"context"
	"fmt"
)

// orderScope identifies the set of rows a sort_order applies to. The table and
// where fragments are code constants, never user input.
type orderScope struct {
	table string
	where string
	args  []any
}

func membersScope(householdID int64) orderScope {
	return orderScope{"members", "household_id = ?", []any{householdID}}
}

func sectionsScope(householdID int64) orderScope {
	return orderScope{"sections", "household_id = ?", []any{householdID}}
}

func householdsScope() orderScope {
	return orderScope{"households", "1 = 1", nil}
}

func expensesScope(householdID, sectionID int64) orderScope {
	return orderScope{
		"expenses",
		"household_id = ? AND COALESCE(section_id, 0) = ?",
		[]any{householdID, sectionID},
	}
}

// reorder moves the row with the given id one position up (delta -1) or down
// (delta +1) within its scope and renumbers the whole scope. Renumbering keeps
// the order well-defined even when rows share a sort_order.
func (s *Store) reorder(ctx context.Context, sc orderScope, id int64, delta int) error {
	if delta == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *Store) error {
		ids, err := tx.orderedIDs(ctx, sc)
		if err != nil {
			return err
		}
		pos := -1
		for i, v := range ids {
			if v == id {
				pos = i
				break
			}
		}
		if pos < 0 {
			return ErrNotFound
		}
		target := pos + delta
		if target < 0 || target >= len(ids) {
			return nil
		}
		ids[pos], ids[target] = ids[target], ids[pos]

		for i, v := range ids {
			//nolint:gosec // sc.table is a package-level constant, not user input.
			if _, err := tx.q.ExecContext(ctx,
				fmt.Sprintf(`UPDATE %s SET sort_order = ? WHERE id = ?`, sc.table), i, v,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) orderedIDs(ctx context.Context, sc orderScope) ([]int64, error) {
	//nolint:gosec // sc.table and sc.where are package-level constants.
	query := fmt.Sprintf(`SELECT id FROM %s WHERE %s ORDER BY sort_order, id`, sc.table, sc.where)
	rows, err := s.q.QueryContext(ctx, query, sc.args...)
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

// MoveHousehold reorders a household relative to its siblings.
func (s *Store) MoveHousehold(ctx context.Context, id int64, delta int) error {
	return s.reorder(ctx, householdsScope(), id, delta)
}

// MoveMember reorders a member within its household.
func (s *Store) MoveMember(ctx context.Context, householdID, id int64, delta int) error {
	return s.reorder(ctx, membersScope(householdID), id, delta)
}

// MoveSection reorders a section within its household.
func (s *Store) MoveSection(ctx context.Context, householdID, id int64, delta int) error {
	return s.reorder(ctx, sectionsScope(householdID), id, delta)
}

// MoveExpense reorders an expense within its household and section.
func (s *Store) MoveExpense(ctx context.Context, householdID, sectionID, id int64, delta int) error {
	return s.reorder(ctx, expensesScope(householdID, sectionID), id, delta)
}
