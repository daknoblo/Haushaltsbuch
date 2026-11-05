package calc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func plausibilityFixture() store.HouseholdSnapshot {
	payer := int64(1)
	s := store.HouseholdSnapshot{
		Household:  store.Household{ID: 1},
		Members:    []store.Member{{ID: 1, HouseholdID: 1, Name: "Example A"}, {ID: 2, HouseholdID: 1, Name: "Example B"}},
		Categories: []store.Category{{ID: 1, HouseholdID: 1, Name: "Income", Classification: store.DirIncome}, {ID: 2, HouseholdID: 1, Name: "Expense", Classification: store.DirExpense}},
		Bank: store.BankLedger{
			Accounts:     []store.BankAccount{{ID: 1, MemberID: 1, Default: true}, {ID: 2, MemberID: 2, Default: true}},
			Imports:      []store.BankImport{{ID: 1, AccountID: 1, Digest: "synthetic"}},
			ClosedMonths: []string{"2020-02", "2020-04"},
		},
	}
	for i, amount := range []int64{10001, 30001, 7001} {
		id := int64(i + 1)
		b := store.Booking{ID: id, HouseholdID: 1, CategoryID: 2, Name: "Synthetic " + string(rune('A'+i)),
			Direction: store.DirExpense, AmountCents: amount, Frequency: store.FreqMonthly, Interval: 1, DuePoint: store.DueStart,
			StartsOn: "2020-01-01", EndsOn: "2020-12-31", CostNature: store.CostFix, BudgetClass: store.ClassNeed,
			SplitMode: store.SplitEqual, PayerMemberID: &payer, Settle: true}
		splits := []store.BookingSplit{{BookingID: id, MemberID: 1}, {BookingID: id, MemberID: 2}}
		if i == 0 {
			b.SplitMode = store.SplitPercent
			splits[0].Value = 40
			splits[1].Value = 60
		}
		if i > 0 {
			b.Direction = store.DirIncome
			b.CategoryID = 1
		}
		if i == 2 {
			b.Frequency = store.FreqWeekly
			b.SplitMode = store.SplitFixed
			splits = []store.BookingSplit{{BookingID: id, MemberID: 2, Value: float64(amount)}}
		}
		s.Bookings = append(s.Bookings, store.BookingSnapshot{Booking: b, Splits: splits})
	}
	for i, decision := range []string{"accepted", "transfer", "pending", "ignored", "accepted"} {
		p := store.BankTransaction{ID: int64(i + 1), ImportID: 1, BookedOn: "2020-02-05", Direction: store.DirExpense, AmountCents: 9999, Name: "Synthetic payment", Decision: decision,
			CostNature: store.CostFix, BudgetClass: store.ClassNeed}
		if decision == "accepted" {
			p.CategoryID = 2
			p.Splits = []store.BankSplit{{MemberID: 1, Cents: 4000}, {MemberID: 2, Cents: 5999}}
		}
		if decision == "transfer" {
			p.TransferAccountID = 2
		}
		if decision == "pending" {
			p.BookedOn = "2020-03-05"
		}
		if i == 4 {
			p.Direction = store.DirIncome
			p.CategoryID = 1
			p.AmountCents = 0
			p.Splits = []store.BankSplit{{MemberID: 1, Cents: 0}}
		}
		p.Fingerprint = store.PaymentFingerprint(p)
		s.Bank.Transactions = append(s.Bank.Transactions, p)
	}
	return s
}

func TestPlausibilityChecksAllBasesWithoutChangingData(t *testing.T) {
	s := plausibilityFixture()
	before, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CheckPlausibility(t.Context(), s, 2020, "2020-07")
	if err != nil {
		t.Fatal(err)
	}
	if result.Errors != 0 || result.Warnings != 0 || !result.Calculated || result.Checks < 1000 {
		t.Fatalf("unexpected result: %+v", result)
	}
	after, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("check modified its source")
	}
}

func TestPlausibilityDetectsCorruptionAndIntentionalDuplicateWarning(t *testing.T) {
	s := plausibilityFixture()
	duplicate := s.Bank.Transactions[0]
	duplicate.ID = 99
	s.Bank.Transactions = append(s.Bank.Transactions, duplicate)
	result, err := CheckPlausibility(t.Context(), s, 2020, "2020-07")
	if err != nil || result.Errors != 0 || result.Warnings != 1 || !result.Calculated {
		t.Fatalf("%+v %v", result, err)
	}
	s.Bank.Transactions[0].Splits[0].Cents++
	result, err = CheckPlausibility(t.Context(), s, 2020, "2020-07")
	if err != nil || result.Errors == 0 || result.Calculated {
		t.Fatalf("corruption not detected: %+v %v", result, err)
	}
}

