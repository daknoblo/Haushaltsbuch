package calc

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestMonthlyRoundingAgreesAcrossReportsAndMembers(t *testing.T) {
	d := sharedPlan()
	for i := range d.Bookings {
		d.Bookings[i].AmountCents = 100
		d.Bookings[i].Frequency = store.FreqYearly
	}
	report := BuildMonthReport(d, "2026-05", Everyone)
	matrix := BuildMatrix(d, month("2026-05"), Everyone)
	if report.ExpenseCents != 16 || matrix.Expense.Cents[0] != 16 {
		t.Fatalf("two yearly EUR 1 bookings: report %d, matrix %d, want 16 cents each",
			report.ExpenseCents, matrix.Expense.Cents[0])
	}
	d.Bookings = d.Bookings[:1]
	d.Bookings[0].AmountCents = 101
	d.Bookings[0].Frequency = store.FreqMonthly
	report = BuildMonthReport(d, "2026-05", Everyone)
	if report.Members[0].ExpenseCents != 51 || report.Members[1].ExpenseCents != 50 {
		t.Fatalf("EUR 1.01 must be 51+50 cents, got %+v", report.Members)
	}
	for _, member := range d.Members {
		personal := BuildMonthReport(d, "2026-05", member.ID)
		ledger := Settlement(d, month("2026-05")).Ledger(member.ID)
		if personal.ExpenseCents != ledger[0].OwedCents {
			t.Errorf("member %d: report differs from settlement", member.ID)
		}
	}
}

func assertBreakdowns(t *testing.T, rep MonthReport) {
	t.Helper()
	var categories, incomeCategories, cost, class, carried int64
	for _, c := range rep.Categories {
		categories += c.Cents
	}
	for _, c := range rep.IncomeCategories {
		incomeCategories += c.Cents
	}
	for _, c := range rep.ByCostNature {
		cost += c
	}
	for _, c := range rep.ByBudgetClass {
		class += c
	}
	for _, m := range rep.Members {
		carried += m.ExpenseCents
	}
	for name, got := range map[string]int64{
		"categories": categories, "cost nature": cost, "budget class": class,
		"members and unassigned": carried + rep.UnassignedCents,
	} {
		if got != rep.ExpenseCents {
			t.Errorf("%s sum %d, expenses %d", name, got, rep.ExpenseCents)
		}
	}
	if incomeCategories != rep.IncomeCents {
		t.Errorf("income categories %d, income %d", incomeCategories, rep.IncomeCents)
	}
}

func TestCentConservationAcrossEveryView(t *testing.T) {
	d := planData()
	for i := range d.Bookings {
		d.Bookings[i].AmountCents = int64(101 + 2*i)
		d.Bookings[i].Frequency = store.FreqYearly
		d.Bookings[i].Interval = 2
		d.Bookings[i].Settle = true
		d.Bookings[i].PayerMemberID = memberRef(1)
	}
	d.Bookings[0].Frequency = store.FreqMonthly
	d.Bookings[2].StartsOn = "2026-02-01"
	d.Bookings[4].EndsOn = "2026-03-31"
	d.Overrides = map[int64][]store.BookingOverride{
		4: {{StartsOn: "2026-02-01", EndsOn: "2026-02-28", AmountCents: 0}},
	}
	months := []string{"2026-01", "2026-02", "2026-03", "2026-04"}
	prepared := Prepare(d, months, months[:2])
	for _, member := range []int64{Everyone, 1, 2} {
		period := PeriodReport(prepared, months, member)
		assertBreakdowns(t, period)
		if !reflect.DeepEqual(period, PeriodReport(d, months, member)) {
			t.Fatal("prepared period differs from on-demand evaluation")
		}
		var fixed int64
		for _, cost := range FixedCosts(prepared, months, member, 0) {
			fixed += cost.Cents
		}
		if fixed != period.FixedCents() {
			t.Errorf("fixed cost list %d, report %d", fixed, period.FixedCents())
		}
		matrix := BuildMatrix(prepared, months, member)
		for _, grouping := range []string{GroupCategory, GroupClass} {
			stack := BuildStackChart(prepared, months, member, grouping, 900, 320)
			for i, month := range months {
				report := BuildMonthReport(prepared, month, member)
				assertBreakdowns(t, report)
				if matrix.Expense.Cents[i] != report.ExpenseCents || stack.Columns[i].TotalCents != report.ExpenseCents {
					t.Errorf("%s member %d: matrix, stack and report disagree", month, member)
				}
			}
		}
		sankey := BuildSankey(t.Context(), prepared, period, months, 900, 460)
		in, out := make(map[string]int64), make(map[string]int64)
		for _, link := range sankey.Links {
			in[link.Target] += link.Cents
			out[link.Source] += link.Cents
		}
		for _, node := range sankey.Nodes {
			if node.Layer == layerTrunk || (node.Layer == layerClass && out[node.ID] > 0) {
				if in[node.ID] != out[node.ID] {
					t.Errorf("Sankey node %s: in %d, out %d", node.ID, in[node.ID], out[node.ID])
				}
			}
		}
	}
	household := PeriodReport(prepared, months, Everyone)
	for _, member := range household.Members {
		personal := PeriodReport(prepared, months, member.Member.ID)
		if personal.ExpenseCents != member.ExpenseCents || personal.IncomeCents != member.IncomeCents {
			t.Errorf("period member %d differs between scopes", member.Member.ID)
		}
	}
	assertSettlementBalances(t, Settlement(prepared, months))
}

func TestIncompleteSplitsKeepTheirRemainder(t *testing.T) {
	for _, value := range []float64{30, 75} {
		d := sharedPlan()
		d.Bookings = d.Bookings[:1]
		d.Bookings[0].SplitMode = store.SplitPercent
		d.Bookings[0].AmountCents = 101
		d.Splits[1][0].Value, d.Splits[1][1].Value = value, value
		for _, months := range [][]string{month("2026-01"), {"2026-01", "2026-02", "2026-03"}} {
			report := PeriodReport(d, months, Everyone)
			assertBreakdowns(t, report)
			if report.UnassignedCents == 0 || len(Settlement(d, months).InvalidBookings) != 1 {
				t.Errorf("invalid percentages %v were silently normalized", value)
			}
		}
	}
}

func TestCentTiesAreIndependentOfInputOrder(t *testing.T) {
	d := sharedPlan()
	for i := range d.Bookings {
		d.Bookings[i].AmountCents = 101
		d.Bookings[i].Frequency = store.FreqOnce
		d.Bookings[i].StartsOn = "2026-01-01"
	}
	months := []string{"2026-01", "2026-02", "2026-03"}
	before := PeriodReport(d, months, Everyone)
	slices.Reverse(d.Bookings)
	slices.Reverse(d.Splits[1])
	after := PeriodReport(d, months, Everyone)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("rounding depends on iteration order: before %+v, after %+v", before, after)
	}
}

func BenchmarkReportEvaluation(b *testing.B) {
	d := planData()
	months := calendarYear()
	for _, prepared := range []bool{false, true} {
		name := "on-demand"
		if prepared {
			name = "prepared"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				data := d
				if prepared {
					data = Prepare(data, months)
				}
				rep := PeriodReport(data, months, Everyone)
				Trend(data, months, Everyone)
				BuildMatrix(data, months, Everyone)
				BuildStackChart(data, months, Everyone, GroupCategory, 900, 320)
				FixedCosts(data, months, Everyone, 8)
				BuildSankey(context.Background(), data, rep, months, 900, 460)
				Settlement(data, months)
			}
		})
	}
}
