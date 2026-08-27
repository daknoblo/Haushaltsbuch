package calc

import (
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func ruleReport(income, need, want, saving int64) MonthReport {
	return MonthReport{
		IncomeCents:  income,
		ExpenseCents: need + want + saving,
		BalanceCents: income - need - want - saving,
		ByBudgetClass: map[store.BudgetClass]int64{
			store.ClassNeed:   need,
			store.ClassWant:   want,
			store.ClassSaving: saving,
		},
	}
}

func TestRuleRingNeedsIncome(t *testing.T) {
	t.Parallel()

	if got := BuildRuleRing(ruleReport(0, 100, 0, 0)); !got.Empty() {
		t.Fatalf("a ring without income should be empty, got %d arcs", len(got.Arcs))
	}
}

func TestRuleRingDrawsTheLeftoverIncome(t *testing.T) {
	t.Parallel()

	ring := BuildRuleRing(ruleReport(300000, 150000, 60000, 30000))
	if len(ring.Arcs) != 4 {
		t.Fatalf("want three buckets and a leftover, got %d arcs", len(ring.Arcs))
	}
	if last := ring.Arcs[3]; last.Class != "" || last.Cents != 60000 {
		t.Errorf("leftover arc = %q/%d, want the unclassed 60000", last.Class, last.Cents)
	}
	if ring.Surplus != 60000 {
		t.Errorf("Surplus = %d, want 60000", ring.Surplus)
	}
}

// Overspending used to push the last bucket off the end of the circle, which
// left the ring claiming nothing was saved while the panel beside it said
// otherwise.
func TestEveryBucketStaysOnTheRingWhenSpendingRunsPastIncome(t *testing.T) {
	t.Parallel()

	ring := BuildRuleRing(ruleReport(300000, 200000, 180000, 40000))
	if len(ring.Arcs) != 3 {
		t.Fatalf("want all three buckets, got %d arcs", len(ring.Arcs))
	}
	for _, a := range ring.Arcs {
		if a.Class == "" {
			t.Error("there is no income left over, so nothing should be drawn for it")
		}
		if !strings.HasPrefix(a.Path, "M ") {
			t.Errorf("%s has no path", a.Class)
		}
	}
	if ring.Surplus != 0 {
		t.Errorf("Surplus = %d, want none", ring.Surplus)
	}
}

// The marks say where the rule puts its boundaries in money. If they were a
// fixed share of the circle they would drift outwards with the overspending,
// and the rule would agree with whatever was spent.
func TestTheMarksStayWithIncomeWhenTheRingGrows(t *testing.T) {
	t.Parallel()

	within := BuildRuleRing(ruleReport(300000, 100000, 50000, 30000))
	beyond := BuildRuleRing(ruleReport(300000, 200000, 180000, 40000))

	if len(within.Marks) != 2 || len(beyond.Marks) != 2 {
		t.Fatal("both rings should carry the 50 and 80 marks")
	}
	for i := range within.Marks {
		if within.Marks[i].Path == beyond.Marks[i].Path {
			t.Errorf("mark %d did not move although the ring holds more than income", within.Marks[i].Percent)
		}
	}
}
