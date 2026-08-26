// Package calc computes monthly budget figures from stored bookings: it
// normalises recurring bookings to monthly equivalents, applies temporary
// amount overrides, allocates shares to members according to each booking's
// split mode and aggregates the results into the figures the overview, the
// dashboard and the Sankey diagram need.
package calc

import (
	"math"
	"sort"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// MaxSplitCents bounds a fixed split value, mirroring the limit the input layer
// enforces so a stored outlier cannot overflow an aggregate.
const MaxSplitCents = 1_000_000_000_000

// Everyone is the member scope that keeps a report at household level.
const Everyone int64 = 0

// Data is one household's complete planning input. Passing it as a whole keeps
// the aggregation functions from growing an unreadable parameter list and makes
// it obvious that every report is built from a single consistent snapshot.
type Data struct {
	Members    []store.Member
	Categories []store.Category
	Tags       []store.Tag
	Bookings   []store.Booking
	Splits     map[int64][]store.BookingSplit
	TagLinks   map[int64][]int64
	Overrides  map[int64][]store.BookingOverride
}

// MemberBalance holds the income, allocated expense share and resulting balance
// for a single member in a month.
type MemberBalance struct {
	Member       store.Member
	IncomeCents  int64
	ExpenseCents int64
	BalanceCents int64
}

// LabeledTotal is a named monetary total used for breakdowns. Key is the id of
// the underlying row so the UI can link back to it.
type LabeledTotal struct {
	Key   int64
	Label string
	Color string
	Icon  string
	Cents int64
}

// MonthReport is the aggregated result for one month of one household.
type MonthReport struct {
	Month string
	// Member is the scope the figures were built for, Everyone for the whole
	// household.
	Member           int64
	IncomeCents      int64
	ExpenseCents     int64
	UnassignedCents  int64
	BalanceCents     int64
	Members          []MemberBalance
	Categories       []LabeledTotal
	IncomeCategories []LabeledTotal
	Tags             []LabeledTotal
	ByCostNature     map[store.CostNature]int64
	ByBudgetClass    map[store.BudgetClass]int64
}

// FixedCents is everything that leaves reliably every month. It is read off the
// cost-nature breakdown rather than summed a second time, so the tile and the
// breakdown below it cannot disagree.
func (r MonthReport) FixedCents() int64 { return r.ByCostNature[store.CostFix] }

// VariableCents is everything that is not a fixed cost.
func (r MonthReport) VariableCents() int64 { return r.ByCostNature[store.CostVariable] }

// SavingCents is the amount deliberately put aside, i.e. everything classified
// as a saving in the 50/30/20 breakdown.
func (r MonthReport) SavingCents() int64 { return r.ByBudgetClass[store.ClassSaving] }

// SavingsRate is the share of net income that is either put aside on purpose or
// left over. It returns 0 without income.
func (r MonthReport) SavingsRate() float64 {
	if r.IncomeCents <= 0 {
		return 0
	}
	return float64(r.SavingCents()+r.BalanceCents) / float64(r.IncomeCents) * 100
}

// FixedCostRate is the share of net income consumed by fixed costs.
func (r MonthReport) FixedCostRate() float64 {
	if r.IncomeCents <= 0 {
		return 0
	}
	return float64(r.FixedCents()) / float64(r.IncomeCents) * 100
}

// ActiveIn reports whether a booking contributes to the given YYYY-MM month.
// A one-off booking counts only in the month it falls into; a recurring one
// counts in every month of its active range.
func ActiveIn(b store.Booking, month string) bool {
	if !b.Frequency.Recurring() {
		return len(b.StartsOn) >= 7 && b.StartsOn[:7] == month
	}
	return coversMonth(b.StartsOn, b.EndsOn, month)
}

// coversMonth reports whether a YYYY-MM-DD range contains a YYYY-MM month.
// An empty bound is open.
func coversMonth(from, until, month string) bool {
	if len(from) >= 7 && month < from[:7] {
		return false
	}
	if len(until) >= 7 && month > until[:7] {
		return false
	}
	return true
}

// AmountFor returns the amount a booking carries in a month. A temporary
// override — an introductory price, say — wins over the base amount; the last
// matching one does, so a later correction beats an earlier one. Only a
// recurring amount can be overridden: a booking that happens once has a single
// amount, and the dialog hides the overrides along with the rhythm, so honoring
// them would apply a discount nobody can see or delete.
func AmountFor(b store.Booking, overrides []store.BookingOverride, month string) int64 {
	amount := b.AmountCents
	if !b.Frequency.Recurring() {
		return amount
	}
	for _, o := range overrides {
		if coversMonth(o.StartsOn, o.EndsOn, month) {
			amount = o.AmountCents
		}
	}
	return amount
}

// monthlyFactor is how much of a booking's amount falls into a single month.
func monthlyFactor(b store.Booking) float64 {
	factor := b.Frequency.MonthlyFactor()
	if b.Frequency.Recurring() && b.Interval > 1 {
		factor /= float64(b.Interval)
	}
	return factor
}

// MonthlyCents returns the rounded monthly-equivalent amount of a booking in a
// given month, overrides included.
func MonthlyCents(b store.Booking, overrides []store.BookingOverride, month string) int64 {
	return round(float64(AmountFor(b, overrides, month)) * monthlyFactor(b))
}

// allocate distributes the monthly amount of a booking among members according
// to its split mode and returns the per-member allocation plus the remainder
// that could not be attributed to anyone.
func allocate(amount float64, b store.Booking, splits []store.BookingSplit) (map[int64]float64, float64) {
	res := make(map[int64]float64)

	switch b.SplitMode {
	case store.SplitPercent:
		for _, s := range splits {
			res[s.MemberID] += amount * ClampPercent(s.Value) / 100.0
		}
	case store.SplitFixed:
		factor := monthlyFactor(b)
		for _, s := range splits {
			res[s.MemberID] += clampAmount(s.Value) * factor
		}
	default: // equal
		// No split at all means nobody carries it: the amount stays unassigned
		// rather than being spread over people who were never picked.
		ids := make([]int64, 0, len(splits))
		for _, s := range splits {
			ids = append(ids, s.MemberID)
		}
		if len(ids) > 0 {
			share := amount / float64(len(ids))
			for _, id := range ids {
				res[id] += share
			}
		}
	}

	var assigned float64
	for _, v := range res {
		assigned += v
	}
	unassigned := amount - assigned
	if math.Abs(unassigned) < 0.5 {
		unassigned = 0
	}
	return res, unassigned
}

// BuildMonthReport aggregates all figures of a household for one month. With a
// member other than Everyone the report only contains that member's own share,
// which is what "what does this cost me" means.
func BuildMonthReport(d Data, month string, member int64) MonthReport {
	rep := MonthReport{
		Month:         month,
		Member:        member,
		ByCostNature:  make(map[store.CostNature]int64),
		ByBudgetClass: make(map[store.BudgetClass]int64),
	}

	var (
		income        float64
		expense       float64
		unassigned    float64
		memIncome     = make(map[int64]float64)
		memExpense    = make(map[int64]float64)
		byCategory    = make(map[int64]float64)
		byIncomeCat   = make(map[int64]float64)
		byTag         = make(map[int64]float64)
		byCostNature  = make(map[store.CostNature]float64)
		byBudgetClass = make(map[store.BudgetClass]float64)
	)

	for _, b := range d.Bookings {
		if !ActiveIn(b, month) {
			continue
		}
		amount := float64(AmountFor(b, d.Overrides[b.ID], month)) * monthlyFactor(b)
		shares, rest := allocate(amount, b, d.Splits[b.ID])

		if member != Everyone {
			share, ok := shares[member]
			if !ok {
				continue
			}
			amount, rest = share, 0
			shares = map[int64]float64{member: share}
		}

		if b.Direction == store.DirIncome {
			income += amount
			for id, v := range shares {
				memIncome[id] += v
			}
			byIncomeCat[b.CategoryID] += amount
			continue
		}

		expense += amount
		unassigned += rest
		for id, v := range shares {
			memExpense[id] += v
		}

		byCategory[b.CategoryID] += amount
		for _, tagID := range d.TagLinks[b.ID] {
			byTag[tagID] += amount
		}
		byCostNature[b.CostNature] += amount
		byBudgetClass[b.BudgetClass] += amount
	}

	rep.IncomeCents = round(income)
	rep.ExpenseCents = round(expense)
	rep.UnassignedCents = round(unassigned)
	rep.BalanceCents = rep.IncomeCents - rep.ExpenseCents

	for _, m := range d.Members {
		if member != Everyone && m.ID != member {
			continue
		}
		in := round(memIncome[m.ID])
		out := round(memExpense[m.ID])
		rep.Members = append(rep.Members, MemberBalance{
			Member:       m,
			IncomeCents:  in,
			ExpenseCents: out,
			BalanceCents: in - out,
		})
	}

	for k, v := range byCostNature {
		rep.ByCostNature[k] = round(v)
	}
	for k, v := range byBudgetClass {
		rep.ByBudgetClass[k] = round(v)
	}

	for _, c := range d.Categories {
		totals := byCategory
		target := &rep.Categories
		if c.Classification == store.DirIncome {
			totals, target = byIncomeCat, &rep.IncomeCategories
		}
		if v := round(totals[c.ID]); v != 0 {
			*target = append(*target,
				LabeledTotal{Key: c.ID, Label: c.Name, Color: c.Color, Icon: c.Icon, Cents: v})
		}
	}
	for _, t := range d.Tags {
		if v := round(byTag[t.ID]); v != 0 {
			rep.Tags = append(rep.Tags, LabeledTotal{Key: t.ID, Label: t.Name, Color: t.Color, Cents: v})
		}
	}

	sortByCentsDesc(rep.Categories)
	sortByCentsDesc(rep.IncomeCategories)
	sortByCentsDesc(rep.Tags)
	return rep
}

// Trend builds one report per month, oldest first.
func Trend(d Data, months []string, member int64) []MonthReport {
	out := make([]MonthReport, 0, len(months))
	for _, m := range months {
		out = append(out, BuildMonthReport(d, m, member))
	}
	return out
}

// PeriodReport condenses a range of months into the figures of a typical
// month, so every breakdown answers for the selected period instead of only
// its last month. A single-month range yields exactly BuildMonthReport.
func PeriodReport(d Data, months []string, member int64) MonthReport {
	rep := average(Trend(d, activeMonths(d, months), member))
	rep.Member = member
	if len(months) > 0 {
		rep.Month = months[len(months)-1]
	}
	return rep
}

// activeMonths returns the months in which at least one booking contributes,
// falling back to the full range when none does. A range reaching back before
// the first booking would otherwise divide by empty months and understate
// every average.
func activeMonths(d Data, months []string) []string {
	out := make([]string, 0, len(months))
	for _, m := range months {
		for _, b := range d.Bookings {
			if AmountFor(b, d.Overrides[b.ID], m) != 0 && ActiveIn(b, m) {
				out = append(out, m)
				break
			}
		}
	}
	if len(out) == 0 {
		return months
	}
	return out
}

// average merges month reports into the figures of a typical month.
func average(reps []MonthReport) MonthReport {
	out := MonthReport{
		ByCostNature:  make(map[store.CostNature]int64),
		ByBudgetClass: make(map[store.BudgetClass]int64),
	}
	n := int64(len(reps))
	if n == 0 {
		return out
	}

	byMember := make(map[int64]int, len(reps[0].Members))
	for _, r := range reps {
		out.IncomeCents += r.IncomeCents
		out.ExpenseCents += r.ExpenseCents
		out.UnassignedCents += r.UnassignedCents
		for k, v := range r.ByCostNature {
			out.ByCostNature[k] += v
		}
		for k, v := range r.ByBudgetClass {
			out.ByBudgetClass[k] += v
		}
		for _, mb := range r.Members {
			i, ok := byMember[mb.Member.ID]
			if !ok {
				out.Members = append(out.Members, MemberBalance{Member: mb.Member})
				i = len(out.Members) - 1
				byMember[mb.Member.ID] = i
			}
			out.Members[i].IncomeCents += mb.IncomeCents
			out.Members[i].ExpenseCents += mb.ExpenseCents
		}
	}

	out.IncomeCents /= n
	out.ExpenseCents /= n
	out.UnassignedCents /= n
	out.BalanceCents = out.IncomeCents - out.ExpenseCents
	for k := range out.ByCostNature {
		out.ByCostNature[k] /= n
	}
	for k := range out.ByBudgetClass {
		out.ByBudgetClass[k] /= n
	}
	for i := range out.Members {
		out.Members[i].IncomeCents /= n
		out.Members[i].ExpenseCents /= n
		out.Members[i].BalanceCents = out.Members[i].IncomeCents - out.Members[i].ExpenseCents
	}

	out.Categories = averageTotals(reps, n, func(r MonthReport) []LabeledTotal { return r.Categories })
	out.IncomeCategories = averageTotals(reps, n, func(r MonthReport) []LabeledTotal { return r.IncomeCategories })
	out.Tags = averageTotals(reps, n, func(r MonthReport) []LabeledTotal { return r.Tags })
	return out
}

// averageTotals merges one breakdown across months, keyed by the row id.
func averageTotals(reps []MonthReport, n int64, pick func(MonthReport) []LabeledTotal) []LabeledTotal {
	sums := make(map[int64]*LabeledTotal)
	order := make([]int64, 0, len(pick(reps[0])))
	for _, r := range reps {
		for _, t := range pick(r) {
			cur, ok := sums[t.Key]
			if !ok {
				merged := t
				merged.Cents = 0
				sums[t.Key] = &merged
				order = append(order, t.Key)
				cur = &merged
			}
			cur.Cents += t.Cents
		}
	}

	out := make([]LabeledTotal, 0, len(order))
	for _, k := range order {
		t := *sums[k]
		if t.Cents /= n; t.Cents != 0 {
			out = append(out, t)
		}
	}
	sortByCentsDesc(out)
	return out
}

// FixedCosts lists the fixed-cost bookings of a period as monthly averages,
// largest first, because that is the list worth renegotiating. A limit of 0
// keeps all of them.
func FixedCosts(d Data, months []string, member int64, limit int) []LabeledTotal {
	active := activeMonths(d, months)
	n := int64(len(active))
	if n == 0 {
		return nil
	}

	sums := make(map[int64]int64)
	out := make([]LabeledTotal, 0, len(d.Bookings))
	for _, m := range active {
		for _, b := range d.Bookings {
			if b.Direction != store.DirExpense || b.CostNature != store.CostFix || !ActiveIn(b, m) {
				continue
			}
			amount := float64(AmountFor(b, d.Overrides[b.ID], m)) * monthlyFactor(b)
			if member != Everyone {
				shares, _ := allocate(amount, b, d.Splits[b.ID])
				share, ok := shares[member]
				if !ok {
					continue
				}
				amount = share
			}
			if _, ok := sums[b.ID]; !ok {
				out = append(out, LabeledTotal{Key: b.ID, Label: b.Name})
			}
			sums[b.ID] += round(amount)
		}
	}

	kept := out[:0]
	for _, t := range out {
		if t.Cents = sums[t.Key] / n; t.Cents != 0 {
			kept = append(kept, t)
		}
	}
	sortByCentsDesc(kept)
	if limit > 0 && len(kept) > limit {
		kept = kept[:limit]
	}
	return kept
}

func sortByCentsDesc(t []LabeledTotal) {
	sort.SliceStable(t, func(i, j int) bool { return t[i].Cents > t[j].Cents })
}

func round(f float64) int64 {
	return int64(math.Round(f))
}

// ClampPercent keeps a percentage within 0-100 and rejects NaN, so a single
// rule decides what a share may be for both the input layer and the report.
func ClampPercent(v float64) float64 {
	switch {
	case math.IsNaN(v), v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}

func clampAmount(v float64) float64 {
	switch {
	case math.IsNaN(v):
		return 0
	case v > MaxSplitCents:
		return MaxSplitCents
	case v < -MaxSplitCents:
		return -MaxSplitCents
	default:
		return v
	}
}
