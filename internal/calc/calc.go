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
	evaluated  *evaluatedData
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
	// IncomeRecorded also holds true for an explicitly entered zero income.
	IncomeRecorded bool
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

// TargetCents is what the 50/30/20 rule allots to a class. The rule measures
// against net income, so it answers 0 without any.
func (r MonthReport) TargetCents(c store.BudgetClass) int64 {
	if r.IncomeCents <= 0 {
		return 0
	}
	return r.IncomeCents * int64(c.TargetPercent()) / 100
}

// OverIncomeCents is how far the classified expenses reach beyond net income.
// It is 0 while they fit, and is what keeps a 100 % bar from quietly hiding a
// household that spends more than it earns.
func (r MonthReport) OverIncomeCents() int64 {
	var sum int64
	for _, v := range r.ByBudgetClass {
		sum += v
	}
	if sum <= r.IncomeCents {
		return 0
	}
	return sum - r.IncomeCents
}

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

// BuildMonthReport aggregates all figures of a household for one month. With a
// member other than Everyone the report only contains that member's own share,
// which is what "what does this cost me" means.
func BuildMonthReport(d Data, month string, member int64) MonthReport {
	return buildReport(d, bookingValues(d, month), month, member)
}

func buildReport(d Data, values []bookingValue, month string, member int64) MonthReport {
	rep := MonthReport{
		Month:         month,
		Member:        member,
		ByCostNature:  make(map[store.CostNature]int64),
		ByBudgetClass: make(map[store.BudgetClass]int64),
	}

	var (
		income        int64
		expense       int64
		unassigned    int64
		memIncome     = make(map[int64]int64)
		memExpense    = make(map[int64]int64)
		byCategory    = make(map[int64]int64)
		byIncomeCat   = make(map[int64]int64)
		byTag         = make(map[int64]int64)
		byCostNature  = make(map[store.CostNature]int64)
		byBudgetClass = make(map[store.BudgetClass]int64)
	)

	for _, value := range values {
		b := value.Booking
		rep.IncomeRecorded = rep.IncomeRecorded || incomeForScope(b, d.Splits[b.ID], member)
		amount, ok := value.scoped(member)
		if !ok {
			continue
		}
		shares, rest := value.Shares, value.Unassigned
		if member != Everyone {
			rest = 0
		}

		if b.Direction == store.DirIncome {
			income += amount
			for id, v := range shares {
				if member == Everyone || member == id {
					memIncome[id] += v
				}
			}
			byIncomeCat[b.CategoryID] += amount
			continue
		}

		expense += amount
		unassigned += rest
		for id, v := range shares {
			if member == Everyone || member == id {
				memExpense[id] += v
			}
		}

		byCategory[b.CategoryID] += amount
		for _, tagID := range d.TagLinks[b.ID] {
			byTag[tagID] += amount
		}
		byCostNature[b.CostNature] += amount
		byBudgetClass[b.BudgetClass] += amount
	}

	rep.IncomeCents = income
	rep.ExpenseCents = expense
	rep.UnassignedCents = unassigned
	rep.BalanceCents = rep.IncomeCents - rep.ExpenseCents

	for _, m := range d.Members {
		if member != Everyone && m.ID != member {
			continue
		}
		in := memIncome[m.ID]
		out := memExpense[m.ID]
		rep.Members = append(rep.Members, MemberBalance{
			Member:       m,
			IncomeCents:  in,
			ExpenseCents: out,
			BalanceCents: in - out,
		})
	}

	for k, v := range byCostNature {
		rep.ByCostNature[k] = v
	}
	for k, v := range byBudgetClass {
		rep.ByBudgetClass[k] = v
	}

	for _, c := range d.Categories {
		totals := byCategory
		target := &rep.Categories
		if c.Classification == store.DirIncome {
			totals, target = byIncomeCat, &rep.IncomeCategories
		}
		if v := totals[c.ID]; v != 0 {
			*target = append(*target,
				LabeledTotal{Key: c.ID, Label: c.Name, Color: c.Color, Icon: c.Icon, Cents: v})
		}
	}
	for _, t := range d.Tags {
		if v := byTag[t.ID]; v != 0 {
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
	month := ""
	if len(months) > 0 {
		month = months[len(months)-1]
	}
	return buildReport(d, periodBookingValues(d, months, true), month, member)
}

// FixedCosts lists the fixed-cost bookings of a period as monthly averages,
// largest first, because that is the list worth renegotiating. A limit of 0
// keeps all of them.
func FixedCosts(d Data, months []string, member int64, limit int) []LabeledTotal {
	out := make([]LabeledTotal, 0, len(d.Bookings))
	for _, value := range periodBookingValues(d, months, true) {
		b := value.Booking
		if b.Direction != store.DirExpense || b.CostNature != store.CostFix {
			continue
		}
		if cents, ok := value.scoped(member); ok && cents != 0 {
			out = append(out, LabeledTotal{Key: b.ID, Label: b.Name, Cents: cents})
		}
	}
	sortByCentsDesc(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// sortByCentsDesc puts the largest first. The tie-breaks are not cosmetic: the
// totals are collected out of a map, so two equal entries would otherwise land
// in a different order on every request.
func sortByCentsDesc(t []LabeledTotal) {
	sort.Slice(t, func(i, j int) bool {
		if t[i].Cents != t[j].Cents {
			return t[i].Cents > t[j].Cents
		}
		if t[i].Label != t[j].Label {
			return t[i].Label < t[j].Label
		}
		return t[i].Key < t[j].Key
	})
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
