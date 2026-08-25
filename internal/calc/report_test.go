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
				PayerMemberID: memberRef(1)},
			{ID: 2, CategoryID: 21, Direction: store.DirExpense, AmountCents: 5000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitPercent,
				CostNature: store.CostFix, BudgetClass: store.ClassNeed,
				PayerMemberID: memberRef(1)},
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
	positions, moves := Settlement(sharedPlan(), month("2026-05"))
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

func TestSettlementIgnoresBookingsWithoutPayer(t *testing.T) {
	d := sharedPlan()
	d.Bookings[0].PayerMemberID = nil
	_, moves := Settlement(d, month("2026-05"))
	// Only the policy is left, and Anna both pays and owes it.
	if len(moves) != 0 {
		t.Errorf("transfers = %+v, want none", moves)
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
