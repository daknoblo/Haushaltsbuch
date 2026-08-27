package calc

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// month wraps a single month as the range the Sankey builder expects.
func month(m string) []string { return []string{m} }

func members() []store.Member {
	return []store.Member{{ID: 1, Name: "Anna"}, {ID: 2, Name: "Ben"}}
}

func categories() []store.Category {
	return []store.Category{
		{ID: 10, Name: "Gehalt", Classification: store.DirIncome},
		{ID: 20, Name: "Miete", Classification: store.DirExpense},
		{ID: 21, Name: "Lebensmittel", Classification: store.DirExpense},
	}
}

func TestActiveIn(t *testing.T) {
	cases := []struct {
		name  string
		b     store.Booking
		month string
		want  bool
	}{
		{"one-off in its month", store.Booking{Frequency: store.FreqOnce, StartsOn: "2026-03-14"}, "2026-03", true},
		{"one-off in another month", store.Booking{Frequency: store.FreqOnce, StartsOn: "2026-03-14"}, "2026-04", false},
		{"recurring before start", store.Booking{Frequency: store.FreqMonthly, StartsOn: "2026-05-01"}, "2026-04", false},
		{"recurring at start", store.Booking{Frequency: store.FreqMonthly, StartsOn: "2026-05-01"}, "2026-05", true},
		{"recurring after end", store.Booking{Frequency: store.FreqMonthly, StartsOn: "2026-01-01", EndsOn: "2026-06-01"}, "2026-07", false},
		{"recurring in the end month", store.Booking{Frequency: store.FreqMonthly, StartsOn: "2026-01-01", EndsOn: "2026-06-01"}, "2026-06", true},
		{"open ended", store.Booking{Frequency: store.FreqYearly}, "2099-12", true},
	}
	for _, c := range cases {
		if got := ActiveIn(c.b, c.month); got != c.want {
			t.Errorf("%s: ActiveIn = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMonthlyCentsSpreadsRecurringAmounts(t *testing.T) {
	cases := []struct {
		name string
		b    store.Booking
		want int64
	}{
		{"monthly", store.Booking{AmountCents: 100000, Frequency: store.FreqMonthly, Interval: 1}, 100000},
		{"yearly is a twelfth", store.Booking{AmountCents: 120000, Frequency: store.FreqYearly, Interval: 1}, 10000},
		{"quarterly is a third", store.Booking{AmountCents: 30000, Frequency: store.FreqQuarterly, Interval: 1}, 10000},
		{"every second month", store.Booking{AmountCents: 10000, Frequency: store.FreqMonthly, Interval: 2}, 5000},
		{"one-off counts fully", store.Booking{AmountCents: 59900, Frequency: store.FreqOnce, Interval: 1}, 59900},
		{"interval is ignored for one-offs", store.Booking{AmountCents: 59900, Frequency: store.FreqOnce, Interval: 4}, 59900},
	}
	for _, c := range cases {
		if got := MonthlyCents(c.b, nil, "2026-05"); got != c.want {
			t.Errorf("%s: MonthlyCents = %d, want %d", c.name, got, c.want)
		}
	}
}

// planData is a small but complete household: two salaries, a shared rent split
// 60/40, groceries split equally and a savings rate.
func planData() Data {
	return Data{
		Members:    members(),
		Categories: categories(),
		Tags:       []store.Tag{{ID: 5, Name: "fix"}},
		Bookings: []store.Booking{
			{ID: 1, CategoryID: 10, Direction: store.DirIncome, AmountCents: 300000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitPercent},
			{ID: 2, CategoryID: 10, Direction: store.DirIncome, AmountCents: 200000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitPercent},
			{ID: 3, CategoryID: 20, Direction: store.DirExpense, AmountCents: 150000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitPercent,
				CostNature: store.CostFix, BudgetClass: store.ClassNeed},
			{ID: 4, CategoryID: 21, Direction: store.DirExpense, AmountCents: 60000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitEqual,
				CostNature: store.CostVariable, BudgetClass: store.ClassNeed},
			{ID: 5, CategoryID: 20, Direction: store.DirExpense, AmountCents: 50000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitEqual,
				CostNature: store.CostFix, BudgetClass: store.ClassSaving},
		},
		Splits: map[int64][]store.BookingSplit{
			1: {{BookingID: 1, MemberID: 1, Value: 100}},
			2: {{BookingID: 2, MemberID: 2, Value: 100}},
			3: {{BookingID: 3, MemberID: 1, Value: 60}, {BookingID: 3, MemberID: 2, Value: 40}},
			4: {{BookingID: 4, MemberID: 1}, {BookingID: 4, MemberID: 2}},
			5: {{BookingID: 5, MemberID: 1}, {BookingID: 5, MemberID: 2}},
		},
		TagLinks: map[int64][]int64{3: {5}, 5: {5}},
	}
}

func TestBuildMonthReport(t *testing.T) {
	rep := BuildMonthReport(planData(), "2026-05", Everyone)

	if rep.IncomeCents != 500000 {
		t.Errorf("income = %d, want 500000", rep.IncomeCents)
	}
	if rep.ExpenseCents != 260000 {
		t.Errorf("expenses = %d, want 260000", rep.ExpenseCents)
	}
	if rep.BalanceCents != 240000 {
		t.Errorf("balance = %d, want 240000", rep.BalanceCents)
	}
	if rep.FixedCents() != 200000 {
		t.Errorf("fixed = %d, want 200000", rep.FixedCents())
	}
	if rep.VariableCents() != 60000 {
		t.Errorf("variable = %d, want 60000", rep.VariableCents())
	}
	if rep.SavingCents() != 50000 {
		t.Errorf("savings = %d, want 50000", rep.SavingCents())
	}

	// Anna: 300000 income, 60 % of rent (90000) + half of groceries (30000)
	// + half of the savings rate (25000) = 145000.
	anna := rep.Members[0]
	if anna.IncomeCents != 300000 || anna.ExpenseCents != 145000 {
		t.Errorf("Anna = %+v, want income 300000 / expenses 145000", anna)
	}
	ben := rep.Members[1]
	if ben.IncomeCents != 200000 || ben.ExpenseCents != 115000 {
		t.Errorf("Ben = %+v, want income 200000 / expenses 115000", ben)
	}
	if rep.UnassignedCents != 0 {
		t.Errorf("unassigned = %d, want 0", rep.UnassignedCents)
	}

	// Categories are sorted by size: rent + savings share one category.
	if len(rep.Categories) != 2 || rep.Categories[0].Label != "Miete" || rep.Categories[0].Cents != 200000 {
		t.Errorf("categories = %+v", rep.Categories)
	}
	if len(rep.IncomeCategories) != 1 || rep.IncomeCategories[0].Cents != 500000 {
		t.Errorf("income categories = %+v", rep.IncomeCategories)
	}
	if len(rep.Tags) != 1 || rep.Tags[0].Cents != 200000 {
		t.Errorf("tags = %+v", rep.Tags)
	}
}

func TestSavingsAndFixedCostRates(t *testing.T) {
	rep := BuildMonthReport(planData(), "2026-05", Everyone)

	// (50000 saved + 240000 surplus) / 500000
	if got := rep.SavingsRate(); math.Abs(got-58) > 0.01 {
		t.Errorf("savings rate = %.2f, want 58", got)
	}
	if got := rep.FixedCostRate(); math.Abs(got-40) > 0.01 {
		t.Errorf("fixed cost rate = %.2f, want 40", got)
	}

	empty := BuildMonthReport(Data{}, "2026-05", Everyone)
	if empty.SavingsRate() != 0 || empty.FixedCostRate() != 0 {
		t.Error("rates without income must be 0, not NaN")
	}
}

func TestUnassignedExpenseIsReported(t *testing.T) {
	d := Data{
		Members:    members(),
		Categories: categories(),
		Bookings: []store.Booking{{
			ID: 1, CategoryID: 20, Direction: store.DirExpense, AmountCents: 10000,
			Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitPercent,
			CostNature: store.CostFix, BudgetClass: store.ClassNeed,
		}},
		// Only 30 % is attributed, so 70 % has no owner.
		Splits: map[int64][]store.BookingSplit{1: {{BookingID: 1, MemberID: 1, Value: 30}}},
	}
	rep := BuildMonthReport(d, "2026-05", Everyone)
	if rep.UnassignedCents != 7000 {
		t.Errorf("unassigned = %d, want 7000", rep.UnassignedCents)
	}
}

// Nobody picked means nobody carries it: guessing the whole household here
// would put a figure on people who were never asked.
func TestBookingWithoutSplitsBelongsToNobody(t *testing.T) {
	d := Data{
		Members:    members(),
		Categories: categories(),
		Bookings: []store.Booking{{
			ID: 1, CategoryID: 20, Direction: store.DirExpense, AmountCents: 10000,
			Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitEqual,
			CostNature: store.CostFix, BudgetClass: store.ClassNeed,
		}},
	}

	rep := BuildMonthReport(d, "2026-05", Everyone)
	if rep.ExpenseCents != 10000 {
		t.Errorf("household expenses = %d, want the booking to keep counting", rep.ExpenseCents)
	}
	if rep.UnassignedCents != 10000 {
		t.Errorf("unassigned = %d, want 10000", rep.UnassignedCents)
	}
	for _, m := range rep.Members {
		if m.ExpenseCents != 0 {
			t.Errorf("%s carries %d of a booking nobody was picked for", m.Member.Name, m.ExpenseCents)
		}
	}
	if got := BuildMonthReport(d, "2026-05", 1).ExpenseCents; got != 0 {
		t.Errorf("person view = %d, want the booking to stay out of it", got)
	}
}

func TestBuildSankeyBalances(t *testing.T) {
	d := planData()
	rep := BuildMonthReport(d, "2026-05", Everyone)
	s := BuildSankey(context.Background(), d, rep, month("2026-05"), 900, 460)

	if s.Empty() {
		t.Fatal("sankey is empty")
	}
	if s.Deficit {
		t.Error("a plan with a surplus must not be flagged as a deficit")
	}

	// Everything entering the trunk has to leave it again, otherwise the
	// diagram would silently lose money.
	var in, out int64
	for _, l := range s.Links {
		if l.Target == "trunk" {
			in += l.Cents
		}
		if l.Source == "trunk" {
			out += l.Cents
		}
	}
	if in != rep.IncomeCents {
		t.Errorf("into the trunk = %d, want %d", in, rep.IncomeCents)
	}
	if in != out {
		t.Errorf("trunk is unbalanced: in %d, out %d", in, out)
	}

	for _, n := range s.Nodes {
		if n.Height < 0 || n.Y < 0 || n.Y+n.Height > s.Height+0.01 {
			t.Errorf("node %s lies outside the canvas: y=%.2f h=%.2f", n.ID, n.Y, n.Height)
		}
		// A caption carries the name plus the amount and its share, so it needs
		// considerably more room than the name alone.
		if n.LabelX() > s.Width-190 {
			t.Errorf("caption of %s starts at %.2f and would be clipped (width %.0f)", n.ID, n.LabelX(), s.Width)
		}
	}
	for _, l := range s.Links {
		if l.Path == "" {
			t.Errorf("link %s->%s has no path", l.Source, l.Target)
		}
	}
}

// A node passes its amount through, so counting inflow and outflow together
// would double its value and draw it twice as tall as it belongs.
func TestSankeyNodeValueIsThroughputNotSum(t *testing.T) {
	d := planData()
	rep := BuildMonthReport(d, "2026-05", Everyone)
	s := BuildSankey(context.Background(), d, rep, month("2026-05"), 900, 460)

	byID := make(map[string]SankeyNode, len(s.Nodes))
	for _, n := range s.Nodes {
		byID[n.ID] = n
	}

	if got := byID["trunk"].Cents; got != rep.IncomeCents {
		t.Errorf("trunk = %d, want %d", got, rep.IncomeCents)
	}
	// Every share is measured against the trunk, so 100 % has to be its value.
	if s.TotalCents != byID["trunk"].Cents {
		t.Errorf("total = %d, want the trunk's %d", s.TotalCents, byID["trunk"].Cents)
	}
	if got := s.Share(byID["trunk"].Cents); got != 100 {
		t.Errorf("trunk share = %.2f, want 100", got)
	}
	if got := byID["class-need"].Cents; got != rep.ByBudgetClass[store.ClassNeed] {
		t.Errorf("need class = %d, want %d", got, rep.ByBudgetClass[store.ClassNeed])
	}
	if got := byID["surplus"].Cents; got != rep.BalanceCents {
		t.Errorf("surplus = %d, want %d", got, rep.BalanceCents)
	}

	// The trunk is the busiest node, so nothing may be drawn taller than it.
	for _, n := range s.Nodes {
		if n.Height > byID["trunk"].Height+0.01 {
			t.Errorf("node %s is taller than the trunk", n.ID)
		}
	}
}

func TestBuildSankeyDeficitGetsAWithdrawalNode(t *testing.T) {
	d := Data{
		Members:    members(),
		Categories: categories(),
		Bookings: []store.Booking{
			{ID: 1, CategoryID: 10, Direction: store.DirIncome, AmountCents: 100000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitEqual},
			{ID: 2, CategoryID: 20, Direction: store.DirExpense, AmountCents: 150000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitEqual,
				CostNature: store.CostFix, BudgetClass: store.ClassNeed},
		},
	}
	rep := BuildMonthReport(d, "2026-05", Everyone)
	s := BuildSankey(context.Background(), d, rep, month("2026-05"), 900, 460)

	if !s.Deficit {
		t.Fatal("overspending must be flagged as a deficit")
	}
	var withdrawal int64
	for _, l := range s.Links {
		if l.Source == "withdrawal" {
			withdrawal = l.Cents
		}
	}
	if withdrawal != 50000 {
		t.Errorf("withdrawal = %d, want 50000", withdrawal)
	}
	// A surplus node would be wrong when the plan is short.
	for _, n := range s.Nodes {
		if n.ID == "surplus" {
			t.Error("a deficit must not produce a surplus node")
		}
	}
}

func TestBuildSankeyEmptyWithoutData(t *testing.T) {
	if !BuildSankey(context.Background(), Data{}, MonthReport{}, nil, 900, 460).Empty() {
		t.Error("sankey without figures must be empty")
	}
}

func TestSankeyLabelsFollowTheLanguage(t *testing.T) {
	d := planData()
	rep := BuildMonthReport(d, "2026-05", Everyone)
	ctx := i18n.WithLang(context.Background(), i18n.English)
	s := BuildSankey(ctx, d, rep, month("2026-05"), 900, 460)

	labels := make(map[string]string, len(s.Nodes))
	for _, n := range s.Nodes {
		labels[n.ID] = n.Label
	}
	for id, want := range map[string]string{
		"trunk":      "Income",
		"class-need": "Need",
		"surplus":    "Surplus",
	} {
		if labels[id] != want {
			t.Errorf("node %s is labeled %q, want %q", id, labels[id], want)
		}
	}
}

func TestPeriodReportAveragesTheRange(t *testing.T) {
	d := Data{
		Members:    members(),
		Categories: categories(),
		Bookings: []store.Booking{
			{ID: 1, CategoryID: 10, Direction: store.DirIncome, AmountCents: 300000,
				Frequency: store.FreqMonthly, Interval: 1, SplitMode: store.SplitEqual},
			{ID: 2, CategoryID: 20, Direction: store.DirExpense, AmountCents: 30000,
				Frequency: store.FreqOnce, Interval: 1, StartsOn: "2026-05-14",
				SplitMode: store.SplitEqual, CostNature: store.CostFix, BudgetClass: store.ClassNeed},
		},
	}
	rep := PeriodReport(d, []string{"2026-04", "2026-05", "2026-06"}, Everyone)

	if rep.Month != "2026-06" {
		t.Errorf("month = %q, want the last month of the range", rep.Month)
	}
	if rep.IncomeCents != 300000 {
		t.Errorf("income = %d, want 300000", rep.IncomeCents)
	}
	// The one-off falls into one month of three.
	if rep.ExpenseCents != 10000 {
		t.Errorf("expenses = %d, want 10000", rep.ExpenseCents)
	}
	if rep.BalanceCents != 290000 {
		t.Errorf("balance = %d, want 290000", rep.BalanceCents)
	}
	if len(rep.Categories) != 1 || rep.Categories[0].Cents != 10000 {
		t.Errorf("categories = %+v", rep.Categories)
	}
	if rep.ByBudgetClass[store.ClassNeed] != 10000 {
		t.Errorf("need class = %d, want 10000", rep.ByBudgetClass[store.ClassNeed])
	}

	// A single month has to stay exactly BuildMonthReport.
	if got, want := PeriodReport(d, month("2026-05"), Everyone), BuildMonthReport(d, "2026-05", Everyone); got.ExpenseCents != want.ExpenseCents {
		t.Errorf("single month = %d, want %d", got.ExpenseCents, want.ExpenseCents)
	}
}

// A period is a stretch of time, not a list of the months that happened to have
// figures in them. A cost that only starts halfway through costs half as much
// per month over the whole range, which is what makes ranges comparable.
func TestPeriodReportDividesByEveryMonthOfTheRange(t *testing.T) {
	d := Data{
		Members:    members(),
		Categories: categories(),
		Bookings: []store.Booking{{
			ID: 1, CategoryID: 20, Direction: store.DirExpense, AmountCents: 60000,
			Frequency: store.FreqMonthly, Interval: 1, StartsOn: "2026-05-01",
			SplitMode: store.SplitEqual, CostNature: store.CostFix, BudgetClass: store.ClassNeed,
		}},
	}
	// The booking only exists in the last two months of the range.
	rep := PeriodReport(d, []string{"2026-03", "2026-04", "2026-05", "2026-06"}, Everyone)
	if rep.ExpenseCents != 30000 {
		t.Errorf("expenses = %d, want 30000 for two months out of four", rep.ExpenseCents)
	}
}

// The figures of the household book this was modeled on: a salary paid in
// three months of the year averages to a twelfth of the year, not a third.
func TestYearAverageCountsEmptyMonths(t *testing.T) {
	d := Data{
		Members:    members(),
		Categories: categories(),
		Bookings: []store.Booking{
			{ID: 1, CategoryID: 10, Direction: store.DirIncome, AmountCents: 370100,
				Frequency: store.FreqOnce, StartsOn: "2026-01-15", SplitMode: store.SplitEqual},
			{ID: 2, CategoryID: 10, Direction: store.DirIncome, AmountCents: 454100,
				Frequency: store.FreqOnce, StartsOn: "2026-02-15", SplitMode: store.SplitEqual},
			{ID: 3, CategoryID: 10, Direction: store.DirIncome, AmountCents: 360500,
				Frequency: store.FreqOnce, StartsOn: "2026-03-15", SplitMode: store.SplitEqual},
		},
	}
	rep := PeriodReport(d, calendarYear(2026), Everyone)
	if rep.IncomeCents != 98725 {
		t.Errorf("income = %d, want 98725 (1.184.700 / 12)", rep.IncomeCents)
	}
}

func calendarYear(y int) []string {
	out := make([]string, 0, 12)
	for m := 1; m <= 12; m++ {
		out = append(out, fmt.Sprintf("%d-%02d", y, m))
	}
	return out
}

func TestFixedCostsAverageAndLimit(t *testing.T) {
	d := planData()
	got := FixedCosts(d, []string{"2026-04", "2026-05"}, Everyone, 0)
	if len(got) != 2 {
		t.Fatalf("got %d fixed bookings, want 2", len(got))
	}
	if got[0].Cents != 150000 || got[1].Cents != 50000 {
		t.Errorf("fixed costs = %+v", got)
	}
	if limited := FixedCosts(d, month("2026-05"), Everyone, 1); len(limited) != 1 {
		t.Errorf("limit was ignored: %+v", limited)
	}
}

func TestTrendKeepsMonthOrder(t *testing.T) {
	d := planData()
	reps := Trend(d, []string{"2026-04", "2026-05", "2026-06"}, Everyone)
	if len(reps) != 3 {
		t.Fatalf("got %d reports, want 3", len(reps))
	}
	for i, want := range []string{"2026-04", "2026-05", "2026-06"} {
		if reps[i].Month != want {
			t.Errorf("report %d is for %s, want %s", i, reps[i].Month, want)
		}
	}
}
