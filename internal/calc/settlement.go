package calc

import (
	"sort"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// MemberPosition is what one member fronted and what they actually owe for a
// period, both as a monthly average so it matches the rest of the dashboard.
type MemberPosition struct {
	Member    store.Member
	PaidCents int64
	OwedCents int64
	// NetCents is positive when the member is owed money.
	NetCents int64
}

// Transfer is one payment that squares the books.
type Transfer struct {
	From  store.Member
	To    store.Member
	Cents int64
}

// Settlement reports who owes whom after a period. Whoever fronts a bill pays
// it in full, so anyone who paid more than their own share gets the difference
// back. Only expenses count: income is nobody's debt to the household, and a
// booking without a payer is left out because there is no one to reimburse.
func Settlement(d Data, months []string) ([]MemberPosition, []Transfer) {
	active := activeMonths(d, months)
	n := int64(len(active))
	if n == 0 || len(d.Members) == 0 {
		return nil, nil
	}

	paid := make(map[int64]int64, len(d.Members))
	owed := make(map[int64]int64, len(d.Members))
	for _, m := range active {
		for _, b := range d.Bookings {
			if b.Direction != store.DirExpense || b.PayerMemberID == nil || !ActiveIn(b, m) {
				continue
			}
			amount := float64(AmountFor(b, d.Overrides[b.ID], m)) * monthlyFactor(b)
			paid[*b.PayerMemberID] += round(amount)
			shares, _ := allocate(amount, b, d.Splits[b.ID], d.Members)
			for id, v := range shares {
				owed[id] += round(v)
			}
		}
	}

	positions := make([]MemberPosition, 0, len(d.Members))
	for _, m := range d.Members {
		p := MemberPosition{Member: m, PaidCents: paid[m.ID] / n, OwedCents: owed[m.ID] / n}
		p.NetCents = p.PaidCents - p.OwedCents
		positions = append(positions, p)
	}
	return positions, transfers(positions)
}

// transfers turns net positions into payments, always sending the largest debt
// to the largest claim so at most one payment per member less one is needed.
func transfers(positions []MemberPosition) []Transfer {
	type side struct {
		member store.Member
		cents  int64
	}
	var debtors, creditors []side
	for _, p := range positions {
		switch {
		case p.NetCents < 0:
			debtors = append(debtors, side{p.Member, -p.NetCents})
		case p.NetCents > 0:
			creditors = append(creditors, side{p.Member, p.NetCents})
		}
	}
	sort.SliceStable(debtors, func(i, j int) bool { return debtors[i].cents > debtors[j].cents })
	sort.SliceStable(creditors, func(i, j int) bool { return creditors[i].cents > creditors[j].cents })

	var out []Transfer
	// Rounding each member's share separately can leave a cent adrift, which
	// would otherwise produce a transfer of a single cent.
	const noise = 100
	for i, j := 0, 0; i < len(debtors) && j < len(creditors); {
		amount := min(debtors[i].cents, creditors[j].cents)
		if amount >= noise {
			out = append(out, Transfer{From: debtors[i].member, To: creditors[j].member, Cents: amount})
		}
		debtors[i].cents -= amount
		creditors[j].cents -= amount
		if debtors[i].cents == 0 {
			i++
		}
		if creditors[j].cents == 0 {
			j++
		}
	}
	return out
}
