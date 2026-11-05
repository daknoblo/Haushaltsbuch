package calc

import (
	"context"
	"slices"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func referenceSource(s store.HouseholdSnapshot, month, current string, basis Basis) string {
	closed := slices.Contains(s.Bank.ClosedMonths, month)
	if basis == BasisActual || (basis == BasisHybrid && month < current && closed) {
		if closed {
			return "actual"
		}
		return "actual_partial"
	}
	if basis == BasisHybrid && month < current {
		return "plan_estimate"
	}
	return "plan"
}

func referenceCovered(s store.HouseholdSnapshot, values []bookingValue, month, current string, basis Basis, member int64) bool {
	if referenceSource(s, month, current, basis) == "actual" {
		return true
	}
	for _, v := range values {
		if v.Booking.Direction != store.DirIncome {
			continue
		}
		if referenceCarries(s, v, member) {
			return true
		}
	}
	return false
}

func referenceCarries(s store.HouseholdSnapshot, v bookingValue, member int64) bool {
	if member == Everyone {
		return true
	}
	if _, ok := v.Shares[member]; ok {
		return true
	}
	if v.Booking.Direction == store.DirIncome {
		for _, row := range s.Bookings {
			if row.Booking.ID != v.Booking.ID {
				continue
			}
			for _, sp := range row.Splits {
				if sp.MemberID == member {
					return true
				}
			}
		}
		for _, row := range s.Bank.Transactions {
			if -row.ID != v.Booking.ID {
				continue
			}
			for _, sp := range row.Splits {
				if sp.MemberID == member {
					return true
				}
			}
		}
	}
	return false
}

func checkMatrixProjection(out *PlausibilityResult, s store.HouseholdSnapshot, reference map[string][]bookingValue, matrix Matrix, current string, basis Basis, scope int64) {
	bandOf := func(v bookingValue) string {
		if v.Booking.Direction == store.DirIncome {
			return BandIncome
		}
		if v.Booking.CostNature == store.CostFix {
			return BandFixed
		}
		return BandVariable
	}
	for _, band := range matrix.Bands {
		categories := map[int64]map[int64]bool{}
		for _, month := range matrix.Months {
			for _, v := range reference[month] {
				if bandOf(v) != band.Key || !referenceCarries(s, v, scope) {
					continue
				}
				if categories[v.Booking.CategoryID] == nil {
					categories[v.Booking.CategoryID] = map[int64]bool{}
				}
				categories[v.Booking.CategoryID][v.Booking.ID] = true
			}
		}
		f := Finding{Code: "matrix", Basis: basis, Member: scope}
		out.Compare(f, int64(len(categories)), int64(len(band.Rows)))
		checkRow := func(row MatrixRow, category, booking int64) {
			f.Name = row.Label
			for i, month := range matrix.Months {
				f.Month = month
				var cents int64
				active := booking == 0 && referenceSource(s, month, current, basis) == "actual"
				for _, v := range reference[month] {
					if bandOf(v) != band.Key || (category != 0 && category != v.Booking.CategoryID) || (booking != 0 && booking != v.Booking.ID) || !referenceCarries(s, v, scope) {
						continue
					}
					active = true
					if scope == Everyone {
						cents += v.Cents
					} else {
						cents += v.Shares[scope]
					}
				}
				out.Compare(f, cents, row.Cents[i])
				out.Compare(f, boolCents(active), boolCents(row.Active[i]))
			}
		}
		checkRow(band.Total, 0, 0)
		for _, row := range band.Rows {
			out.Compare(f, 1, boolCents(categories[row.Key] != nil))
			checkRow(row, row.Key, 0)
			children := len(categories[row.Key])
			if children <= 1 {
				children = 0
			}
			out.Compare(f, int64(children), int64(len(row.Children)))
			for _, child := range row.Children {
				out.Compare(f, 1, boolCents(categories[row.Key][child.Key]))
				checkRow(child, row.Key, child.Key)
			}
		}
	}
	for i, month := range matrix.Months {
		active := referenceSource(s, month, current, basis) == "actual"
		for _, v := range reference[month] {
			if v.Booking.Direction == store.DirExpense && referenceCarries(s, v, scope) {
				active = true
			}
		}
		out.Compare(Finding{Code: "matrix", Basis: basis, Member: scope, Month: month}, boolCents(active), boolCents(matrix.Expense.Active[i]))
	}
}

// MetricReference independently selects income-covered months. Source data must
// have passed CheckPlausibility before constructing a reference.
type MetricReference struct {
	Report, Household MonthReport
	Months            []string
}

// ReferenceMetrics verifies the dashboard's period selection as well as money.
func ReferenceMetrics(s store.HouseholdSnapshot, months []string, current string, basis Basis, member int64) MetricReference {
	var out MetricReference
	values := make(map[string][]bookingValue, len(months))
	for _, month := range months {
		values[month] = referenceMonth(s, month, current, basis)
		if referenceCovered(s, values[month], month, current, basis, member) {
			out.Months = append(out.Months, month)
		}
	}
	var period []bookingValue
	if len(out.Months) > 0 {
		period = referencePeriod(values, out.Months)
	}
	out.Report = referenceReport(period, member)
	out.Household = referenceReport(period, Everyone)
	return out
}

func checkOtherViews(ctx context.Context, out *PlausibilityResult, d Data, s store.HouseholdSnapshot, reference map[string][]bookingValue, months []string, scope int64) {
	f := Finding{Code: "chart", Basis: d.Basis, Member: scope}
	for _, grouping := range []string{GroupCategory, GroupClass} {
		stack := BuildStackChart(d, months, scope, grouping, 900, 320)
		actual := map[string]int64{}
		for _, column := range stack.Columns {
			var sum int64
			for _, segment := range column.Segments {
				sum += segment.Cents
			}
			f.Month = column.Month
			out.Compare(f, column.TotalCents, sum)
			actual[column.Month] = column.TotalCents
		}
		for _, month := range months {
			f.Month = month
			out.Compare(f, referenceReport(reference[month], scope).ExpenseCents, actual[month])
		}
	}
	metrics := ReferenceMetrics(s, months, d.CurrentMonth, d.Basis, scope)
	report := PeriodReport(d, metrics.Months, scope)
	checkReport(out, Finding{Code: "period", Basis: d.Basis, Member: scope}, metrics.Report, report)
	var fixed int64
	for _, row := range FixedCosts(d, metrics.Months, scope, 0) {
		fixed += row.Cents
	}
	out.Compare(Finding{Code: "costs", Basis: d.Basis, Member: scope}, metrics.Report.FixedCents(), fixed)
	graph := BuildSankey(ctx, d, report, metrics.Months, 900, 460)
	f.Month = ""
	out.Compare(f, max(metrics.Report.IncomeCents, metrics.Report.ExpenseCents), graph.TotalCents)
	inflow, outflow := map[string]int64{}, map[string]int64{}
	for _, link := range graph.Links {
		outflow[link.Source] += link.Cents
		inflow[link.Target] += link.Cents
	}
	for _, node := range graph.Nodes {
		out.Compare(f, max(inflow[node.ID], outflow[node.ID]), node.Cents)
		if inflow[node.ID] > 0 && outflow[node.ID] > 0 {
			out.Compare(f, inflow[node.ID], outflow[node.ID])
		}
	}
}

func checkCostLines(out *PlausibilityResult, f Finding, want []bookingValue, got CostReport) {
	expected := make(map[int64]bookingValue, len(want))
	var invalid int64
	for _, v := range want {
		if v.Booking.Direction != store.DirExpense {
			continue
		}
		if !v.Complete || len(v.Shares) == 0 {
			invalid++
			continue
		}
		expected[v.Booking.ID] = v
	}
	out.Compare(f, invalid, int64(len(got.InvalidBookings)))
	out.Compare(f, int64(len(expected)), int64(len(got.Lines)))
	for _, line := range got.Lines {
		v, ok := expected[line.Booking.ID]
		out.Compare(f, 1, boolCents(ok))
		out.Compare(f, v.Cents, line.Cents)
		for id, cents := range v.Shares {
			out.Compare(f, cents, line.Shares[id])
		}
	}
}

func checkTags(out *PlausibilityResult, f Finding, s store.HouseholdSnapshot, values []bookingValue, scope int64, report MonthReport) {
	links := map[int64][]int64{}
	for _, b := range s.Bookings {
		links[b.Booking.ID] = b.TagIDs
	}
	for _, p := range s.Bank.Transactions {
		links[-p.ID] = p.TagIDs
	}
	expected, actual := map[int64]int64{}, map[int64]int64{}
	for _, v := range values {
		if v.Booking.Direction != store.DirExpense {
			continue
		}
		cents := v.Cents
		if scope != Everyone {
			cents = v.Shares[scope]
		}
		for _, tag := range links[v.Booking.ID] {
			expected[tag] += cents
		}
	}
	for _, row := range report.Tags {
		actual[row.Key] += row.Cents
	}
	for id, cents := range expected {
		out.Compare(f, cents, actual[id])
		delete(actual, id)
	}
	for _, cents := range actual {
		out.Compare(f, 0, cents)
	}
}
