package calc

import (
	"math/big"
	"sort"
	"strconv"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// These reference operations deliberately do not call the production
// normalization, allocation, activity or aggregation functions.
func referenceFactor(b store.Booking) *big.Rat {
	numerator, denominator := int64(1), int64(1)
	switch b.Frequency {
	case store.FreqWeekly:
		numerator, denominator = 52, 12
	case store.FreqQuarterly:
		denominator = 3
	case store.FreqYearly:
		denominator = 12
	}
	if b.Frequency != store.FreqOnce {
		denominator *= int64(b.Interval)
	}
	return big.NewRat(numerator, denominator)
}

func referenceDecimal(v float64) *big.Rat {
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	if !ok {
		panic("non-finite allocation after source validation")
	}
	return r
}

func referenceRounded(v *big.Rat) int64 {
	twice := new(big.Int).Lsh(new(big.Int).Set(v.Num()), 1)
	twice.Add(twice, v.Denom())
	return twice.Quo(twice, new(big.Int).Lsh(new(big.Int).Set(v.Denom()), 1)).Int64()
}

func referenceAllocate(raw map[int64]*big.Rat, target int64) map[int64]int64 {
	type remainder struct {
		id       int64
		fraction *big.Rat
	}
	ranked := make([]remainder, 0, len(raw))
	out := make(map[int64]int64, len(raw))
	for id, v := range raw {
		out[id] = new(big.Int).Quo(v.Num(), v.Denom()).Int64()
		target -= out[id]
		ranked = append(ranked, remainder{id, new(big.Rat).Sub(v, big.NewRat(out[id], 1))})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if c := ranked[i].fraction.Cmp(ranked[j].fraction); c != 0 {
			return c > 0
		}
		return ranked[i].id < ranked[j].id
	})
	for _, v := range ranked {
		if target <= 0 {
			break
		}
		out[v.id]++
		target--
	}
	return out
}

func referenceActive(b store.Booking, month string) bool {
	if b.Frequency == store.FreqOnce {
		return b.StartsOn[:7] == month
	}
	return (b.StartsOn == "" || b.StartsOn[:7] <= month) && (b.EndsOn == "" || b.EndsOn[:7] >= month)
}

func referenceMonth(s store.HouseholdSnapshot, month, current string, basis Basis) []bookingValue {
	actual := basis == BasisActual
	for _, closed := range s.Bank.ClosedMonths {
		if basis == BasisHybrid && closed == month && month < current {
			actual = true
		}
	}
	var out []bookingValue
	if actual {
		owners := make(map[int64]int64, len(s.Bank.Accounts))
		for _, account := range s.Bank.Accounts {
			owners[account.ID] = account.MemberID
		}
		payers := make(map[int64]int64, len(s.Bank.Imports))
		for _, batch := range s.Bank.Imports {
			payers[batch.ID] = owners[batch.AccountID]
		}
		for _, p := range s.Bank.Transactions {
			if p.Decision != "accepted" || p.BookedOn[:7] != month {
				continue
			}
			payer := payers[p.ImportID]
			v := bookingValue{Booking: store.Booking{ID: -p.ID, Name: p.Name, Direction: p.Direction, CategoryID: p.CategoryID, CostNature: p.CostNature, BudgetClass: p.BudgetClass, PayerMemberID: &payer, Settle: p.Settle},
				Cents: p.AmountCents, Shares: make(map[int64]int64), Complete: true}
			for _, sp := range p.Splits {
				v.Shares[sp.MemberID] = sp.Cents
			}
			// Positive payments do not make a zero share an expense carrier.
			if p.AmountCents > 0 {
				for id, cents := range v.Shares {
					if cents == 0 {
						delete(v.Shares, id)
					}
				}
			}
			out = append(out, v)
		}
		return out
	}
	for _, row := range s.Bookings {
		b := row.Booking
		if !referenceActive(b, month) {
			continue
		}
		amount := b.AmountCents
		if b.Frequency != store.FreqOnce {
			for _, o := range row.Overrides {
				if (o.StartsOn == "" || o.StartsOn[:7] <= month) && (o.EndsOn == "" || o.EndsOn[:7] >= month) {
					amount = o.AmountCents
				}
			}
		}
		v := bookingValue{Booking: b, Cents: referenceRounded(new(big.Rat).Mul(big.NewRat(amount, 1), referenceFactor(b)))}
		raw := make(map[int64]*big.Rat, len(row.Splits))
		weights, assigned := new(big.Rat), new(big.Rat)
		for _, sp := range row.Splits {
			weights.Add(weights, referenceDecimal(sp.Value))
			value := new(big.Rat)
			switch b.SplitMode {
			case store.SplitPercent:
				if sp.Value == 0 {
					continue
				}
				value.Mul(big.NewRat(v.Cents, 100), referenceDecimal(sp.Value))
			case store.SplitFixed:
				if sp.Value == 0 && amount != 0 {
					continue
				}
				value.Mul(referenceDecimal(sp.Value), referenceFactor(b))
			default:
				value.SetFrac64(v.Cents, int64(len(row.Splits)))
			}
			raw[sp.MemberID] = value
			assigned.Add(assigned, value)
		}
		v.Complete = len(row.Splits) > 0
		if b.SplitMode == store.SplitFixed {
			v.Complete = weights.Cmp(big.NewRat(amount, 1)) == 0
		}
		if b.SplitMode == store.SplitPercent {
			v.Complete = weights.Cmp(big.NewRat(100, 1)) == 0
		}
		target := referenceRounded(assigned)
		if v.Complete && len(raw) > 0 {
			target = v.Cents
		}
		v.Shares = referenceAllocate(raw, target)
		v.Unassigned = v.Cents - sumCents(v.Shares)
		out = append(out, v)
	}
	return out
}

