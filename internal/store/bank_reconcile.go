package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// BankSourceFingerprint is an unambiguous identity for supplied payment fields.
// It excludes classification and review state and survives later corrections.
func BankSourceFingerprint(b BankTransaction) string {
	normalize := func(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
	sum := sha256.Sum256([]byte(fmt.Sprintf("%q|%q|%d|%q|%q", b.BookedOn, b.Direction, b.AmountCents, normalize(b.Name), normalize(b.Reference))))
	return hex.EncodeToString(sum[:])
}

// BankReconciliation counts rows, not files. ReviewMonth targets current rows.
type BankReconciliation struct {
	Added, Existing, Preserved int
	ReviewMonth                string
}

// ReconcileBankImports compares against all current decisions in the selected
// accounts. Existing records are untouched; every new row remains pending.
func (s *Store) ReconcileBankImports(ctx context.Context, householdID int64, batches []BankImportBatch) (BankReconciliation, error) {
	var out BankReconciliation
	count := 0
	seen := map[int64]bool{}
	for _, batch := range batches {
		digest, err := hex.DecodeString(batch.Import.Digest)
		count += len(batch.Payments)
		if err != nil || len(digest) != sha256.Size || len(batch.Payments) == 0 || count > 1000 ||
			batch.Import.Filename == "" || utf8.RuneCountInString(batch.Import.Filename) > 255 || seen[batch.Import.AccountID] {
			return out, ErrInvalid
		}
		seen[batch.Import.AccountID] = true
		for _, payment := range batch.Payments {
			payment.Decision = "pending"
			if err := ValidateBankTransaction(payment); err != nil {
				return out, err
			}
		}
	}
	if count == 0 {
		return out, ErrInvalid
	}
	err := s.withTx(ctx, func(tx *Store) error {
		ledger, err := tx.BankLedger(ctx, householdID)
		if err != nil {
			return err
		}
		accounts := map[int64]bool{}
		for _, account := range ledger.Accounts {
			accounts[account.ID] = true
		}
		importAccounts := map[int64]int64{}
		for _, batch := range ledger.Imports {
			importAccounts[batch.ID] = batch.AccountID
		}
		existingMonth, addedMonth, correctedMonth := "", "", ""
		for _, batch := range batches {
			if !accounts[batch.Import.AccountID] {
				return ErrNotFound
			}
			var existing []BankTransaction
			for _, payment := range ledger.Transactions {
				if importAccounts[payment.ImportID] == batch.Import.AccountID {
					existing = append(existing, payment)
				}
			}
			matched, err := matchBankPayments(ctx, batch.Payments, existing)
			if err != nil {
				return err
			}
			var batchID int64
			for i, payment := range batch.Payments {
				if old, ok := matched[i]; ok {
					out.Existing++
					if BankSourceFingerprint(old) != BankSourceFingerprint(payment) {
						out.Preserved++
						if correctedMonth == "" {
							correctedMonth = old.BookedOn[:7]
						}
					}
					if existingMonth == "" {
						existingMonth = old.BookedOn[:7]
					}
					continue
				}
				if err := tx.ensureOpenMonth(ctx, householdID, payment.BookedOn[:7]); err != nil {
					return err
				}
				if batchID == 0 {
					for _, saved := range ledger.Imports {
						if saved.AccountID == batch.Import.AccountID && saved.Digest == batch.Import.Digest {
							batchID = saved.ID
							break
						}
					}
					if batchID == 0 {
						res, err := tx.q.ExecContext(ctx, `INSERT INTO bank_imports (household_id, account_id, digest, filename, created_at) VALUES (?, ?, ?, ?, ?)`,
							householdID, batch.Import.AccountID, batch.Import.Digest, batch.Import.Filename, now())
						if err != nil {
							return err
						}
						batchID, err = res.LastInsertId()
						if err != nil {
							return err
						}
					}
				}
				payment.ID, payment.ImportID, payment.Decision = 0, batchID, "pending"
				payment.Splits, payment.TagIDs = nil, nil
				payment.SourceFingerprint = BankSourceFingerprint(payment)
				if err := tx.validateBankReferences(ctx, householdID, payment); err != nil {
					return err
				}
				if err := tx.insertBankTransaction(ctx, householdID, payment); err != nil {
					return err
				}
				out.Added++
				if addedMonth == "" {
					addedMonth = payment.BookedOn[:7]
				}
			}
		}
		out.ReviewMonth = addedMonth
		if out.ReviewMonth == "" {
			out.ReviewMonth = correctedMonth
		}
		if out.ReviewMonth == "" {
			out.ReviewMonth = existingMonth
		}
		return nil
	})
	if err != nil {
		return BankReconciliation{}, err
	}
	return out, nil
}

// Matching is count-aware. Augmenting paths keep an original/current identity
// alias from stealing another incoming row's only match.
func matchBankPayments(ctx context.Context, incoming, existing []BankTransaction) (map[int]BankTransaction, error) {
	candidates := map[string][]int{}
	for i, payment := range existing {
		current := BankSourceFingerprint(payment)
		candidates[current] = append(candidates[current], i)
		if payment.SourceFingerprint != "" && payment.SourceFingerprint != current {
			candidates[payment.SourceFingerprint] = append(candidates[payment.SourceFingerprint], i)
		}
	}
	assigned := map[int]string{}
	var assign func(string, map[string]bool) bool
	assign = func(key string, visited map[string]bool) bool {
		if visited[key] || ctx.Err() != nil {
			return false
		}
		visited[key] = true
		for _, index := range candidates[key] {
			if assigned[index] == "" {
				assigned[index] = key
				return true
			}
		}
		for _, index := range candidates[key] {
			if previous := assigned[index]; previous != key && assign(previous, visited) {
				assigned[index] = key
				return true
			}
		}
		return false
	}
	for _, payment := range incoming {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		assign(BankSourceFingerprint(payment), map[string]bool{})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	byKey := map[string][]BankTransaction{}
	// Preserve ledger order so a repeated upload yields a stable review target.
	for i, payment := range existing {
		if key := assigned[i]; key != "" {
			byKey[key] = append(byKey[key], payment)
		}
	}
	matched := make(map[int]BankTransaction)
	for i, payment := range incoming {
		key := BankSourceFingerprint(payment)
		if len(byKey[key]) > 0 {
			matched[i] = byKey[key][0]
			byKey[key] = byKey[key][1:]
		}
	}
	return matched, nil
}
