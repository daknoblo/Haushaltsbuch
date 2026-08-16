package store

import (
	"context"
	"errors"
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

func seededStore(t *testing.T) (*Store, context.Context, Household) {
	t.Helper()
	ctx := t.Context()
	st := newTestStore(t)
	if err := st.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hs, err := st.ListHouseholds(ctx)
	if err != nil {
		t.Fatalf("list households: %v", err)
	}
	return st, ctx, hs[0]
}

func TestEnsureSeed(t *testing.T) {
	ctx := t.Context()
	st := newTestStore(t)
	if err := st.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Second call must be idempotent.
	if err := st.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed again: %v", err)
	}

	hs, err := st.ListHouseholds(ctx)
	if err != nil {
		t.Fatalf("list households: %v", err)
	}
	if len(hs) != 1 {
		t.Fatalf("want 1 household, got %d", len(hs))
	}

	active, err := st.ActiveHouseholdID(ctx)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active != hs[0].ID {
		t.Fatalf("active household = %d, want %d", active, hs[0].ID)
	}

	members, err := st.ListMembers(ctx, hs[0].ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("want 2 members, got %d", len(members))
	}
}

func TestExpenseWithSplits(t *testing.T) {
	st, ctx, hh := seededStore(t)
	members, _ := st.ListMembers(ctx, hh.ID)

	all := []SplitInput{{MemberID: members[0].ID}, {MemberID: members[1].ID}}
	e, err := st.CreateExpense(ctx, Expense{
		HouseholdID: hh.ID,
		Name:        "Miete",
		AmountCents: 120000,
		Frequency:   FreqMonthly,
		CostNature:  CostFix,
		BudgetClass: ClassNeed,
		SplitMode:   SplitEqual,
	}, all)
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}

	splits, err := st.ListSplits(ctx, e.ID)
	if err != nil {
		t.Fatalf("list splits: %v", err)
	}
	if len(splits) != 2 {
		t.Fatalf("want 2 splits, got %d", len(splits))
	}

	// Replacing with a single member drops the other one.
	if err := st.ReplaceSplits(ctx, e.ID, hh.ID, []SplitInput{{MemberID: members[0].ID, Value: 60}}); err != nil {
		t.Fatalf("replace splits: %v", err)
	}
	splits, _ = st.ListSplits(ctx, e.ID)
	if len(splits) != 1 {
		t.Fatalf("want 1 split after replace, got %d", len(splits))
	}
	if splits[0].Value != 60 {
		t.Fatalf("split value = %v, want 60", splits[0].Value)
	}
}

func TestExpenseRejectsForeignSectionAndMember(t *testing.T) {
	st, ctx, hh := seededStore(t)

	other, err := st.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	otherSections, _ := st.ListSections(ctx, other.ID)
	otherMembers, _ := st.ListMembers(ctx, other.ID)

	e, err := st.CreateExpense(ctx, Expense{
		HouseholdID: hh.ID,
		Name:        "Test",
		SectionID:   &otherSections[0].ID,
		Frequency:   FreqMonthly,
		CostNature:  CostFix,
		BudgetClass: ClassNeed,
		SplitMode:   SplitEqual,
	}, []SplitInput{{MemberID: otherMembers[0].ID}})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if e.SectionID != nil {
		t.Errorf("section of another household was accepted: %v", *e.SectionID)
	}
	splits, _ := st.ListSplits(ctx, e.ID)
	if len(splits) != 0 {
		t.Errorf("member of another household was accepted: %+v", splits)
	}
}

func TestDeleteExpenseIsHouseholdScoped(t *testing.T) {
	st, ctx, hh := seededStore(t)
	e, err := st.CreateExpense(ctx, Expense{
		HouseholdID: hh.ID,
		Name:        "Test",
		Frequency:   FreqMonthly,
		CostNature:  CostFix,
		BudgetClass: ClassNeed,
		SplitMode:   SplitEqual,
	}, nil)
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}

	other, err := st.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	if err := st.DeleteExpense(ctx, other.ID, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete across households = %v, want ErrNotFound", err)
	}
	if _, err := st.GetExpense(ctx, e.ID); err != nil {
		t.Fatalf("expense must still exist: %v", err)
	}
}

