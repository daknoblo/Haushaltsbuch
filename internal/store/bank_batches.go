package store

import "context"

// BankImportBatch is one explicitly selected destination for part of a statement.
type BankImportBatch struct {
	Import   BankImport
	Payments []BankTransaction
}

// CreateBankImports commits every mapped source account together, or none.
func (s *Store) CreateBankImports(ctx context.Context, householdID int64, batches []BankImportBatch) error {
	if len(batches) == 0 {
		return ErrInvalid
	}
	count := 0
	seen := make(map[int64]bool, len(batches))
	for _, batch := range batches {
		count += len(batch.Payments)
		if count > 1000 || seen[batch.Import.AccountID] {
			return ErrInvalid
		}
		seen[batch.Import.AccountID] = true
	}
	return s.withTx(ctx, func(tx *Store) error {
		for _, batch := range batches {
			if err := tx.CreateBankImport(ctx, householdID, batch.Import, batch.Payments); err != nil {
				return err
			}
		}
		return nil
	})
}