func TestPlausibilityWarnsAboutOverlappingMonthsNotSuccessiveYears(t *testing.T) {
	s := plausibilityFixture()
	next := s.Bookings[0]
	next.Booking.ID, next.Booking.AmountCents, next.Booking.StartsOn = 4, 12001, "2020-06-15"
	next.Splits = []store.BookingSplit{{BookingID: 4, MemberID: 1, Value: 40}, {BookingID: 4, MemberID: 2, Value: 60}}
	s.Bookings = append(s.Bookings, next)
	result, err := CheckPlausibility(t.Context(), s, 2020, "2020-07")
	if err != nil || result.Errors != 0 || result.Warnings != 1 || result.Findings[0].Code != "overlapPlan" {
		t.Fatalf("%+v %v", result, err)
	}
	s.Bookings[3].Booking.StartsOn, s.Bookings[3].Booking.EndsOn = "2021-01-01", "2021-12-31"
	result, err = CheckPlausibility(t.Context(), s, 2020, "2020-07")
	if err != nil || result.Errors != 0 || result.Warnings != 0 {
		t.Fatalf("successive years flagged: %+v %v", result, err)
	}
}

func TestActualSnapshotsSurvivePlanEditsAndHybridNeverAddsBoth(t *testing.T) {
	s := plausibilityFixture()
	s.Bookings[0].Booking.AmountCents = 100000
	s.Bookings[0].Splits[0].Value, s.Bookings[0].Splits[1].Value = 99, 1
	for _, test := range []struct {
		basis                  Basis
		month, current, source string
		expense                int64
	}{
		{BasisPlan, "2020-02", "2020-07", "plan", 100000},
		{BasisActual, "2020-02", "2020-07", "actual", 9999},
		{BasisHybrid, "2020-02", "2020-07", "actual", 9999},
		{BasisHybrid, "2020-01", "2020-07", "plan_estimate", 100000},
		{BasisHybrid, "2020-02", "2020-02", "plan", 100000},
		{BasisHybrid, "2020-08", "2020-07", "plan", 100000},
	} {
		data := WithBankLedger(FromSnapshot(s), s.Bank, test.basis, test.current)
		got := BuildMonthReport(data, test.month, Everyone)
		if got.ExpenseCents != test.expense || got.Source != test.source {
			t.Fatalf("%+v: expense=%d source=%s", test, got.ExpenseCents, got.Source)
		}
		if test.source == "actual" && BuildMonthReport(data, test.month, 1).ExpenseCents != 4000 {
			t.Fatal("plan edit changed an accepted allocation snapshot")
		}
	}
	result, err := CheckPlausibility(t.Context(), s, 2020, "2020-07")
	if err != nil || result.Errors != 0 {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestPlausibilityDetectsDroppedActiveZeroMonth(t *testing.T) {
	s := plausibilityFixture()
	s.Bookings[0].Booking.AmountCents = 0
	s.Bookings[0].Booking.SplitMode = store.SplitFixed
	s.Bookings[0].Splits[0].Value, s.Bookings[0].Splits[1].Value = 0, 0
	months := []string{"2020-01"}
	data := WithBankLedger(FromSnapshot(s), s.Bank, BasisPlan, "2020-07")
	matrix := BuildMatrix(data, months, 1)
	matrix.Expense.Active[0] = false
	matrix.Expense.ActiveMonths = 0
	var result PlausibilityResult
	reference := map[string][]bookingValue{"2020-01": referenceMonth(s, "2020-01", "2020-07", BasisPlan)}
	checkMatrixProjection(&result, s, reference, matrix, "2020-07", BasisPlan, 1)
	if result.Errors == 0 {
		t.Fatal("missing active zero month was not detected")
	}
}

func TestPlausibilityDetectsWrongReportAndCanceledCheck(t *testing.T) {
	var out PlausibilityResult
	want := MonthReport{IncomeCents: 100, BalanceCents: 100}
	got := want
	got.IncomeCents++
	checkReport(&out, Finding{Code: "month"}, want, got)
	if out.Errors != 1 || out.Findings[0].Expected != 100 || out.Findings[0].Actual != 101 {
		t.Fatalf("%+v", out)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := CheckPlausibility(ctx, plausibilityFixture(), 2020, "2020-07")
	if !errors.Is(err, context.Canceled) || result.Calculated {
		t.Fatalf("canceled check reported success: %+v %v", result, err)
	}
}

func TestPlausibilityIncompleteSettlementIsAWarningNotAMathError(t *testing.T) {
	s := plausibilityFixture()
	complete := s.Bookings[0]
	complete.Booking.ID, complete.Booking.Name = 4, "Valid shared expense"
	complete.Splits = []store.BookingSplit{{BookingID: 4, MemberID: 1, Value: 40}, {BookingID: 4, MemberID: 2, Value: 60}}
	s.Bookings = append(s.Bookings, complete)
	s.Bookings[0].Splits[0].Value = 30
	result, err := CheckPlausibility(t.Context(), s, 2020, "2020-07")
	if err != nil || result.Errors != 0 || result.Warnings != 1 || !result.Calculated {
		t.Fatalf("%+v %v", result, err)
	}
}
