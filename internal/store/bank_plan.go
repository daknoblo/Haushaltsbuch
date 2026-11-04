package store

import (
	"context"
	"time"
)

// CreatePlanFromPayment is an explicit, idempotency-guarded promotion, never an
// automatic side effect of accepting an actual payment.
func (s *Store) CreatePlanFromPayment(ctx context.Context, householdID, transactionID int64, frequency Frequency, start string) error {
	if !frequency.Valid() || start == "" || ValidateDateRange(start, "") != nil || start < time.Now().Format("2006-01-02") {
		return ErrInvalid
	}
	return s.withTx(ctx, func(tx *Store) error {
		l, err := tx.BankLedger(ctx, householdID)
		if err != nil {
			return err
		}
		for _, p := range l.Transactions {
			if p.ID != transactionID {
				continue
			}
			if p.Decision != "accepted" || p.PlanID != 0 {
				return ErrInvalid
			}
			var owner int64
			for _, b := range l.Imports {
				if b.ID != p.ImportID {
					continue
				}
				for _, a := range l.Accounts {
					if a.ID == b.AccountID {
						owner = a.MemberID
					}
				}
			}
			if owner == 0 {
				return ErrInvalid
			}
			b := Booking{
				HouseholdID: householdID, CategoryID: p.CategoryID, PayerMemberID: &owner,
				Name: p.Name, Direction: p.Direction, AmountCents: p.AmountCents, Frequency: frequency,
				Interval: 1, DuePoint: DueStart, StartsOn: start, CostNature: p.CostNature, BudgetClass: p.BudgetClass,
				SplitMode: SplitFixed, Settle: p.Settle,
			}
			var splits []SplitInput
			for _, sp := range p.Splits {
				splits = append(splits, SplitInput{MemberID: sp.MemberID, Value: float64(sp.Cents)})
			}
			created, err := tx.CreateBooking(ctx, b, splits, p.TagIDs)
			if err != nil {
				return err
			}
			return affected(tx.q.ExecContext(ctx, `UPDATE bank_transactions SET plan_id = ?
				WHERE id = ? AND household_id = ? AND plan_id IS NULL`, created.ID, p.ID, householdID))
		}
		return ErrNotFound
	})
}
