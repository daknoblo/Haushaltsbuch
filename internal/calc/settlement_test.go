package calc

import (
	"fmt"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func assertSettlementBalances(t *testing.T, rep SettlementReport) {
	t.Helper()
	moved := make(map[int64]int64)
	for _, tr := range rep.Transfers {
		moved[tr.From.ID] -= tr.Cents
		moved[tr.To.ID] += tr.Cents
	}
	var net int64
	for _, p := range rep.Positions {
		var paid, owed int64
		for _, l := range rep.Ledger(p.Member.ID) {
			paid += l.PaidCents
			owed += l.OwedCents
		}
		if paid != p.PaidCents || owed != p.OwedCents || paid-owed != p.NetCents {
			t.Errorf("ledger does not match position: %+v; paid %d, owed %d", p, paid, owed)
		}
		if moved[p.Member.ID] != p.NetCents {
			t.Errorf("transfers move %d for member %d, want %d", moved[p.Member.ID], p.Member.ID, p.NetCents)
		}
		net += p.NetCents
	}
	if net != 0 {
		t.Errorf("net positions = %d, want zero", net)
	}
	for _, l := range rep.Lines {
		var carried int64
		for _, c := range l.Shares {
			carried += c
		}
		if carried != l.Cents {
			t.Errorf("booking %d costs %d, shares sum to %d", l.Booking.ID, l.Cents, carried)
		}
	}
}

func TestSettlementRoundingAndSmallTransfers(t *testing.T) {
	for _, amount := range []int64{1, 2, 99, 101, 10001} {
		t.Run(fmt.Sprint(amount), func(t *testing.T) {
			d := sharedPlan()
			d.Bookings = d.Bookings[:1]
			d.Bookings[0].AmountCents = amount
			rep := Settlement(d, month("2026-05"))
			assertSettlementBalances(t, rep)
			if amount > 1 && (len(rep.Transfers) != 1 || rep.Transfers[0].Cents != amount/2) {
				t.Errorf("transfers = %+v, want %d cents", rep.Transfers, amount/2)
			}
		})
	}
}

func TestSettlementAveragedLinesMatchPositions(t *testing.T) {
	d := sharedPlan()
	d.Bookings[0].Frequency = store.FreqOnce
	d.Bookings[0].StartsOn = "2026-01-15"
	d.Bookings[0].AmountCents = 10001
	d.Bookings[1].Frequency = store.FreqOnce
	d.Bookings[1].StartsOn = "2026-02-15"
	d.Bookings[1].AmountCents = 101
	months := []string{"2026-01", "2026-02", "2026-03"}
	assertSettlementBalances(t, Settlement(d, months))
	assertSettlementBalances(t, SettlementTotal(d, months))
}

func TestSettlementRecurringCentConservation(t *testing.T) {
	for _, frequency := range []store.Frequency{store.FreqWeekly, store.FreqMonthly, store.FreqQuarterly, store.FreqYearly} {
		for _, count := range []int{2, 3, 5} {
			d := sharedPlan()
			d.Members = make([]store.Member, count)
			d.Splits[1] = make([]store.BookingSplit, count)
			for i := range count {
				id := int64(i + 1)
				d.Members[i] = store.Member{ID: id}
				d.Splits[1][i] = store.BookingSplit{BookingID: 1, MemberID: id}
			}
			d.Bookings[0].Frequency = frequency
			d.Bookings[0].Interval = 2
			d.Bookings[0].AmountCents = 10001
			d.Bookings[1].AmountCents = 103
			months := []string{"2026-01", "2026-02", "2026-03", "2026-04"}
			assertSettlementBalances(t, Settlement(d, months))
			assertSettlementBalances(t, SettlementTotal(d, months))
		}
	}
}

func TestSettlementTotalIncludesOverridesAndOneOffs(t *testing.T) {
	d := sharedPlan()
	d.Bookings[0].EndsOn = "2026-02-28"
	d.Bookings[1].Frequency = store.FreqOnce
	d.Bookings[1].StartsOn = "2026-03-15"
	d.Overrides = map[int64][]store.BookingOverride{
		1: {{StartsOn: "2026-01-01", EndsOn: "2026-01-31", AmountCents: 80000}},
	}
	rep := SettlementTotal(d, []string{"2026-01", "2026-02", "2026-03"})
	assertSettlementBalances(t, rep)
	if p := rep.Positions[0]; p.PaidCents != 180000 || p.OwedCents != 90000 {
		t.Errorf("payer = %+v, want 180000 fronted and 90000 carried; own policy excluded", p)
	}
	if l := rep.Ledger(2)[0]; l.TotalCents != 180000 || l.PaidCents != 0 || l.OwedCents != 90000 {
		t.Errorf("rent ledger = %+v", l)
	}
}

func TestSettlementInvalidSplitsBlockTransfers(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode store.SplitMode
		a, b float64
	}{
		{"percent under", store.SplitPercent, 30, 20},
		{"percent over", store.SplitPercent, 80, 80},
		{"percent zero", store.SplitPercent, 0, 0},
		{"fixed under", store.SplitFixed, 10000, 20000},
		{"fixed over", store.SplitFixed, 90000, 90000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := sharedPlan()
			d.Bookings[0].SplitMode = tc.mode
			d.Splits[1][0].Value = tc.a
			d.Splits[1][1].Value = tc.b
			rep := SettlementTotal(d, []string{"2026-01", "2026-02"})
			if len(rep.InvalidBookings) != 1 || rep.InvalidBookings[0].ID != 1 || len(rep.Transfers) != 0 {
				t.Fatalf("invalid split was not blocked: %+v", rep)
			}
			for _, line := range rep.Lines {
				if line.Booking.ID == 1 {
					t.Error("invalid booking included in ledger")
				}
			}
		})
	}
}

func TestSettlementFixedSplitOverrideInvalidatesWholePeriod(t *testing.T) {
	d := sharedPlan()
	d.Bookings[0].SplitMode = store.SplitFixed
	d.Splits[1][0].Value, d.Splits[1][1].Value = 50000, 50000
	d.Overrides = map[int64][]store.BookingOverride{
		1: {{StartsOn: "2026-02-01", EndsOn: "2026-02-28", AmountCents: 80000}},
	}
	rep := SettlementTotal(d, []string{"2026-01", "2026-02"})
	if len(rep.InvalidBookings) != 1 || len(rep.Lines) != 0 {
		t.Errorf("invalid booking must not leave a partial period: %+v", rep)
	}
}

func TestSettlementDoesNotHideSmallSplitErrors(t *testing.T) {
	for _, mode := range []store.SplitMode{store.SplitFixed, store.SplitPercent} {
		d := sharedPlan()
		d.Bookings = d.Bookings[:1]
		d.Bookings[0].Frequency = store.FreqYearly
		d.Bookings[0].AmountCents = 100
		d.Bookings[0].SplitMode = mode
		d.Splits[1][0].Value, d.Splits[1][1].Value = 49, 50
		rep := Settlement(d, month("2026-05"))
		if len(rep.InvalidBookings) != 1 {
			t.Errorf("%s: normalization concealed an incomplete split", mode)
		}
	}
}

func TestSettlementTotalEmptyPeriod(t *testing.T) {
	rep := SettlementTotal(sharedPlan(), nil)
	if len(rep.Positions) != 2 || len(rep.Lines) != 0 || len(rep.Transfers) != 0 {
		t.Errorf("empty period = %+v", rep)
	}
}
