package calc

import (
	"maps"
	"math/big"
	"slices"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Basis chooses planning, actual payments or the closed-month hybrid.
type Basis string

// Reporting bases never change the underlying stored plans.
const (
	BasisPlan   Basis = "plan"
	BasisActual Basis = "actual"
	BasisHybrid Basis = "hybrid"
)

// Valid reports whether the basis is supported.
func (b Basis) Valid() bool { return b == BasisPlan || b == BasisActual || b == BasisHybrid }

// WithBankLedger builds a report-only view. Negative IDs separate actual rows
// from plan IDs without persisting synthetic bookings or changing their dates.
func WithBankLedger(d Data, l store.BankLedger, basis Basis, currentMonth string) Data {
	d.evaluated = nil
	d.Basis, d.CurrentMonth = basis, currentMonth
	d.ClosedMonths = make(map[string]bool, len(l.ClosedMonths))
	for _, month := range l.ClosedMonths {
		d.ClosedMonths[month] = true
	}
	d.Bookings = slices.Clone(d.Bookings)
	d.Splits = maps.Clone(d.Splits)
	d.TagLinks = maps.Clone(d.TagLinks)
	if d.Splits == nil {
		d.Splits = make(map[int64][]store.BookingSplit)
	}
	if d.TagLinks == nil {
		d.TagLinks = make(map[int64][]int64)
	}
	owners := make(map[int64]int64)
	for _, a := range l.Accounts {
		owners[a.ID] = a.MemberID
	}
	importOwners := make(map[int64]int64)
	for _, i := range l.Imports {
		importOwners[i.ID] = owners[i.AccountID]
	}
	for _, t := range l.Transactions {
		if t.Decision != "accepted" {
			continue
		}
		payer := importOwners[t.ImportID]
		b := store.Booking{
			ID: -t.ID, Name: t.Name, Note: t.Reference, Direction: t.Direction,
			CategoryID: t.CategoryID, AmountCents: t.AmountCents, PayerMemberID: &payer,
			Frequency: store.FreqOnce, Interval: 1, StartsOn: t.BookedOn,
			CostNature: t.CostNature, BudgetClass: t.BudgetClass, SplitMode: store.SplitFixed, Settle: t.Settle,
		}
		d.Bookings = append(d.Bookings, b)
		for _, sp := range t.Splits {
			d.Splits[b.ID] = append(d.Splits[b.ID], store.BookingSplit{BookingID: b.ID, MemberID: sp.MemberID, Value: float64(sp.Cents)})
		}
		d.TagLinks[b.ID] = slices.Clone(t.TagIDs)
	}
	return d
}

func (d Data) actualMonth(month string) bool {
	return d.Basis == BasisActual ||
		(d.Basis == BasisHybrid && month < d.CurrentMonth && d.ClosedMonths[month])
}

func (d Data) activeIn(b store.Booking, month string) bool {
	return ActiveIn(b, month) && (b.ID < 0) == d.actualMonth(month)
}

// Source identifies incomplete actuals and estimated history explicitly.
func (d Data) Source(month string) string {
	if d.actualMonth(month) {
		if d.ClosedMonths[month] {
			return "actual"
		}
		return "actual_partial"
	}
	if d.Basis == BasisHybrid && month < d.CurrentMonth {
		return "plan_estimate"
	}
	return "plan"
}

// ActualAllocation proportionally carries a plan's allocation to a real
// payment, using the same stable cent balancing as the other reports.
func ActualAllocation(plan store.Booking, splits []store.BookingSplit, amount int64) []store.BankSplit {
	if !completeSplit(plan, splits, plan.AmountCents) {
		return nil
	}
	raw := make(map[int64]*big.Rat, len(splits))
	total := new(big.Rat)
	for _, sp := range splits {
		weight := clampAmount(sp.Value)
		if plan.SplitMode == store.SplitPercent {
			weight = ClampPercent(sp.Value)
		}
		if plan.SplitMode == store.SplitEqual {
			weight = 1
		}
		if weight > 0 || (weight == 0 && amount == 0) {
			value := decimalAmount(weight)
			addAmount(raw, sp.MemberID, value)
			total.Add(total, value)
		}
	}
	if total.Sign() == 0 && amount != 0 {
		return nil
	}
	for id, weight := range raw {
		if total.Sign() > 0 {
			raw[id] = new(big.Rat).Quo(new(big.Rat).Mul(big.NewRat(amount, 1), weight), total)
		}
	}
	values := balancedCents(raw, amount)
	ids := slices.Sorted(maps.Keys(values))
	out := make([]store.BankSplit, 0, len(ids))
	for _, id := range ids {
		out = append(out, store.BankSplit{MemberID: id, Cents: values[id]})
	}
	return out
}
