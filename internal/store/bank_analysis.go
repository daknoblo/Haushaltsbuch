package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"unicode/utf8"
)

// BankAnalysis is advisory metadata, never an additional financial record.
type BankAnalysis struct {
	Key         string
	CategoryID  int64
	CostNature  CostNature
	BudgetClass BudgetClass
	Reason      string
}

// Valid bounds advisory metadata independently of the payment's financial fields.
func (a BankAnalysis) Valid() bool {
	key, err := hex.DecodeString(a.Key)
	return err == nil && len(key) == sha256.Size && a.CategoryID >= 0 &&
		(a.CostNature == "" || a.CostNature.Valid()) &&
		(a.BudgetClass == "" || a.BudgetClass.Valid()) &&
		utf8.RuneCountInString(a.Reason) > 0 && utf8.RuneCountInString(a.Reason) <= 400
}

// BankAnalysisUpdate binds a result to the exact payment sent for classification.
type BankAnalysisUpdate struct {
	Expected BankTransaction
	Analysis BankAnalysis
}

// SaveBankAnalyses rejects stale results after edits, decisions or model changes.
func (s *Store) SaveBankAnalyses(ctx context.Context, householdID int64, deployment string, categories []Category, updates []BankAnalysisUpdate) error {
	return s.withTx(ctx, func(tx *Store) error {
		currentModel, err := tx.AIDeployment(ctx)
		if err != nil {
			return err
		}
		if deployment != currentModel || len(updates) == 0 || len(updates) > 50 {
			return ErrInvalid
		}
		currentCategories, err := tx.ListCategories(ctx, householdID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(categories, currentCategories) {
			return ErrInvalid
		}
		ledger, err := tx.BankLedger(ctx, householdID)
		if err != nil {
			return err
		}
		current := map[int64]BankTransaction{}
		for _, p := range ledger.Transactions {
			current[p.ID] = p
		}
		seen := map[int64]bool{}
		for _, update := range updates {
			p, exists := current[update.Expected.ID]
			if !exists || seen[p.ID] || p.Decision != "pending" || !reflect.DeepEqual(p, update.Expected) || !update.Analysis.Valid() {
				return ErrInvalid
			}
			seen[p.ID] = true
			if err := tx.ensureOpenMonth(ctx, householdID, p.BookedOn[:7]); err != nil {
				return err
			}
			if update.Analysis.CategoryID != 0 {
				proposed := p
				proposed.CategoryID = update.Analysis.CategoryID
				if err := tx.validateBankReferences(ctx, householdID, proposed); err != nil {
					return err
				}
			}
			encoded, err := json.Marshal(update.Analysis)
			if err != nil {
				return err
			}
			if err := affected(tx.q.ExecContext(ctx, `UPDATE bank_transactions SET analysis = ? WHERE household_id = ? AND id = ? AND decision = 'pending'`,
				string(encoded), householdID, p.ID)); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReviewBankBatch validates every decision against one snapshot and commits all
// changes together. It never allows duplicate overrides or reopening history.
// The prepare callback must be read-only and must not perform network I/O.
func (s *Store) ReviewBankBatch(ctx context.Context, householdID int64, prepare func(HouseholdSnapshot) ([]BankTransaction, error)) error {
	return s.withTx(ctx, func(tx *Store) error {
		snapshot, err := tx.readHousehold(ctx, householdID)
		if err != nil {
			return err
		}
		payments, err := prepare(snapshot)
		if err != nil {
			return err
		}
		if len(payments) == 0 || len(payments) > 1000 {
			return ErrInvalid
		}
		pending := map[int64]bool{}
		for _, p := range snapshot.Bank.Transactions {
			pending[p.ID] = p.Decision == "pending"
		}
		for _, p := range payments {
			if !pending[p.ID] || (p.Decision != "pending" && p.Decision != "accepted" && p.Decision != "transfer") {
				return ErrInvalid
			}
			pending[p.ID] = false
			if err := tx.ReviewBankTransaction(ctx, householdID, p, false); err != nil {
				return err
			}
		}
		return nil
	})
}
