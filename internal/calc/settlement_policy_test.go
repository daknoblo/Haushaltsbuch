package calc

import (
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestPayerCarriesAlone(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   store.SplitMode
		payer  *int64
		splits []store.BookingSplit
		want   bool
	}{
		{"equal sole payer", store.SplitEqual, memberRef(1), []store.BookingSplit{{MemberID: 1}}, true},
		{"equal shared", store.SplitEqual, memberRef(1), []store.BookingSplit{{MemberID: 1}, {MemberID: 2}}, false},
		{"another payer", store.SplitEqual, memberRef(2), []store.BookingSplit{{MemberID: 1}}, false},
		{"no payer", store.SplitEqual, nil, []store.BookingSplit{{MemberID: 1}}, false},
		{"no carrier", store.SplitEqual, memberRef(1), nil, false},
		{"percent sole payer", store.SplitPercent, memberRef(1), []store.BookingSplit{{MemberID: 1, Value: 100}, {MemberID: 2, Value: 0}}, true},
		{"percent incomplete", store.SplitPercent, memberRef(1), []store.BookingSplit{{MemberID: 1, Value: 50}}, false},
		{"percent shared", store.SplitPercent, memberRef(1), []store.BookingSplit{{MemberID: 1, Value: 60}, {MemberID: 2, Value: 40}}, false},
		{"fixed sole payer", store.SplitFixed, memberRef(1), []store.BookingSplit{{MemberID: 1, Value: 10000}, {MemberID: 2}}, true},
		{"fixed incomplete", store.SplitFixed, memberRef(1), []store.BookingSplit{{MemberID: 1, Value: 5000}}, false},
		{"fixed overallocated", store.SplitFixed, memberRef(1), []store.BookingSplit{{MemberID: 1, Value: 20000}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := store.Booking{
				Direction: store.DirExpense, Frequency: store.FreqMonthly,
				AmountCents: 10000, SplitMode: tc.mode, PayerMemberID: tc.payer, Settle: true,
			}
			if got := PayerCarriesAlone(b, tc.splits, nil, "2026-09"); got != tc.want {
				t.Errorf("automatic exclusion = %t, want %t", got, tc.want)
			}
			if got := SettlementEnabled(b, tc.splits, nil, "2026-09"); got == tc.want {
				t.Errorf("effective setting = %t", got)
			}
		})
	}
}

func TestSolePayerHonorsFixedOverridesAndIncome(t *testing.T) {
	b := store.Booking{
		Direction: store.DirExpense, Frequency: store.FreqMonthly, AmountCents: 10000,
		SplitMode: store.SplitFixed, PayerMemberID: memberRef(1), Settle: true,
	}
	splits := []store.BookingSplit{{MemberID: 1, Value: 10000}}
	overrides := []store.BookingOverride{{StartsOn: "2026-09-01", EndsOn: "2026-09-30", AmountCents: 20000}}
	if PayerCarriesAlone(b, splits, overrides, "2026-09") {
		t.Fatal("invalid fixed split must remain visible to settlement validation")
	}
	if !PayerCarriesAlone(b, splits, overrides, "2026-10") {
		t.Fatal("expired override must not prevent automatic exclusion")
	}
	b.Direction = store.DirIncome
	if PayerCarriesAlone(b, splits, nil, "2026-09") || SettlementEnabled(b, splits, nil, "2026-09") {
		t.Fatal("income is never an expense to settle")
	}
}

func TestSettlementRestoresOnlyTheSavedPreference(t *testing.T) {
	d := sharedPlan()
	d.Bookings = d.Bookings[:1]
	shared := d.Splits[1]
	d.Splits[1] = shared[:1]
	if got := Settlement(d, month("2026-09")); len(got.Lines) != 0 || len(got.Transfers) != 0 {
		t.Fatalf("sole payer included: %+v", got)
	}
	if !d.Bookings[0].Settle {
		t.Fatal("automatic exclusion changed the saved preference")
	}
	d.Splits[1] = shared
	if got := Settlement(d, month("2026-09")); len(got.Lines) != 1 || len(got.Transfers) != 1 {
		t.Fatal("adding another carrier did not restore settlement")
	}
	d.Bookings[0].Settle = false
	d.Splits[1] = shared[:1]
	Settlement(d, month("2026-09"))
	d.Splits[1] = shared
	if got := Settlement(d, month("2026-09")); len(got.Lines) != 0 {
		t.Fatal("manual exclusion must remain disabled")
	}
}

func TestAnotherPersonPayingSoleCostStillSettles(t *testing.T) {
	d := sharedPlan()
	d.Bookings = d.Bookings[:1]
	d.Splits[1] = []store.BookingSplit{{MemberID: 2}}
	rep := Settlement(d, month("2026-09"))
	if len(rep.Transfers) != 1 || rep.Transfers[0].From.ID != 2 || rep.Transfers[0].Cents != 100000 {
		t.Fatalf("another person's sole cost needs reimbursement: %+v", rep)
	}
}
