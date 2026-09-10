package calc

import (
	"sort"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// MemberPosition is what one member fronted and what they actually owe for a
// period, either as a monthly average or as a period total.
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

// ShareLine is one expense the settlement was built from: what it costs,
// who fronts it and how much of it each member carries. It is
// what makes a transfer traceable — a bill that is not divided moves no money
// between members, and only the line shows that.
type ShareLine struct {
	Booking store.Booking
	Payer   store.Member
	// Cents holds a monthly average for Settlement, a period sum for
	// SettlementTotal.
	Cents int64
	// Shares holds every member's carried amount, the payer included.
	Shares map[int64]int64
	Splits []store.BookingSplit
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
	// InvalidBookings have shares that do not cover their amount. No transfer
	// is suggested until these are corrected.
	InvalidBookings []store.Booking
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
		cents := l.Cents
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

// LedgerLine is what one booking does to a member's balance: what they fronted
// for it, less the share they carry themselves.
type LedgerLine struct {
	Booking store.Booking
	// TotalCents is the full booking amount, before splitting.
	TotalCents int64
	// PaidCents is 0 unless the member fronted this booking.
	PaidCents int64
	// OwedCents is the share the member carries.
	OwedCents int64
	NetCents  int64
}

// Ledger lists every booking a member is involved in, so their balance can be
// followed line by line instead of taken on faith. The lines add up to that
// member's position.
func (r SettlementReport) Ledger(member int64) []LedgerLine {
	out := make([]LedgerLine, 0, len(r.Lines))
	for _, l := range r.Lines {
		line := LedgerLine{Booking: l.Booking, TotalCents: l.Cents, OwedCents: l.ShareOf(member)}
		if l.Payer.ID == member {
			line.PaidCents = l.Cents
		}
		if line.PaidCents == 0 && line.OwedCents == 0 {
			continue
		}
		line.NetCents = line.PaidCents - line.OwedCents
		out = append(out, line)
	}
	return out
}

// Settlement reports who owes whom after a period. Whoever fronts a bill pays
// it in full, so anyone who paid more than their own share gets the difference
// back. Only expenses count: income is nobody's debt to the household, a
// booking without a payer has no one to reimburse, one marked as not to be
// settled is deliberately left out, and one nobody carries settles nothing —
// counting what was fronted for it would leave the payer owed by no one.
func Settlement(d Data, months []string) SettlementReport {
	n := int64(len(months))
	if n == 0 {
		return SettlementReport{}
	}
	return settlement(d, months, true)
}

// SettlementTotal adds the normalized monthly amounts without averaging them.
// The caller selects the elapsed months; this is a plan, not a payment log.
func SettlementTotal(d Data, months []string) SettlementReport {
	return settlement(d, months, false)
}

func settlement(d Data, months []string, averaged bool) SettlementReport {
	rep := SettlementReport{Positions: make([]MemberPosition, 0, len(d.Members))}
	known := make(map[int64]store.Member, len(d.Members))
	for _, m := range d.Members {
		known[m.ID] = m
	}
	for _, value := range periodBookingValues(d, months, averaged) {
		b := value.Booking
		if b.Direction != store.DirExpense || !b.Settle || b.PayerMemberID == nil || len(d.Splits[b.ID]) == 0 {
			continue
		}
		payer, valid := known[*b.PayerMemberID]
		valid = valid && value.Complete
		for id, share := range value.Shares {
			_, exists := known[id]
			valid = valid && exists && share >= 0
		}
		if !valid {
			rep.InvalidBookings = append(rep.InvalidBookings, b)
			continue
		}
		if payerCarriesAlone(value) {
			continue
		}
		if value.Cents != 0 {
			rep.Lines = append(rep.Lines, ShareLine{
				Booking: b, Payer: payer, Cents: value.Cents,
				Shares: value.Shares, Splits: d.Splits[b.ID], Carriers: len(value.Shares),
			})
		}
	}
	sort.Slice(rep.Lines, func(i, j int) bool {
		a, b := rep.Lines[i], rep.Lines[j]
		if a.Cents != b.Cents {
			return a.Cents > b.Cents
		}
		if a.Booking.Name != b.Booking.Name {
			return a.Booking.Name < b.Booking.Name
		}
		return a.Booking.ID < b.Booking.ID
	})
	for _, m := range d.Members {
		p := MemberPosition{Member: m}
		for _, l := range rep.Lines {
			if l.Payer.ID == m.ID {
				p.PaidCents += l.Cents
			}
			p.OwedCents += l.ShareOf(m.ID)
		}
		p.NetCents = p.PaidCents - p.OwedCents
		rep.Positions = append(rep.Positions, p)
	}
	if len(rep.InvalidBookings) == 0 {
		rep.Transfers = transfers(rep.Positions)
	}
	return rep
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
	for i, j := 0, 0; i < len(debtors) && j < len(creditors); {
		amount := min(debtors[i].cents, creditors[j].cents)
		out = append(out, Transfer{From: debtors[i].member, To: creditors[j].member, Cents: amount})
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
