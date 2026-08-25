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

// ShareLine is one expense the settlement was built from: what it costs in an
// average month, who fronts it and how much of it each member carries. It is
// what makes a transfer traceable — a bill that is not divided moves no money
// between members, and only the line shows that.
type ShareLine struct {
	Booking      store.Booking
	Payer        store.Member
	MonthlyCents int64
	// Shares holds every member's carried amount, the payer included.
	Shares map[int64]int64
	// Carriers is how many members carry the booking, i.e. the "divided by".
	Carriers int
}

// Shared reports whether more than one member carries the booking.
func (l ShareLine) Shared() bool { return l.Carriers > 1 }

// ShareOf returns what a member carries of this booking.
func (l ShareLine) ShareOf(member int64) int64 { return l.Shares[member] }

// SettlementReport is who owes whom plus the lines that produced it.
type SettlementReport struct {
	Positions []MemberPosition
	Transfers []Transfer
	Lines     []ShareLine
}

// Carried is what a scope shoulders, split by whether the cost is divided.
// The two add up to that scope's expenses.
type Carried struct {
	SharedCents int64
	SoleCents   int64
}

// CarriedBy sums what a member carries, or the whole household for Everyone.
// In a member scope a divided booking counts with that member's share only,
// which is what "half the rent plus what I carry alone" means.
func (r SettlementReport) CarriedBy(member int64) Carried {
	var out Carried
	for _, l := range r.LinesFor(member) {
		cents := l.MonthlyCents
		if member != Everyone {
			cents = l.ShareOf(member)
		}
		if l.Shared() {
			out.SharedCents += cents
			continue
		}
		out.SoleCents += cents
	}
	return out
}

// LinesFor returns the expenses a member carries a part of, all of them for
// Everyone.
func (r SettlementReport) LinesFor(member int64) []ShareLine {
	if member == Everyone {
		return r.Lines
	}
	out := make([]ShareLine, 0, len(r.Lines))
	for _, l := range r.Lines {
		if l.ShareOf(member) != 0 {
			out = append(out, l)
		}
	}
	return out
}

// DebtLine is one booking's contribution to what one member owes another, seen
// from the debtor: positive for a share the other one fronted, negative for
// what the debtor fronted on the other's behalf.
type DebtLine struct {
	Booking store.Booking
	Payer   store.Member
	Cents   int64
}

// DebtBreakdown is what a payment is made of.
type DebtBreakdown struct {
	Lines []DebtLine
	// TotalCents is what the lines add up to, i.e. the plain balance between
	// the two. It equals the transfer unless the household has more than two
	// members and the payment was netted across all of them.
	TotalCents int64
}

// Between explains the balance between two members booking by booking, so a
// payment can be checked instead of believed.
func (r SettlementReport) Between(debtor, creditor int64) DebtBreakdown {
	var out DebtBreakdown
	for _, l := range r.Lines {
		var cents int64
		switch l.Payer.ID {
		case creditor:
			cents = l.ShareOf(debtor)
		case debtor:
			cents = -l.ShareOf(creditor)
		}
		if cents == 0 {
			continue
		}
		out.Lines = append(out.Lines, DebtLine{Booking: l.Booking, Payer: l.Payer, Cents: cents})
		out.TotalCents += cents
	}
	return out
}

// Settlement reports who owes whom after a period. Whoever fronts a bill pays
// it in full, so anyone who paid more than their own share gets the difference
// back. Only expenses count: income is nobody's debt to the household, and a
// booking without a payer is left out because there is no one to reimburse.
func Settlement(d Data, months []string) SettlementReport {
	active := activeMonths(d, months)
	n := int64(len(active))
	if n == 0 || len(d.Members) == 0 {
		return SettlementReport{}
	}

	paid := make(map[int64]int64, len(d.Members))
	owed := make(map[int64]int64, len(d.Members))
	totals := make(map[int64]int64, len(d.Bookings))
	perBooking := make(map[int64]map[int64]int64, len(d.Bookings))
	order := make([]store.Booking, 0, len(d.Bookings))

	for _, m := range active {
		for _, b := range d.Bookings {
			if b.Direction != store.DirExpense || b.PayerMemberID == nil || !ActiveIn(b, m) {
				continue
			}
			amount := float64(AmountFor(b, d.Overrides[b.ID], m)) * monthlyFactor(b)
			paid[*b.PayerMemberID] += round(amount)
			if _, seen := perBooking[b.ID]; !seen {
				perBooking[b.ID] = make(map[int64]int64, len(d.Members))
				order = append(order, b)
			}
			totals[b.ID] += round(amount)
			shares, _ := allocate(amount, b, d.Splits[b.ID], d.Members)
			for id, v := range shares {
				owed[id] += round(v)
				perBooking[b.ID][id] += round(v)
			}
		}
	}

	rep := SettlementReport{Positions: make([]MemberPosition, 0, len(d.Members))}
	for _, m := range d.Members {
		p := MemberPosition{Member: m, PaidCents: paid[m.ID] / n, OwedCents: owed[m.ID] / n}
		p.NetCents = p.PaidCents - p.OwedCents
		rep.Positions = append(rep.Positions, p)
	}
	rep.Transfers = transfers(rep.Positions)
	rep.Lines = shareLines(d, order, totals, perBooking, n)
	return rep
}

// shareLines turns the accumulated per-booking sums into monthly averages,
// largest first, so the list reads like the settlement it explains.
func shareLines(d Data, order []store.Booking, totals map[int64]int64, shares map[int64]map[int64]int64, n int64) []ShareLine {
	byID := make(map[int64]store.Member, len(d.Members))
	for _, m := range d.Members {
		byID[m.ID] = m
	}

	out := make([]ShareLine, 0, len(order))
	for _, b := range order {
		line := ShareLine{
			Booking:      b,
			Payer:        byID[*b.PayerMemberID],
			MonthlyCents: totals[b.ID] / n,
			Shares:       make(map[int64]int64, len(d.Members)),
		}
		for _, m := range d.Members {
			cents := shares[b.ID][m.ID] / n
			line.Shares[m.ID] = cents
			if cents != 0 {
				line.Carriers++
			}
		}
		if line.MonthlyCents == 0 {
			continue
		}
		out = append(out, line)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].MonthlyCents > out[j].MonthlyCents })
	return out
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
