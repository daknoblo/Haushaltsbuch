package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestEnsureSeed(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureSeed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Second call must be idempotent.
	if err := st.EnsureSeed(); err != nil {
		t.Fatalf("seed again: %v", err)
	}

	hs, err := st.ListHouseholds()
	if err != nil {
		t.Fatalf("list households: %v", err)
	}
	if len(hs) != 1 {
		t.Fatalf("want 1 household, got %d", len(hs))
	}

	active, err := st.ActiveHouseholdID()
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active != hs[0].ID {
		t.Fatalf("active household = %d, want %d", active, hs[0].ID)
	}

	members, err := st.ListMembers(hs[0].ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("want 2 members, got %d", len(members))
	}
}

func TestExpenseWithSplits(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureSeed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hs, _ := st.ListHouseholds()
	members, _ := st.ListMembers(hs[0].ID)

	e, err := st.CreateExpense(Expense{
		HouseholdID: hs[0].ID,
		Name:        "Miete",
		AmountCents: 120000,
		Frequency:   FreqMonthly,
		CostNature:  CostFix,
		BudgetClass: ClassNeed,
		SplitMode:   SplitEqual,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}

	for _, m := range members {
		if err := st.AddSplitMember(e.ID, m.ID); err != nil {
			t.Fatalf("add split: %v", err)
		}
	}
	// Adding the same member again must be a no-op.
	if err := st.AddSplitMember(e.ID, members[0].ID); err != nil {
		t.Fatalf("add split idempotent: %v", err)
	}

	splits, err := st.ListSplits(e.ID)
	if err != nil {
		t.Fatalf("list splits: %v", err)
	}
	if len(splits) != 2 {
		t.Fatalf("want 2 splits, got %d", len(splits))
	}

	if err := st.RemoveSplitMember(e.ID, members[1].ID); err != nil {
		t.Fatalf("remove split: %v", err)
	}
	splits, _ = st.ListSplits(e.ID)
	if len(splits) != 1 {
		t.Fatalf("want 1 split after remove, got %d", len(splits))
	}
}

func TestIncomes(t *testing.T) {
	st := newTestStore(t)
	if err := st.EnsureSeed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hs, _ := st.ListHouseholds()
	members, _ := st.ListMembers(hs[0].ID)

	if _, err := st.CreateIncome(hs[0].ID, members[0].ID, "2026-07", "Gehalt", 300000); err != nil {
		t.Fatalf("create income: %v", err)
	}
	if _, err := st.CreateIncome(hs[0].ID, members[0].ID, "2026-07", "Bonus", 50000); err != nil {
		t.Fatalf("create bonus: %v", err)
	}

	ins, err := st.ListIncomes(hs[0].ID, "2026-07")
	if err != nil {
		t.Fatalf("list incomes: %v", err)
	}
	if len(ins) != 2 {
		t.Fatalf("want 2 income lines, got %d", len(ins))
	}

	n, err := st.CopyIncomes(hs[0].ID, "2026-07", "2026-08")
	if err != nil {
		t.Fatalf("copy incomes: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 copied, got %d", n)
	}
	ins, _ = st.ListIncomes(hs[0].ID, "2026-08")
	if len(ins) != 2 {
		t.Fatalf("want 2 income lines in august, got %d", len(ins))
	}
}

func TestFrequencyMonthlyFactor(t *testing.T) {
	cases := map[Frequency]float64{
		FreqMonthly: 1.0,
		FreqWeekly:  52.0 / 12.0,
		FreqYearly:  1.0 / 12.0,
	}
	for f, want := range cases {
		if got := f.MonthlyFactor(); got != want {
			t.Errorf("%s factor = %v, want %v", f, got, want)
		}
	}
}
