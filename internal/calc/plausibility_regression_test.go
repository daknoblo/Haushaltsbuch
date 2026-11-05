package calc

import (
	"reflect"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestZeroActualAllocationRetainsSelectedMember(t *testing.T) {
	plan := store.Booking{SplitMode: store.SplitFixed}
	shares := []store.BookingSplit{{MemberID: 1}}
	got := ActualAllocation(plan, shares, 0)
	if len(got) != 1 || got[0].MemberID != 1 || got[0].Cents != 0 {
		t.Fatalf("zero allocation lost: %+v", got)
	}
	if got := ActualAllocation(plan, shares, 1); got != nil {
		t.Fatal("positive payment must not invent weights from zero fixed shares")
	}
}

func TestAllocationMatchesIndependentReferenceAtBoundaries(t *testing.T) {
	for _, amount := range []int64{0, 1, 2, 49, 99, 199, 500, 999, MaxSplitCents} {
		for _, frequency := range []store.Frequency{store.FreqOnce, store.FreqMonthly, store.FreqWeekly, store.FreqQuarterly, store.FreqYearly} {
			for _, interval := range []int{1, 2, 3, 7, 22, 60} {
				for _, mode := range []store.SplitMode{store.SplitEqual, store.SplitPercent, store.SplitFixed} {
					b := store.Booking{ID: 1, AmountCents: amount, Direction: store.DirExpense, Frequency: frequency, Interval: interval, SplitMode: mode, StartsOn: "2020-01-01"}
					splits := []store.BookingSplit{{BookingID: 1, MemberID: 1}, {BookingID: 1, MemberID: 2}}
					if mode == store.SplitPercent {
						splits[0].Value = 40.13
						splits[1].Value = 59.87
					}
					if mode == store.SplitFixed {
						splits[0].Value = float64(amount / 2)
						splits[1].Value = float64(amount - amount/2)
					}
					want := referenceMonth(store.HouseholdSnapshot{Bookings: []store.BookingSnapshot{{Booking: b, Splits: splits}}}, "2020-01", "2020-07", BasisPlan)[0]
					got := evaluateBooking(b, splits, nil, "2020-01")
					if got.Cents != want.Cents || got.Complete != want.Complete || got.Unassigned != want.Unassigned || !reflect.DeepEqual(got.Shares, want.Shares) {
						t.Fatalf("amount=%d frequency=%s interval=%d mode=%s: got=%+v want=%+v", amount, frequency, interval, mode, got, want)
					}
				}
			}
		}
	}
}

func TestMonthlyNormalizationMatchesExactCents(t *testing.T) {
	for _, rate := range []struct {
		frequency              store.Frequency
		numerator, denominator int64
	}{
		{store.FreqMonthly, 1, 1},
		{store.FreqQuarterly, 1, 3},
		{store.FreqYearly, 1, 12},
		{store.FreqWeekly, 52, 12},
	} {
		for interval := 1; interval <= 60; interval++ {
			denominator := rate.denominator * int64(interval)
			for amount := int64(0); amount <= 1000; amount++ {
				numerator := amount * rate.numerator
				want := numerator / denominator
				if 2*(numerator%denominator) >= denominator {
					want++
				}
				b := store.Booking{AmountCents: amount, Frequency: rate.frequency, Interval: interval}
				if got := MonthlyCents(b, nil, "2020-01"); got != want {
					t.Fatalf("%s interval %d amount %d: monthly cents %d, exact %d", rate.frequency, interval, amount, got, want)
				}
			}
		}
	}
}

func TestZeroFixedAllocationRemainsAValidActiveZero(t *testing.T) {
	member := int64(1)
	data := Data{
		Members:    []store.Member{{ID: member, Name: "Example"}},
		Categories: []store.Category{{ID: 1, Name: "Example", Classification: store.DirExpense}},
		Bookings: []store.Booking{{ID: 1, CategoryID: 1, PayerMemberID: &member, Direction: store.DirExpense, Name: "Zero",
			Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitFixed, CostNature: store.CostFix, BudgetClass: store.ClassNeed}},
		Splits: map[int64][]store.BookingSplit{1: {{MemberID: member, Value: 0}}},
	}
	if costs := Costs(data, []string{"2020-01"}); len(costs.InvalidBookings) != 0 || len(costs.Lines) != 1 {
		t.Fatal("an explicitly allocated zero must not be reported as invalid")
	}
	matrix := BuildMatrix(data, []string{"2020-01"}, member)
	if !matrix.Expense.Active[0] {
		t.Fatal("an explicitly allocated zero must remain active in the member view")
	}
}

func TestSingleMonthPeriodPreservesActualCoverage(t *testing.T) {
	data := WithBankLedger(Data{}, store.BankLedger{ClosedMonths: []string{"2020-01"}}, BasisActual, "2020-02")
	monthly := BuildMonthReport(data, "2020-01", Everyone)
	period := PeriodReport(data, []string{"2020-01"}, Everyone)
	if !reflect.DeepEqual(period, monthly) {
		t.Fatalf("single-month period lost source or zero-income coverage: source=%q incomeRecorded=%t", period.Source, period.IncomeRecorded)
	}

}

func TestPeriodRemainderTiesUseStableIDsAcrossDifferentAmounts(t *testing.T) {
	data := Data{Splits: make(map[int64][]store.BookingSplit)}
	for i, amount := range []int64{1000, 1, 1} {
		id := int64(i + 1)
		data.Bookings = append(data.Bookings, store.Booking{ID: id, Direction: store.DirExpense, AmountCents: amount, Frequency: store.FreqOnce, StartsOn: "2020-01-01", SplitMode: store.SplitEqual})
		data.Splits[id] = []store.BookingSplit{{MemberID: 1}}
	}
	values := periodBookingValues(data, []string{"2020-01", "2020-02", "2020-03"}, true)
	for _, v := range values {
		want := int64(0)
		if v.Booking.ID == 1 {
			want = 334
		}
		if v.Cents != want {
			t.Fatalf("booking %d received %d cents, want %d: equal thirds must tie by ID", v.Booking.ID, v.Cents, want)
		}
	}
}
