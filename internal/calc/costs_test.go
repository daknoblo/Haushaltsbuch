package calc

import (
	"reflect"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestCostsIncludeNonReimbursableExpenses(t *testing.T) {
	d := sharedPlan()
	add := func(id, cents int64, payer *int64, settle bool, nature store.CostNature, splits []store.BookingSplit) {
		b := d.Bookings[0]
		b.ID, b.AmountCents, b.PayerMemberID, b.Settle, b.CostNature = id, cents, payer, settle, nature
		d.Bookings = append(d.Bookings, b)
		d.Splits[id] = splits
	}
	add(3, 1500, nil, true, store.CostVariable, []store.BookingSplit{{MemberID: 2}})
	add(4, 101, memberRef(1), false, store.CostFix, []store.BookingSplit{{MemberID: 1}, {MemberID: 2}})
	d.Bookings[3].BudgetClass = store.ClassSaving
	add(5, 700, memberRef(1), true, store.CostFix, []store.BookingSplit{{MemberID: 2}})
	d.Bookings[2].SplitMode = store.SplitFixed
	d.Splits[3][0].Value = 1500
	before := Settlement(d, month("2026-05"))
	costs := Costs(d, month("2026-05"))
	if len(costs.Lines) != 5 || len(costs.InvalidBookings) != 0 {
		t.Fatalf("costs = %+v", costs)
	}
	for _, tc := range []struct {
		member int64
		want   Carried
	}{
		{Everyone, Carried{SharedCents: 100101, SoleCents: 7200, FixedSharedCents: 100101, FixedSoleCents: 5700}},
		{1, Carried{SharedCents: 50051, SoleCents: 5000, FixedSharedCents: 50051, FixedSoleCents: 5000}},
		{2, Carried{SharedCents: 50050, SoleCents: 2200, FixedSharedCents: 50050, FixedSoleCents: 700}},
	} {
		if got := costs.CarriedBy(tc.member); got != tc.want {
			t.Errorf("member %d: got %+v, want %+v", tc.member, got, tc.want)
		}
	}
	for _, line := range costs.Lines {
		want := line.Booking.ID >= 2 && line.Booking.ID <= 4
		if line.WithoutSettlement != want {
			t.Errorf("booking %d: without settlement = %v", line.Booking.ID, line.WithoutSettlement)
		}
	}
	if len(costs.LinesFor(2)) != 4 || len(costs.LinesFor(99)) != 0 {
		t.Error("person scope must select only that person's expenses")
	}
	if !reflect.DeepEqual(before, Settlement(d, month("2026-05"))) ||
		len(before.Transfers) != 1 || before.Transfers[0].Cents != 50700 {
		t.Fatalf("cost visibility changed settlement: %+v", before)
	}
}

func TestCostsUseSamePeriodRoundingAsBudget(t *testing.T) {
	d := sharedPlan()
	d.Bookings[0].AmountCents = 100001
	d.Bookings[1].Frequency, d.Bookings[1].AmountCents = store.FreqOnce, 101
	d.Bookings[1].StartsOn = "2026-02-01"
	d.Overrides = map[int64][]store.BookingOverride{
		1: {{StartsOn: "2026-01-01", EndsOn: "2026-01-31", AmountCents: 99999}},
	}
	months := []string{"2026-01", "2026-02", "2026-03"}
	d = Prepare(d, months)
	for _, averaged := range []bool{false, true} {
		costs := CostsTotal(d, months)
		if averaged {
			costs = Costs(d, months)
		}
		for _, member := range []int64{Everyone, 1, 2} {
			var want int64
			for _, m := range months {
				want += BuildMonthReport(d, m, member).ExpenseCents
			}
			if averaged {
				want = PeriodReport(d, months, member).ExpenseCents
			}
			if got := costs.CarriedBy(member).TotalCents(); got != want {
				t.Errorf("average=%v member=%d: costs %d, budget %d", averaged, member, got, want)
			}
		}
		if got := costs.CarriedBy(1).TotalCents() + costs.CarriedBy(2).TotalCents(); got != costs.CarriedBy(Everyone).TotalCents() {
			t.Error("member costs must add up without losing cents")
		}
	}
	if got := CostsTotal(d, months).CarriedBy(Everyone).TotalCents(); got != 300102 {
		t.Errorf("period total = %d, want 300102", got)
	}
	for _, report := range []CostReport{Costs(d, nil), CostsTotal(d, nil)} {
		if len(report.Lines) != 0 || len(report.InvalidBookings) != 0 {
			t.Error("empty period must have no costs")
		}
	}
}

func TestCostsWarnAboutInvalidAllocations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   store.SplitMode
		splits []store.BookingSplit
	}{
		{"unassigned", store.SplitEqual, nil},
		{"unknown member", store.SplitEqual, []store.BookingSplit{{MemberID: 99}}},
		{"incomplete percent", store.SplitPercent, []store.BookingSplit{{MemberID: 1, Value: 40}}},
		{"excess fixed", store.SplitFixed, []store.BookingSplit{{MemberID: 1, Value: 100001}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := sharedPlan()
			d.Bookings[0].SplitMode, d.Bookings[0].Settle, d.Bookings[0].PayerMemberID = tc.mode, false, nil
			d.Splits[1] = tc.splits
			got := Costs(d, month("2026-05"))
			if len(got.InvalidBookings) != 1 || got.InvalidBookings[0].ID != 1 ||
				len(got.Lines) != 1 || got.CarriedBy(Everyone).TotalCents() != 5000 {
				t.Fatalf("invalid expense must be listed and excluded: %+v", got)
			}
			d.Bookings[0].StartsOn = "2027-01-01"
			if len(Costs(d, month("2026-05")).InvalidBookings) != 0 {
				t.Error("inactive expenses should not warn")
			}
			d.Bookings[0].StartsOn, d.Bookings[0].Direction = "", store.DirIncome
			if len(Costs(d, month("2026-05")).InvalidBookings) != 0 {
				t.Error("income is not a carried cost")
			}
		})
	}
}

func TestCostsKeepZeroSharesAndNonEqualAllocations(t *testing.T) {
	d := sharedPlan()
	d.Bookings[0].AmountCents = 101
	d.Bookings[0].SplitMode = store.SplitPercent
	d.Splits[1][0].Value, d.Splits[1][1].Value = 70, 30
	d.Bookings[1].AmountCents = 0
	got := Costs(d, month("2026-05"))
	if len(got.Lines) != 2 || len(got.LinesFor(1)) != 2 || got.Lines[0].ShareOf(1) != 71 ||
		got.Lines[0].ShareOf(2) != 30 || got.Lines[1].Shared() || !got.Lines[1].WithoutSettlement {
		t.Fatalf("zero personal costs and 70/30 split: %+v", got)
	}
}