func TestIncomes(t *testing.T) {
	st, ctx, hh := seededStore(t)
	members, _ := st.ListMembers(ctx, hh.ID)

	if _, err := st.CreateIncome(ctx, hh.ID, members[0].ID, "2026-07", "Gehalt", 300000); err != nil {
		t.Fatalf("create income: %v", err)
	}
	if _, err := st.CreateIncome(ctx, hh.ID, members[0].ID, "2026-07", "Bonus", 50000); err != nil {
		t.Fatalf("create bonus: %v", err)
	}

	ins, err := st.ListIncomes(ctx, hh.ID, "2026-07")
	if err != nil {
		t.Fatalf("list incomes: %v", err)
	}
	if len(ins) != 2 {
		t.Fatalf("want 2 income lines, got %d", len(ins))
	}

	n, err := st.CopyIncomes(ctx, hh.ID, "2026-07", "2026-08")
	if err != nil {
		t.Fatalf("copy incomes: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 copied, got %d", n)
	}
	ins, _ = st.ListIncomes(ctx, hh.ID, "2026-08")
	if len(ins) != 2 {
		t.Fatalf("want 2 income lines in august, got %d", len(ins))
	}
}

func TestCopyIncomesRejectsDuplicates(t *testing.T) {
	st, ctx, hh := seededStore(t)
	members, _ := st.ListMembers(ctx, hh.ID)
	if _, err := st.CreateIncome(ctx, hh.ID, members[0].ID, "2026-07", "Gehalt", 300000); err != nil {
		t.Fatalf("create income: %v", err)
	}
	if _, err := st.CopyIncomes(ctx, hh.ID, "2026-07", "2026-08"); err != nil {
		t.Fatalf("first copy: %v", err)
	}

	if _, err := st.CopyIncomes(ctx, hh.ID, "2026-07", "2026-08"); !errors.Is(err, ErrCopyTargetNotEmpty) {
		t.Fatalf("second copy = %v, want ErrCopyTargetNotEmpty", err)
	}
	if _, err := st.CopyIncomes(ctx, hh.ID, "2026-07", "2026-07"); !errors.Is(err, ErrCopyTargetNotEmpty) {
		t.Fatalf("copy onto itself = %v, want ErrCopyTargetNotEmpty", err)
	}

	ins, _ := st.ListIncomes(ctx, hh.ID, "2026-08")
	if len(ins) != 1 {
		t.Fatalf("want 1 income line in august, got %d", len(ins))
	}
}

func TestCreateIncomeRejectsForeignMember(t *testing.T) {
	st, ctx, hh := seededStore(t)
	other, err := st.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	otherMembers, _ := st.ListMembers(ctx, other.ID)

	if _, err := st.CreateIncome(ctx, hh.ID, otherMembers[0].ID, "2026-07", "X", 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create income = %v, want ErrNotFound", err)
	}
}

func TestReorderSections(t *testing.T) {
	st, ctx, hh := seededStore(t)
	before, _ := st.ListSections(ctx, hh.ID)
	if len(before) < 3 {
		t.Fatalf("need at least 3 sections, got %d", len(before))
	}

	if err := st.MoveSection(ctx, hh.ID, before[2].ID, -1); err != nil {
		t.Fatalf("move up: %v", err)
	}
	after, _ := st.ListSections(ctx, hh.ID)
	if after[1].ID != before[2].ID || after[2].ID != before[1].ID {
		t.Fatalf("order after move up = %v", ids(after))
	}

	// Moving the first entry further up is a no-op rather than an error.
	if err := st.MoveSection(ctx, hh.ID, after[0].ID, -1); err != nil {
		t.Fatalf("move first up: %v", err)
	}
	again, _ := st.ListSections(ctx, hh.ID)
	if again[0].ID != after[0].ID {
		t.Fatalf("first entry moved unexpectedly: %v", ids(again))
	}
}

func ids(secs []Section) []int64 {
	out := make([]int64, len(secs))
	for i, s := range secs {
		out[i] = s.ID
	}
	return out
}

func TestUpdatesReportMissingRows(t *testing.T) {
	st, ctx, hh := seededStore(t)
	if err := st.RenameSection(ctx, hh.ID, 999999, "X"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename missing section = %v, want ErrNotFound", err)
	}
	if err := st.UpdateMember(ctx, hh.ID, 999999, "X", "#000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing member = %v, want ErrNotFound", err)
	}
	if err := st.DeleteCategory(ctx, hh.ID, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing category = %v, want ErrNotFound", err)
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
