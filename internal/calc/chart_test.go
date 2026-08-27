package calc

import "testing"

// A month that ends in the red has to be drawn below the baseline, not clipped
// off at the bottom of the plot.
func TestTrendChartMakesRoomForADeficit(t *testing.T) {
	reps := []MonthReport{
		{Month: "2026-01", IncomeCents: 300000, ExpenseCents: 200000, BalanceCents: 100000},
		{Month: "2026-02", IncomeCents: 0, ExpenseCents: 340000, BalanceCents: -340000},
	}
	c := BuildTrendChart(reps, 760, 260)

	if c.Zero <= c.Top || c.Zero >= c.Bottom {
		t.Errorf("baseline at %.1f, want it inside the plot %.1f–%.1f", c.Zero, c.Top, c.Bottom)
	}
	if len(c.Line) != 2 {
		t.Fatalf("surplus line has %d points, want 2", len(c.Line))
	}
	if c.Line[0].Y >= c.Zero {
		t.Error("a surplus was drawn below the baseline")
	}
	if c.Line[1].Y <= c.Zero {
		t.Error("a deficit was drawn above the baseline")
	}
	if c.Line[1].Y > c.Bottom {
		t.Errorf("the deficit at %.1f fell out of the plot", c.Line[1].Y)
	}
}

// Without a deficit the baseline stays at the foot of the plot, so the chart
// looks the way it always did.
func TestTrendChartKeepsTheBaselineWithoutADeficit(t *testing.T) {
	reps := []MonthReport{{Month: "2026-01", IncomeCents: 300000, ExpenseCents: 200000, BalanceCents: 100000}}
	if c := BuildTrendChart(reps, 760, 260); c.Zero != c.Bottom {
		t.Errorf("baseline at %.1f, want the foot of the plot %.1f", c.Zero, c.Bottom)
	}
}

func TestStackChartStacksEveryMonthToItsTotal(t *testing.T) {
	months := calendarYear()
	c := BuildStackChart(budgetBook(), months, Everyone, GroupCategory, 900, 320)

	if len(c.Columns) != 12 {
		t.Fatalf("got %d columns, want 12", len(c.Columns))
	}
	// Rent, broadcast fee, savings plan and groceries run every month.
	want := int64(98100 + 1800 + 100000 + 25000)
	for _, col := range c.Columns {
		if col.TotalCents != want {
			t.Errorf("%s totals %d, want %d", col.Month, col.TotalCents, want)
		}
	}
	// The heaviest group sits at the foot of the column.
	first := c.Columns[0].Segments[0]
	if first.Y+first.Height < c.Columns[0].Segments[1].Y+c.Columns[0].Segments[1].Height {
		t.Error("the largest group was not stacked first")
	}
}

// Grouping by class is the same money seen differently, so the columns keep
// their height and only the blocks change.
func TestStackChartByClassKeepsTheTotals(t *testing.T) {
	months := calendarYear()
	byCat := BuildStackChart(budgetBook(), months, Everyone, GroupCategory, 900, 320)
	byClass := BuildStackChart(budgetBook(), months, Everyone, GroupClass, 900, 320)

	for i := range byCat.Columns {
		if byCat.Columns[i].TotalCents != byClass.Columns[i].TotalCents {
			t.Errorf("%s: %d by category, %d by class",
				byCat.Columns[i].Month, byCat.Columns[i].TotalCents, byClass.Columns[i].TotalCents)
		}
	}
	if len(byClass.Keys) != 2 {
		t.Errorf("got %d classes, want need and saving", len(byClass.Keys))
	}
}

func TestCleanGroupingFallsBackToCategories(t *testing.T) {
	if got := CleanGrouping("nonsense"); got != GroupCategory {
		t.Errorf("grouping = %q, want %q", got, GroupCategory)
	}
}
