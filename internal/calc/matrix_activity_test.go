package calc

import (
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestMatrixIncludesActiveZeroMonths(t *testing.T) {
	d := sharedPlan()
	d.Bookings = d.Bookings[:1]
	d.Bookings[0].StartsOn = "2026-02-01"
	d.Bookings[0].EndsOn = "2026-04-30"
	d.Bookings[0].AmountCents = 10000
	d.Overrides = map[int64][]store.BookingOverride{
		1: {{StartsOn: "2026-03-01", EndsOn: "2026-04-30", AmountCents: 0}},
	}
	matrix := BuildMatrix(d, []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05"}, Everyone)
	row := matrix.Band(BandFixed).Rows[0]
	if row.ActiveMonths != 3 || row.MeanCents != 3333 || row.MedianCents != 0 {
		t.Errorf("active values 10000, 0, 0: %+v", row)
	}
	if row.Active[0] || row.Active[4] || !row.Active[2] || !row.Active[3] {
		t.Errorf("activity = %v", row.Active)
	}
	if row.Trend[2] != TrendDown || row.Trend[3] != TrendFlat || row.Trend[4] != TrendNone {
		t.Errorf("zero month trends = %v", row.Trend)
	}
}

func TestMatrixBalancedMonthCountsTowardsSurplusAverage(t *testing.T) {
	d := planData()
	d.Bookings = d.Bookings[:2]
	d.Bookings[0].AmountCents = 10000
	d.Bookings[1].AmountCents = 10000
	d.Bookings[1].Direction = store.DirExpense
	d.Bookings[1].CategoryID = 20
	d.Bookings[1].CostNature = store.CostFix
	d.Bookings[1].BudgetClass = store.ClassNeed
	d.Overrides = map[int64][]store.BookingOverride{
		1: {{StartsOn: "2026-02-01", EndsOn: "2026-02-28", AmountCents: 20000}},
	}
	m := BuildMatrix(d, []string{"2026-01", "2026-02"}, Everyone)
	if m.Surplus.ActiveMonths != 2 || m.Surplus.MeanCents != 5000 || m.Surplus.MedianCents != 5000 {
		t.Errorf("surplus values 0, 10000 should average 5000: %+v", m.Surplus)
	}
}

func TestMatrixShowsZeroOnlyBookings(t *testing.T) {
	d := sharedPlan()
	d.Bookings = d.Bookings[:1]
	d.Bookings[0].AmountCents = 0
	for _, member := range []int64{Everyone, 1, 2} {
		m := BuildMatrix(d, month("2026-05"), member)
		if m.Empty() || m.Band(BandFixed).Total.ActiveMonths != 1 {
			t.Errorf("zero booking disappeared for member %d", member)
		}
	}
}
