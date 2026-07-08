package calc

import (
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestBuildMonthReport(t *testing.T) {
	members := []store.Member{
		{ID: 1, Name: "A"},
		{ID: 2, Name: "B"},
	}
	sections := []store.Section{{ID: 10, Name: "Wohnen"}}
	sid := int64(10)

	expenses := []store.Expense{
		{ID: 100, Name: "Miete", AmountCents: 120000, Frequency: store.FreqMonthly, SplitMode: store.SplitEqual, SectionID: &sid, CostNature: store.CostFix, BudgetClass: store.ClassNeed},
		{ID: 101, Name: "Versicherung", AmountCents: 5000, Frequency: store.FreqMonthly, SplitMode: store.SplitPercent, CostNature: store.CostFix, BudgetClass: store.ClassNeed},
		{ID: 102, Name: "Einkauf", AmountCents: 5000, Frequency: store.FreqWeekly, SplitMode: store.SplitEqual, CostNature: store.CostVariable, BudgetClass: store.ClassNeed},
	}
	splits := map[int64][]store.ExpenseSplit{
		100: {{MemberID: 1}, {MemberID: 2}},
		101: {{MemberID: 1, Value: 100}},
		102: {{MemberID: 1}, {MemberID: 2}},
	}
	incomes := []store.Income{
		{MemberID: 1, YearMonth: "2026-07", AmountCents: 300000},
		{MemberID: 2, YearMonth: "2026-07", AmountCents: 250000},
	}

	rep := BuildMonthReport("2026-07", members, sections, nil, expenses, splits, incomes)

	if rep.IncomeCents != 550000 {
		t.Errorf("income = %d, want 550000", rep.IncomeCents)
	}
	// 120000 + 5000 + 5000*52/12 = 146666.67 -> 146667
	if rep.ExpenseCents != 146667 {
		t.Errorf("expense = %d, want 146667", rep.ExpenseCents)
	}
	if rep.BalanceCents != 403333 {
		t.Errorf("balance = %d, want 403333", rep.BalanceCents)
	}

	// Member A: 60000 (Miete) + 5000 (Versicherung) + 10833 (Einkauf/2)
	if got := rep.Members[0].ExpenseCents; got != 75833 {
		t.Errorf("member A expense = %d, want 75833", got)
	}
	// Member B: 60000 + 10833
	if got := rep.Members[1].ExpenseCents; got != 70833 {
		t.Errorf("member B expense = %d, want 70833", got)
	}
	if got := rep.Members[0].BalanceCents; got != 300000-75833 {
		t.Errorf("member A balance = %d, want %d", got, 300000-75833)
	}

	if len(rep.Sections) != 2 { // Wohnen + Ohne Sektion
		t.Errorf("sections = %d, want 2", len(rep.Sections))
	}
	if rep.ByCostNature[store.CostFix] != 125000 {
		t.Errorf("fix total = %d, want 125000", rep.ByCostNature[store.CostFix])
	}
}

func TestExpenseActiveIn(t *testing.T) {
	recurring := store.Expense{ActiveFrom: "2026-05", ActiveUntil: "2026-08"}
	cases := map[string]bool{"2026-04": false, "2026-05": true, "2026-07": true, "2026-08": true, "2026-09": false}
	for month, want := range cases {
		if got := ExpenseActiveIn(recurring, month); got != want {
			t.Errorf("recurring active in %s = %v, want %v", month, got, want)
		}
	}

	oneoff := store.Expense{IsOneOff: true, OccurredOn: "2026-07-15"}
	if !ExpenseActiveIn(oneoff, "2026-07") {
		t.Error("one-off should be active in its month")
	}
	if ExpenseActiveIn(oneoff, "2026-08") {
		t.Error("one-off should not be active in other months")
	}
}
