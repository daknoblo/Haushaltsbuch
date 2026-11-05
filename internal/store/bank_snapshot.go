package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

func (s *Store) importBankLedger(ctx context.Context, householdID int64, l BankLedger) error {
	for _, a := range l.Accounts {
		if a.ID <= 0 || a.Name == "" || len(a.Name) > 480 {
			return ErrBadSnapshot
		}
		if err := affected(s.q.ExecContext(ctx, `INSERT INTO bank_accounts (id, household_id, member_id, name, is_default)
			SELECT ?, ?, id, ?, ? FROM members WHERE id = ? AND household_id = ?`,
			a.ID, householdID, a.Name, a.Default, a.MemberID, householdID)); err != nil {
			return err
		}
	}
	for _, b := range l.Imports {
		digest, err := hex.DecodeString(b.Digest)
		if err != nil || len(digest) != sha256.Size || b.ID <= 0 || len(b.Filename) > 1020 {
			return ErrBadSnapshot
		}
		if err := affected(s.q.ExecContext(ctx, `INSERT INTO bank_imports (id, household_id, account_id, digest, filename, created_at)
			SELECT ?, ?, id, ?, ?, ? FROM bank_accounts WHERE id = ? AND household_id = ?`,
			b.ID, householdID, b.Digest, b.Filename, b.CreatedAt, b.AccountID, householdID)); err != nil {
			return err
		}
	}
	for _, b := range l.Transactions {
		if b.ID <= 0 || b.ImportID <= 0 {
			return ErrBadSnapshot
		}
		if err := ValidateBankTransaction(b); err != nil {
			return fmt.Errorf("%w: %w", ErrBadSnapshot, err)
		}
		if b.SourceFingerprint != "" {
			key, err := hex.DecodeString(b.SourceFingerprint)
			if err != nil || len(key) != sha256.Size {
				return ErrBadSnapshot
			}
			b.SourceFingerprint = strings.ToLower(b.SourceFingerprint)
		}
		if err := s.validateBankReferences(ctx, householdID, b); err != nil {
			return err
		}
		if err := s.insertBankTransaction(ctx, householdID, b); err != nil {
			return err
		}
	}
	for _, month := range l.ClosedMonths {
		if err := s.SetMonthReconciled(ctx, householdID, month, true); err != nil {
			return err
		}
	}
	return nil
}
