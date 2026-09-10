package calc

import (
	"reflect"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestIncomeCoverageUsesPresenceNotPositiveAmounts(t *testing.T) {
	d := Data{
		Members: members(), Categories: categories(),
		Bookings: []store.Booking{
			{ID: 1, CategoryID: 10, Direction: store.DirIncome, Frequency: store.FreqOnce, StartsOn: "2026-01-01", SplitMode: store.SplitEqual},
			{ID: 2, CategoryID: 20, Direction: store.DirExpense, Frequency: store.FreqMonthly, AmountCents: 271300, SplitMode: store.SplitEqual},
			{ID: 3, CategoryID: 10, Direction: store.DirIncome, Frequency: store.FreqOnce, StartsOn: "2026-03-01", AmountCents: 300000, SplitMode: store.SplitEqual},
			{ID: 4, CategoryID: 10, Direction: store.DirIncome, Frequency: store.FreqOnce, StartsOn: "2026-04-01", SplitMode: store.SplitFixed},
		},
		Splits: map[int64][]store.BookingSplit{
			1: {{MemberID: 1}}, 2: {{MemberID: 1}},
			3: {{MemberID: 2}}, 4: {{MemberID: 1, Value: 0}},
		},
	}
	months := []string{"2026-01", "2026-02", "2026-03", "2026-04"}
	for member, want := range map[int64][]string{
		Everyone: {"2026-01", "2026-03", "2026-04"},
		1:        {"2026-01", "2026-04"},
		2:        {"2026-03"},
	} {
		got := MonthsWithIncome(d, months, member)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("member %d: got %v, want %v", member, got, want)
		}
		matrix := BuildMatrix(d, months, member)
		for i, month := range months {
			rep := BuildMonthReport(d, month, member)
			if rep.IncomeRecorded != matrix.Surplus.Active[i] {
				t.Errorf("%s member %d: report and matrix disagree on income presence", month, member)
			}
			if !rep.IncomeRecorded && matrix.Surplus.Cents[i] != 0 {
				t.Errorf("%s: missing income created a false deficit", month)
			}
		}
	}
	rep := BuildMonthReport(d, "2026-01", 1)
	if !rep.IncomeRecorded || rep.IncomeCents != 0 || rep.BalanceCents != -271300 {
		t.Errorf("explicit zero must retain its genuine deficit: %+v", rep)
	}
}

func TestTrendChartDisplaysRecordedZero(t *testing.T) {
	chart := BuildTrendChart([]MonthReport{{Month: "2026-09", IncomeRecorded: true}}, 760, 260)
	if chart.Empty() || len(chart.Line) != 1 || chart.Line[0].Cents != 0 {
		t.Fatal("an explicitly recorded all-zero month is not missing data")
	}
}

func TestTrendChartLeavesGapsWithoutInventingDeficits(t *testing.T) {
	reps := []MonthReport{
		{Month: "2026-01", IncomeRecorded: true, IncomeCents: 269300, ExpenseCents: 271300, BalanceCents: -2000},
		{Month: "2026-02", ExpenseCents: 271300, BalanceCents: -271300},
		{Month: "2026-03", IncomeRecorded: true, IncomeCents: 0, ExpenseCents: 5000, BalanceCents: -5000},
		{Month: "2026-04", ExpenseCents: 271300, BalanceCents: -271300},
	}
	chart := BuildTrendChart(reps, 760, 260)
	if len(chart.Line) != 2 || len(chart.Segments) != 2 || len(chart.Segments[0]) != 1 || len(chart.Segments[1]) != 1 {
		t.Fatalf("missing month was bridged: %+v", chart.Segments)
	}
	if chart.Line[0].Cents != -2000 || chart.Line[1].Cents != -5000 {
		t.Errorf("genuine deficits must remain: %+v", chart.Line)
	}
	if chart.Grid[0].Cents <= -271300 {
		t.Error("unknown-income deficits still determine the axis")
	}
	var expenses, incomes int
	for _, bar := range chart.Bars {
		if bar.Income {
			incomes++
		} else {
			expenses++
		}
	}
	if expenses != 4 || incomes != 2 {
		t.Errorf("planned costs must remain, unknown income bars must not: %d/%d", expenses, incomes)
	}
}
