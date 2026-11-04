package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ListBankAccounts returns the account setup without loading statement history.
func (s *Store) ListBankAccounts(ctx context.Context, householdID int64) ([]BankAccount, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT a.id, a.member_id, a.name, a.is_default
		FROM bank_accounts a JOIN members m ON m.id = a.member_id AND m.household_id = a.household_id
		WHERE a.household_id = ? ORDER BY m.sort_order, m.id, a.is_default DESC, a.id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BankAccount
	for rows.Next() {
		var a BankAccount
		if err := rows.Scan(&a.ID, &a.MemberID, &a.Name, &a.Default); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ensureMemberBankAccount(ctx context.Context, householdID, memberID int64) error {
	var id int64
	err := s.q.QueryRowContext(ctx, `SELECT id FROM bank_accounts WHERE household_id = ? AND member_id = ?
		ORDER BY is_default DESC, id LIMIT 1`, householdID, memberID).Scan(&id)
	if err == nil {
		return affected(s.q.ExecContext(ctx, `UPDATE bank_accounts SET is_default = 1 WHERE id = ? AND household_id = ?`,
			id, householdID))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	base := fmt.Sprintf("Girokonto (%d)", memberID)
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s #%d", base, n)
		}
		var count int
		if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM bank_accounts WHERE household_id = ? AND name = ?`,
			householdID, name).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			continue
		}
		return affected(s.q.ExecContext(ctx, `INSERT INTO bank_accounts (household_id, member_id, name, is_default)
			SELECT ?, id, ?, 1 FROM members WHERE household_id = ? AND id = ?`, householdID, name, householdID, memberID))
	}
}

// SetDefaultBankAccount changes only the preferred account, never payment ownership.
func (s *Store) SetDefaultBankAccount(ctx context.Context, householdID, id int64) error {
	return s.withTx(ctx, func(tx *Store) error {
		var memberID int64
		err := tx.q.QueryRowContext(ctx, `SELECT member_id FROM bank_accounts WHERE household_id = ? AND id = ?`,
			householdID, id).Scan(&memberID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `UPDATE bank_accounts SET is_default = 0 WHERE household_id = ? AND member_id = ?`,
			householdID, memberID); err != nil {
			return err
		}
		return affected(tx.q.ExecContext(ctx, `UPDATE bank_accounts SET is_default = 1 WHERE household_id = ? AND id = ?`,
			householdID, id))
	})
}

// RenameBankAccount changes its label without changing historical account IDs.
func (s *Store) RenameBankAccount(ctx context.Context, householdID, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return ErrInvalid
	}
	return s.withTx(ctx, func(tx *Store) error {
		if err := tx.checkBankAccountName(ctx, householdID, id, name); err != nil {
			return err
		}
		return affected(tx.q.ExecContext(ctx, `UPDATE bank_accounts SET name = ? WHERE household_id = ? AND id = ?`,
			name, householdID, id))
	})
}

func (s *Store) checkBankAccountName(ctx context.Context, householdID, id int64, name string) error {
	var count int
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM bank_accounts WHERE household_id = ? AND name = ? AND id != ?`,
		householdID, name, id).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrBankAccountName
	}
	return nil
}
