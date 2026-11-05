package calc

import (
	"context"
	"fmt"
	"slices"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Finding is a reproducible discrepancy, or a data warning requiring review.
// Possible duplicates are never automatically removed.
type Finding struct {
	Warning                  bool
	Code                     string
	Name                     string
	Month                    string
	Basis                    Basis
	Member                   int64
	Expected, Actual         int64
	Numbers                  bool
	ExpectedText, ActualText string
}

// CompareText verifies the displayed text rather than a hidden numeric value.
func (r *PlausibilityResult) CompareText(f Finding, expected, actual string) {
	r.Checks++
	if expected != actual {
		f.ExpectedText, f.ActualText = expected, actual
		r.add(f)
	}
}

// PlausibilityResult is request-local and is never stored in the database.
type PlausibilityResult struct {
	Checks, Errors, Warnings int
	Findings                 []Finding
	Calculated               bool
}

func (r *PlausibilityResult) add(f Finding) {
	if f.Warning {
		r.Warnings++
	} else {
		r.Errors++
	}
	if len(r.Findings) < 100 {
		r.Findings = append(r.Findings, f)
	}
}

// Compare records an exact cent comparison, also used for rendered figures.
func (r *PlausibilityResult) Compare(f Finding, expected, actual int64) {
	r.Checks++
	if expected != actual {
		f.Expected, f.Actual, f.Numbers = expected, actual, true
		r.add(f)
	}
}

// CheckPlausibility checks one calendar year in every basis and member scope.
// Structural and duplicate checks cover the entire household. The independent
// reference uses source rows, not generated report rows or cached evaluations.
func CheckPlausibility(ctx context.Context, snapshot store.HouseholdSnapshot, year int, current string) (PlausibilityResult, error) {
	var out PlausibilityResult
	if year < 1 || year > 9999 {
		return out, fmt.Errorf("plausibility year: %w", store.ErrInvalid)
	}
	if err := checkSource(ctx, &out, snapshot); err != nil {
		return out, err
	}
	if out.Errors > 0 {
		return out, nil
	}
	months := make([]string, 12)
	for i := range months {
		months[i] = fmt.Sprintf("%04d-%02d", year, i+1)
	}
	scopes := []int64{Everyone}
	for _, m := range snapshot.Members {
		scopes = append(scopes, m.ID)
	}
	for _, basis := range []Basis{BasisPlan, BasisActual, BasisHybrid} {
		data := Prepare(WithBankLedger(FromSnapshot(snapshot), snapshot.Bank, basis, current), months)
		reference := make(map[string][]bookingValue, len(months))
		for _, month := range months {
			reference[month] = referenceMonth(snapshot, month, current, basis)
			checkLeaves(&out, data, month, basis, reference[month])
		}
		for _, scope := range scopes {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			matrix := BuildMatrix(data, months, scope)
			trend := Trend(data, months, scope)
			for i, month := range months {
				f := Finding{Code: "month", Month: month, Basis: basis, Member: scope}
				expected := referenceReport(reference[month], scope)
				checkReport(&out, f, expected, trend[i])
				out.CompareText(f, referenceSource(snapshot, month, current, basis), trend[i].Source)
				checkTags(&out, f, snapshot, reference[month], scope, trend[i])
				f.Code = "matrix"
				out.Compare(f, expected.IncomeCents, matrix.Band(BandIncome).Total.Cents[i])
				out.Compare(f, expected.ExpenseCents, matrix.Expense.Cents[i])
				known := referenceCovered(snapshot, reference[month], month, current, basis, scope)
				out.Compare(f, boolCents(known), boolCents(trend[i].IncomeRecorded))
				wantBalance := int64(0)
				if known {
					wantBalance = expected.BalanceCents
				}
				out.Compare(f, wantBalance, matrix.Surplus.Cents[i])
				out.Compare(f, boolCents(known), boolCents(matrix.Surplus.Active[i]))
				checkReport(&out, Finding{Code: "period", Month: month, Basis: basis, Member: scope}, expected, PeriodReport(data, []string{month}, scope))
			}
			checkMatrixRows(&out, Finding{Code: "matrix", Basis: basis, Member: scope}, matrix)
			checkMatrixProjection(&out, snapshot, reference, matrix, current, basis, scope)
			checkOtherViews(ctx, &out, data, snapshot, reference, months, scope)
			chart := BuildTrendChart(trend, 760, 260)
			for _, bar := range chart.Bars {
				expected := referenceReport(reference[bar.Month], scope)
				want := expected.ExpenseCents
				if bar.Income {
					want = expected.IncomeCents
				}
				out.Compare(Finding{Code: "chart", Basis: basis, Member: scope, Month: bar.Month}, want, bar.Cents)
			}
			for _, point := range chart.Line {
				out.Compare(Finding{Code: "chart", Basis: basis, Member: scope, Month: point.Month}, referenceReport(reference[point.Month], scope).BalanceCents, point.Cents)
			}
			for _, n := range []int{3, 6, 12} {
				for start := 0; start+n <= 12; start += n {
					period := months[start : start+n]
					ref := referencePeriod(reference, period)
					checkReport(&out, Finding{Code: "period", Basis: basis, Member: scope, Month: period[0] + " – " + period[n-1]}, referenceReport(ref, scope), PeriodReport(data, period, scope))
				}
			}
		}
		for _, month := range months {
			checkSettlement(&out, Finding{Code: "settlement", Basis: basis, Month: month}, Settlement(data, []string{month}))
			checkCostLines(&out, Finding{Code: "costs", Basis: basis, Month: month}, reference[month], Costs(data, []string{month}))
		}
		elapsed := make([]string, 0, 12)
		for _, month := range months {
			if month <= current {
				elapsed = append(elapsed, month)
			}
		}
		checkSettlement(&out, Finding{Code: "settlement", Basis: basis}, SettlementTotal(data, elapsed))
	}
	out.Calculated = true
	return out, ctx.Err()
}

func boolCents(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func checkLeaves(out *PlausibilityResult, d Data, month string, basis Basis, expected []bookingValue) {
	got := bookingValues(d, month)
	f := Finding{Code: "allocation", Month: month, Basis: basis}
	out.Compare(f, int64(len(expected)), int64(len(got)))
	byID := make(map[int64]bookingValue, len(got))
	for _, v := range got {
		byID[v.Booking.ID] = v
	}
	for _, v := range expected {
		f.Name = v.Booking.Name
		actual, ok := byID[v.Booking.ID]
		out.Compare(f, 1, boolCents(ok))
		out.Compare(f, v.Cents, actual.Cents)
		out.Compare(f, v.Unassigned, actual.Unassigned)
		out.Compare(f, int64(len(v.Shares)), int64(len(actual.Shares)))
		for id, cents := range v.Shares {
			f.Member = id
			out.Compare(f, cents, actual.Shares[id])
		}
		f.Member = 0
	}
}

func checkReport(out *PlausibilityResult, f Finding, want, got MonthReport) {
	out.Compare(f, want.IncomeCents, got.IncomeCents)
	out.Compare(f, want.ExpenseCents, got.ExpenseCents)
	out.Compare(f, want.BalanceCents, got.BalanceCents)
	out.Compare(f, want.UnassignedCents, got.UnassignedCents)
	for _, class := range []store.BudgetClass{store.ClassNeed, store.ClassWant, store.ClassSaving} {
		out.Compare(f, want.ByBudgetClass[class], got.ByBudgetClass[class])
	}
	for _, nature := range []store.CostNature{store.CostFix, store.CostVariable} {
		out.Compare(f, want.ByCostNature[nature], got.ByCostNature[nature])
	}
	for _, pair := range [][2][]LabeledTotal{{want.Categories, got.Categories}, {want.IncomeCategories, got.IncomeCategories}} {
		expected, actual := map[int64]int64{}, map[int64]int64{}
		for _, v := range pair[0] {
			expected[v.Key] = v.Cents
		}
		for _, v := range pair[1] {
			actual[v.Key] += v.Cents
		}
		for id, cents := range expected {
			out.Compare(f, cents, actual[id])
			delete(actual, id)
		}
		for _, cents := range actual {
			out.Compare(f, 0, cents)
		}
	}
}

func checkMatrixRows(out *PlausibilityResult, f Finding, matrix Matrix) {
	rows := []MatrixRow{matrix.Expense, matrix.Surplus}
	for _, band := range matrix.Bands {
		rows = append(rows, band.Total)
		for _, row := range band.Rows {
			rows = append(rows, row)
			rows = append(rows, row.Children...)
		}
	}
	for _, row := range rows {
		f.Name = row.Label
		var sum int64
		active := make([]int64, 0, len(row.Cents))
		for i, cents := range row.Cents {
			sum += cents
			if row.Active[i] {
				active = append(active, cents)
			}
		}
		out.Compare(f, sum, row.TotalCents)
		out.Compare(f, int64(len(active)), int64(row.ActiveMonths))
		if len(active) > 0 {
			slices.Sort(active)
			middle := active[len(active)/2]
			if len(active)%2 == 0 {
				middle = (active[len(active)/2-1] + middle) / 2
			}
			out.Compare(f, sum/int64(len(active)), row.MeanCents)
			out.Compare(f, middle, row.MedianCents)
		}
	}
}

func checkSettlement(out *PlausibilityResult, f Finding, r SettlementReport) {
	remaining := make(map[int64]int64, len(r.Positions))
	var net int64
	for _, p := range r.Positions {
		out.Compare(f, p.PaidCents-p.OwedCents, p.NetCents)
		remaining[p.Member.ID] = p.NetCents
		net += p.NetCents
	}
	out.Compare(f, 0, net)
	for _, t := range r.Transfers {
		remaining[t.From.ID] += t.Cents
		remaining[t.To.ID] -= t.Cents
	}
	if len(r.InvalidBookings) == 0 {
		for _, cents := range remaining {
			out.Compare(f, 0, cents)
		}
	} else {
		out.Compare(f, 0, int64(len(r.Transfers)))
	}
	for _, line := range r.Lines {
		out.Compare(f, line.Cents, sumCents(line.Shares))
	}
}
