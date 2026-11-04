package calc

import (
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestHybridUsesActualOnlyForClosedPastMonths(t *testing.T) {
	d := Data{
		Members: []store.Member{{ID: 1}, {ID: 2}},
		Bookings: []store.Booking{{ID: 1, Direction: store.DirExpense, AmountCents: 12000,
			Frequency: store.FreqYearly, Interval: 1, SplitMode: store.SplitEqual, StartsOn: "2020-01-01"}},
		Splits: map[int64][]store.BookingSplit{1: {{MemberID: 1}, {MemberID: 2}}},
	}
	l := store.BankLedger{
		Accounts:     []store.BankAccount{{ID: 1, MemberID: 1}},
		Imports:      []store.BankImport{{ID: 1, AccountID: 1}},
		ClosedMonths: []string{"2020-01", "2020-02"},
		Transactions: []store.BankTransaction{
			{ID: 1, ImportID: 1, Decision: "accepted", BookedOn: "2020-01-15", AmountCents: 12000,
				Direction: store.DirExpense, Splits: []store.BankSplit{{MemberID: 1, Cents: 6000}, {MemberID: 2, Cents: 6000}}},
			{ID: 2, ImportID: 1, Decision: "pending", BookedOn: "2020-03-15", AmountCents: 99999, Direction: store.DirExpense},
			{ID: 3, ImportID: 1, Decision: "transfer", BookedOn: "2020-01-15", AmountCents: 99999, Direction: store.DirExpense},
		},
	}
	hybrid := WithBankLedger(d, l, BasisHybrid, "2020-04")
	for _, tc := range []struct {
		month, source string
		amount        int64
	}{
		{"2020-01", "actual", 12000}, {"2020-02", "actual", 0},
		{"2020-03", "plan_estimate", 1000}, {"2020-04", "plan", 1000}, {"2020-05", "plan", 1000},
	} {
		rep := BuildMonthReport(hybrid, tc.month, Everyone)
		if rep.ExpenseCents != tc.amount || rep.Source != tc.source {
			t.Fatalf("%s: %+v", tc.month, rep)
		}
		if l.ClosedMonths[0] == tc.month && !rep.IncomeRecorded {
			t.Fatal("closed month has known zero income")
		}
	}
	if got := BuildMonthReport(hybrid, "2020-01", 2).ExpenseCents; got != 6000 {
		t.Fatalf("member share %d", got)
	}
	actual := WithBankLedger(d, l, BasisActual, "2020-04")
	if got := BuildMonthReport(actual, "2020-03", Everyone); got.ExpenseCents != 0 || got.Source != "actual_partial" {
		t.Fatalf("pending included: %+v", got)
	}
	plan := WithBankLedger(d, l, BasisPlan, "2020-04")
	if got := BuildMonthReport(plan, "2020-01", Everyone).ExpenseCents; got != 1000 {
		t.Fatalf("plan mutated: %d", got)
	}
	if len(d.Bookings) != 1 || len(d.Splits) != 1 {
		t.Fatal("input modified")
	}
	months := []string{"2020-01", "2020-02"}
	prepared := Prepare(hybrid, months)
	if got := PeriodReport(prepared, months, Everyone).ExpenseCents; got != 6000 {
		t.Fatalf("period average %d", got)
	}
	matrix := BuildMatrix(prepared, months, Everyone)
	if matrix.Surplus.Cents[0] != -12000 || !matrix.Surplus.Active[1] {
		t.Fatalf("closed zero-income matrix: %+v", matrix.Surplus)
	}
}

func TestActualAllocationRejectsInvalidPlanAndBalancesCents(t *testing.T) {
	plan := store.Booking{SplitMode: store.SplitPercent, AmountCents: 100}
	shares := []store.BookingSplit{{MemberID: 2, Value: 50}, {MemberID: 1, Value: 50}}
	got := ActualAllocation(plan, shares, 101)
	if len(got) != 2 || got[0].MemberID != 1 || got[0].Cents != 51 || got[1].Cents != 50 {
		t.Fatalf("allocation: %+v", got)
	}
	shares[0].Value = 40
	if got := ActualAllocation(plan, shares, 101); len(got) != 0 {
		t.Fatalf("normalized incomplete plan: %+v", got)
	}
}