func referencePeriod(months map[string][]bookingValue, period []string) []bookingValue {
	totals := make(map[int64]bookingValue)
	for _, month := range period {
		for _, v := range months[month] {
			total, ok := totals[v.Booking.ID]
			if !ok {
				total = bookingValue{Booking: v.Booking, Shares: make(map[int64]int64), Complete: true}
			}
			total.Cents += v.Cents
			total.Complete = total.Complete && v.Complete
			for id, cents := range v.Shares {
				total.Shares[id] += cents
			}
			totals[v.Booking.ID] = total
		}
	}
	n := int64(len(period))
	amounts := make(map[int64]int64)
	for _, direction := range []store.Direction{store.DirIncome, store.DirExpense} {
		raw := make(map[int64]*big.Rat)
		var sum int64
		for id, v := range totals {
			if v.Booking.Direction == direction {
				raw[id] = big.NewRat(v.Cents, n)
				sum += v.Cents
			}
		}
		for id, cents := range referenceAllocate(raw, sum/n) {
			amounts[id] = cents
		}
	}
	out := make([]bookingValue, 0, len(totals))
	for id, v := range totals {
		v.Cents = amounts[id]
		raw := make(map[int64]*big.Rat)
		var sum int64
		for member, cents := range v.Shares {
			raw[member] = big.NewRat(cents, n)
			sum += cents
		}
		target := sum / n
		if v.Complete {
			target = v.Cents
		}
		v.Shares = referenceAllocate(raw, target)
		v.Unassigned = v.Cents - sumCents(v.Shares)
		out = append(out, v)
	}
	return out
}

func referenceReport(values []bookingValue, member int64) MonthReport {
	out := MonthReport{ByCostNature: make(map[store.CostNature]int64), ByBudgetClass: make(map[store.BudgetClass]int64)}
	income, expense := map[int64]int64{}, map[int64]int64{}
	for _, v := range values {
		cents := v.Cents
		if member != Everyone {
			cents = v.Shares[member]
		}
		if v.Booking.Direction == store.DirIncome {
			out.IncomeCents += cents
			income[v.Booking.CategoryID] += cents
		} else {
			out.ExpenseCents += cents
			expense[v.Booking.CategoryID] += cents
			out.ByCostNature[v.Booking.CostNature] += cents
			out.ByBudgetClass[v.Booking.BudgetClass] += cents
			if member == Everyone {
				out.UnassignedCents += v.Unassigned
			}
		}
	}
	out.BalanceCents = out.IncomeCents - out.ExpenseCents
	for id, cents := range income {
		out.IncomeCategories = append(out.IncomeCategories, LabeledTotal{Key: id, Cents: cents})
	}
	for id, cents := range expense {
		out.Categories = append(out.Categories, LabeledTotal{Key: id, Cents: cents})
	}
	return out
}
