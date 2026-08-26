package calc

import (
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// internetPlan is the case the overrides exist for: a contract that costs
// 49.99 € but runs at 10 € for the first six months.
func internetPlan() Data {
	return Data{
		Members:    members(),
		Categories: categories(),
		Bookings: []store.Booking{{
			ID: 1, CategoryID: 20, Direction: store.DirExpense, AmountCents: 4999,
			Frequency: store.FreqMonthly, Interval: 1, StartsOn: "2026-01-01",
			SplitMode: store.SplitEqual, CostNature: store.CostFix, BudgetClass: store.ClassNeed,
		}},
		Overrides: map[int64][]store.BookingOverride{
			1: {{ID: 1, BookingID: 1, StartsOn: "2026-01-01", EndsOn: "2026-06-30", AmountCents: 1000}},
		},
	}
}

func TestOverrideWinsForTheMonthsItCovers(t *testing.T) {
	d := internetPlan()
	if got := BuildMonthReport(d, "2026-03", Everyone).ExpenseCents; got != 1000 {
		t.Errorf("inside the promotion = %d, want 1000", got)
	}
	if got := BuildMonthReport(d, "2026-07", Everyone).ExpenseCents; got != 4999 {
		t.Errorf("after the promotion = %d, want 4999", got)
	}

	// Half a year at 10 €, half at 49.99 € averages in between.
	year := []string{
		"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06",
		"2026-07", "2026-08", "2026-09", "2026-10", "2026-11", "2026-12",
	}
	rep := PeriodReport(d, year, Everyone)
	if rep.ExpenseCents <= 1000 || rep.ExpenseCents >= 4999 {
		t.Errorf("yearly average = %d, want a value between the two prices", rep.ExpenseCents)
	}
}

func TestLaterOverrideWins(t *testing.T) {
	d := internetPlan()
	d.Overrides[1] = append(d.Overrides[1], store.BookingOverride{
		ID: 2, BookingID: 1, StartsOn: "2026-03-01", EndsOn: "2026-03-31", AmountCents: 0,
	})
	if got := BuildMonthReport(d, "2026-03", Everyone).ExpenseCents; got != 0 {
		t.Errorf("overlapping override = %d, want the later one to win with 0", got)
	}
}

// A booking that happens once has a single amount, and the dialog hides the
// overrides along with the rhythm. Honoring one left behind by an earlier
// rhythm would be a discount nobody can see or delete.
func TestOverrideIsIgnoredForAOneOff(t *testing.T) {
	d := internetPlan()
	d.Bookings[0].Frequency = store.FreqOnce
	d.Bookings[0].StartsOn = "2026-03-15"
	if got := BuildMonthReport(d, "2026-03", Everyone).ExpenseCents; got != 4999 {
		t.Errorf("one-off with a leftover override = %d, want the stored 4999", got)
	}
}

// A bill nobody carries settles nothing: counting what was fronted for it would
// report the payer as owed money by no one, next to "everything settled".
func TestUncarriedBookingSettlesNothing(t *testing.T) {
	d := sharedPlan()
	d.Splits = map[int64][]store.BookingSplit{}

	rep := Settlement(d, []string{"2026-03"})
	for _, p := range rep.Positions {
		if p.PaidCents != 0 || p.OwedCents != 0 || p.NetCents != 0 {
			t.Errorf("%s = paid %d, owed %d, net %d, want a flat position",
				p.Member.Name, p.PaidCents, p.OwedCents, p.NetCents)
		}
	}
	if len(rep.Transfers) != 0 {
		t.Errorf("transfers = %d, want none", len(rep.Transfers))
	}
	if got := rep.CarriedBy(Everyone); got.SharedCents != 0 || got.SoleCents != 0 {
		t.Errorf("carried = %+v, want nothing carried", got)
	}
}

// sharedPlan is the case the person view exists for: a rent both share and a
// policy only Anna carries.
func sharedPlan() Data {
	return Data{
		Members:    members(),
		Categories: categories(),
		Bookings: []store.Booking{
			{ID: 1, CategoryID: 20, Direction: store.DirExpense, AmountCents: 100000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitEqual,
				CostNature: store.CostFix, BudgetClass: store.ClassNeed,
				PayerMemberID: memberRef(1), Settle: true},
			{ID: 2, CategoryID: 21, Direction: store.DirExpense, AmountCents: 5000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitPercent,
				CostNature: store.CostFix, BudgetClass: store.ClassNeed,
				PayerMemberID: memberRef(1), Settle: true},
		},
		Splits: map[int64][]store.BookingSplit{
			1: {{BookingID: 1, MemberID: 1}, {BookingID: 1, MemberID: 2}},
			2: {{BookingID: 2, MemberID: 1, Value: 100}},
		},
	}
}

func memberRef(id int64) *int64 { return &id }

func TestPersonViewShowsOwnShareOnly(t *testing.T) {
	d := sharedPlan()

	household := BuildMonthReport(d, "2026-05", Everyone)
	if household.ExpenseCents != 105000 {
		t.Errorf("household expenses = %d, want 105000", household.ExpenseCents)
	}

	// Anna: half the rent plus the whole policy.
	anna := BuildMonthReport(d, "2026-05", 1)
	if anna.ExpenseCents != 55000 {
		t.Errorf("Anna = %d, want 55000", anna.ExpenseCents)
	}
	if len(anna.Members) != 1 || anna.Members[0].Member.ID != 1 {
		t.Errorf("person view must hold one member, got %+v", anna.Members)
	}

	// Ben: half the rent, the policy is none of his business.
	ben := BuildMonthReport(d, "2026-05", 2)
	if ben.ExpenseCents != 50000 {
		t.Errorf("Ben = %d, want 50000", ben.ExpenseCents)
	}
	for _, c := range ben.Categories {
		if c.Key == 21 {
			t.Error("a booking Ben has no share in leaked into his view")
		}
	}
}

func TestSettlementSquaresTheBooks(t *testing.T) {
	rep := Settlement(sharedPlan(), month("2026-05"))
	positions, moves := rep.Positions, rep.Transfers
	if len(positions) != 2 {
		t.Fatalf("positions = %+v", positions)
	}

	// Anna fronts 1050 € and owes 550 €, so Ben owes her his 500 €.
	if positions[0].PaidCents != 105000 || positions[0].OwedCents != 55000 {
		t.Errorf("Anna = %+v", positions[0])
	}
	if positions[1].PaidCents != 0 || positions[1].OwedCents != 50000 {
		t.Errorf("Ben = %+v", positions[1])
	}

	if len(moves) != 1 {
		t.Fatalf("transfers = %+v", moves)
	}
	if moves[0].From.ID != 2 || moves[0].To.ID != 1 || moves[0].Cents != 50000 {
		t.Errorf("transfer = %+v, want Ben -> Anna 50000", moves[0])
	}

	var net int64
	for _, p := range positions {
		net += p.NetCents
	}
	if net != 0 {
		t.Errorf("positions do not add up to zero: %d", net)
	}
}

// A bill only its payer carries must not move a cent, which is the difference
// between the shared rent and the policy Anna keeps to herself.
func TestSettlementLinesSeparateSharedFromSole(t *testing.T) {
	rep := Settlement(sharedPlan(), month("2026-05"))
	if len(rep.Lines) != 2 {
		t.Fatalf("lines = %+v", rep.Lines)
	}

	rent, policy := rep.Lines[0], rep.Lines[1]
	if !rent.Shared() || rent.Carriers != 2 {
		t.Errorf("rent = %+v, want it divided by two", rent)
	}
	if rent.ShareOf(1) != 50000 || rent.ShareOf(2) != 50000 {
		t.Errorf("rent shares = %v, want 50000 each", rent.Shares)
	}
	if policy.Shared() || policy.ShareOf(2) != 0 {
		t.Errorf("policy = %+v, want Anna to carry it alone", policy)
	}
	if policy.Payer.ID != 1 {
		t.Errorf("policy payer = %d, want Anna", policy.Payer.ID)
	}

	household := rep.CarriedBy(Everyone)
	if household.SharedCents != 100000 || household.SoleCents != 5000 {
		t.Errorf("household = %+v, want 100000 shared / 5000 alone", household)
	}
	// What Ben transfers is his half of the rent, never a part of the policy.
	if rep.Transfers[0].Cents != rent.ShareOf(2) {
		t.Errorf("transfer = %d, want Ben's rent share %d", rep.Transfers[0].Cents, rent.ShareOf(2))
	}
}

// The person view answers "what does this cost me": half the shared rent plus
// what that member carries alone, and nothing they have no share in.
func TestCarriedByMemberMatchesTheirExpenses(t *testing.T) {
	d := sharedPlan()
	rep := Settlement(d, month("2026-05"))

	anna := rep.CarriedBy(1)
	if anna.SharedCents != 50000 || anna.SoleCents != 5000 {
		t.Errorf("Anna = %+v, want 50000 shared / 5000 alone", anna)
	}
	if got := anna.SharedCents + anna.SoleCents; got != BuildMonthReport(d, "2026-05", 1).ExpenseCents {
		t.Errorf("Anna carries %d, but her report says %d", got, BuildMonthReport(d, "2026-05", 1).ExpenseCents)
	}

	ben := rep.CarriedBy(2)
	if ben.SharedCents != 50000 || ben.SoleCents != 0 {
		t.Errorf("Ben = %+v, want 50000 shared and nothing alone", ben)
	}
	if lines := rep.LinesFor(2); len(lines) != 1 || lines[0].Booking.ID != 1 {
		t.Errorf("Ben's lines = %+v, want the rent only", lines)
	}
}

// A payment has to be checkable: every member's ledger lists what they fronted
// less their own share and has to end on their position.
func TestLedgerAddsUpToThePosition(t *testing.T) {
	d := sharedPlan()
	// Ben fronts the groceries both of them share.
	d.Bookings = append(d.Bookings, store.Booking{
		ID: 3, CategoryID: 22, Direction: store.DirExpense, AmountCents: 25000,
		Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitEqual,
		CostNature: store.CostVariable, BudgetClass: store.ClassNeed,
		PayerMemberID: memberRef(2), Settle: true,
	})
	d.Splits[3] = []store.BookingSplit{{BookingID: 3, MemberID: 1}, {BookingID: 3, MemberID: 2}}

	rep := Settlement(d, month("2026-05"))
	for _, p := range rep.Positions {
		var net int64
		for _, l := range rep.Ledger(p.Member.ID) {
			if l.NetCents != l.PaidCents-l.OwedCents {
				t.Errorf("%s: line %+v does not net out", p.Member.Name, l)
			}
			net += l.NetCents
		}
		if net != p.NetCents {
			t.Errorf("%s: ledger = %d, position = %d", p.Member.Name, net, p.NetCents)
		}
	}

	// Ben's ledger holds the rent he only carries and the groceries he fronted,
	// never the policy Anna keeps to herself.
	ben := rep.Ledger(2)
	if len(ben) != 2 {
		t.Fatalf("Ben's ledger = %+v", ben)
	}
	if ben[0].PaidCents != 0 || ben[0].OwedCents != 50000 {
		t.Errorf("rent line = %+v, want carried only", ben[0])
	}
	if ben[1].PaidCents != 25000 || ben[1].OwedCents != 12500 {
		t.Errorf("groceries line = %+v, want fronted 25000 and carried 12500", ben[1])
	}

	// With two members the payment is exactly what the debtor's ledger says.
	if len(rep.Transfers) != 1 {
		t.Fatalf("transfers = %+v", rep.Transfers)
	}
	if rep.Transfers[0].From.ID != 2 || rep.Transfers[0].Cents != 37500 {
		t.Errorf("transfer = %+v, want Ben paying 37500", rep.Transfers[0])
	}
}

func TestSettlementIgnoresBookingsWithoutPayer(t *testing.T) {
	d := sharedPlan()
	d.Bookings[0].PayerMemberID = nil
	rep := Settlement(d, month("2026-05"))
	// Only the policy is left, and Anna both pays and owes it.
	if len(rep.Transfers) != 0 {
		t.Errorf("transfers = %+v, want none", rep.Transfers)
	}
	if len(rep.Lines) != 1 || rep.Lines[0].Booking.ID != 2 {
		t.Errorf("lines = %+v, want only the policy", rep.Lines)
	}
}

// A shared cost the two never square between them still belongs in the budget,
// but not in the settlement.
func TestSettlementSkipsWhatIsNotToBeSettled(t *testing.T) {
	d := sharedPlan()
	d.Bookings[0].Settle = false

	rep := Settlement(d, month("2026-05"))
	if len(rep.Transfers) != 0 {
		t.Errorf("transfers = %+v, want none once the rent is left out", rep.Transfers)
	}
	for _, l := range rep.Lines {
		if l.Booking.ID == 1 {
			t.Error("the rent leaked into the settlement")
		}
	}
	// The budget still knows the rent, only the settlement does not.
	if got := BuildMonthReport(d, "2026-05", Everyone).ExpenseCents; got != 105000 {
		t.Errorf("expenses = %d, want the rent to keep counting", got)
	}
}

func TestTrendChartStaysInsideItsCanvas(t *testing.T) {
	reps := Trend(planData(), []string{"2026-04", "2026-05", "2026-06"}, Everyone)
	c := BuildTrendChart(reps, 720, 260)

	if c.Empty() {
		t.Fatal("chart is empty")
	}
	if len(c.Bars) != 6 || len(c.Ticks) != 3 {
		t.Fatalf("bars = %d, ticks = %d", len(c.Bars), len(c.Ticks))
	}
	for _, b := range c.Bars {
		if b.Y < c.Top-0.01 || b.Y+b.Height > c.Bottom+0.01 {
			t.Errorf("bar %s leaves the plot: y=%.2f h=%.2f", b.Month, b.Y, b.Height)
		}
		if b.X < c.Left-0.01 || b.X+b.Width > c.Right+0.01 {
			t.Errorf("bar %s leaves the plot horizontally: x=%.2f", b.Month, b.X)
		}
	}
	// The axis has to reach past the tallest bar, otherwise it would be clipped.
	if len(c.Grid) < 2 || c.Grid[len(c.Grid)-1].Cents < 500000 {
		t.Errorf("grid = %+v", c.Grid)
	}
}

func TestTrendChartWithoutFiguresIsEmpty(t *testing.T) {
	if !BuildTrendChart(nil, 720, 260).Empty() {
		t.Error("a chart without reports must be empty")
	}
	if !BuildTrendChart([]MonthReport{{Month: "2026-05"}}, 720, 260).Empty() {
		t.Error("a chart of zero figures must be empty")
	}
}
