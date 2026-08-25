// Package calc computes monthly budget figures from stored bookings: it
// normalises recurring bookings to monthly equivalents, allocates shares to
// members according to each booking's split mode and aggregates the results
// into the figures the overview, the dashboards and the Sankey diagram need.
package calc

import (
	"math"
	"sort"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// MaxSplitCents bounds a fixed split value, mirroring the limit the input layer
// enforces so a stored outlier cannot overflow an aggregate.
const MaxSplitCents = 1_000_000_000_000

// Data is one household's complete planning input. Passing it as a whole keeps
// the aggregation functions from growing an unreadable parameter list and makes
// it obvious that every report is built from a single consistent snapshot.
type Data struct {
	Members    []store.Member
	Sections   []store.Section
	Categories []store.Category
	Tags       []store.Tag
	Bookings   []store.Booking
	Splits     map[int64][]store.BookingSplit
	TagLinks   map[int64][]int64
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
	Cents int64
}

// MonthReport is the aggregated result for one month of one household.
type MonthReport struct {
	Month            string
	IncomeCents      int64
	ExpenseCents     int64
	FixedCents       int64
	VariableCents    int64
	UnassignedCents  int64
	BalanceCents     int64
	Members          []MemberBalance
	Sections         []LabeledTotal
	Categories       []LabeledTotal
	IncomeCategories []LabeledTotal
	Tags             []LabeledTotal
	ByCostNature     map[store.CostNature]int64
	ByBudgetClass    map[store.BudgetClass]int64
}

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
	return float64(r.FixedCents) / float64(r.IncomeCents) * 100
}

// ActiveIn reports whether a booking contributes to the given YYYY-MM month.
// A one-off booking counts only in the month it falls into; a recurring one
// counts in every month of its active range.
func ActiveIn(b store.Booking, month string) bool {
	if !b.Frequency.Recurring() {
		return len(b.StartsOn) >= 7 && b.StartsOn[:7] == month
	}
	if len(b.StartsOn) >= 7 && month < b.StartsOn[:7] {
		return false
	}
	if len(b.EndsOn) >= 7 && month > b.EndsOn[:7] {
		return false
	}
	return true
}

// monthlyFactor is how much of a booking's amount falls into a single month.
func monthlyFactor(b store.Booking) float64 {
	factor := b.Frequency.MonthlyFactor()
	if b.Frequency.Recurring() && b.Interval > 1 {
		factor /= float64(b.Interval)
	}
	return factor
}

// MonthlyCents returns the rounded monthly-equivalent amount of a booking.
func MonthlyCents(b store.Booking) int64 {
	return round(float64(b.AmountCents) * monthlyFactor(b))
}

// allocate distributes the monthly amount of a booking among members according
// to its split mode and returns the per-member allocation plus the remainder
// that could not be attributed to anyone.
func allocate(amount float64, b store.Booking, splits []store.BookingSplit, members []store.Member) (map[int64]float64, float64) {
	res := make(map[int64]float64)

	switch b.SplitMode {
	case store.SplitPercent:
		for _, s := range splits {
			res[s.MemberID] += amount * clampPercent(s.Value) / 100.0
		}
	case store.SplitFixed:
		factor := monthlyFactor(b)
		for _, s := range splits {
			res[s.MemberID] += clampAmount(s.Value) * factor
		}
	default: // equal
		ids := make([]int64, 0, len(splits))
		for _, s := range splits {
			ids = append(ids, s.MemberID)
		}
		if len(ids) == 0 {
			for _, m := range members {
				ids = append(ids, m.ID)
			}
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

// BuildMonthReport aggregates all figures of a household for one month.
func BuildMonthReport(d Data, month string) MonthReport {
	rep := MonthReport{
		Month:         month,
		ByCostNature:  make(map[store.CostNature]int64),
		ByBudgetClass: make(map[store.BudgetClass]int64),
	}

	var (
		income        float64
		expense       float64
		fixed         float64
		variable      float64
		unassigned    float64
		memIncome     = make(map[int64]float64)
		memExpense    = make(map[int64]float64)
		bySection     = make(map[int64]float64)
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
		amount := float64(b.AmountCents) * monthlyFactor(b)
		shares, rest := allocate(amount, b, d.Splits[b.ID], d.Members)

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
		if b.SectionID != nil {
			bySection[*b.SectionID] += amount
		}
		for _, tagID := range d.TagLinks[b.ID] {
			byTag[tagID] += amount
		}
		byCostNature[b.CostNature] += amount
		byBudgetClass[b.BudgetClass] += amount
		if b.CostNature == store.CostFix {
			fixed += amount
		} else {
			variable += amount
		}
	}

	rep.IncomeCents = round(income)
	rep.ExpenseCents = round(expense)
	rep.FixedCents = round(fixed)
	rep.VariableCents = round(variable)
	rep.UnassignedCents = round(unassigned)
	rep.BalanceCents = rep.IncomeCents - rep.ExpenseCents

	for _, m := range d.Members {
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

	for _, s := range d.Sections {
		if v := round(bySection[s.ID]); v != 0 {
			rep.Sections = append(rep.Sections, LabeledTotal{Key: s.ID, Label: s.Name, Cents: v})
		}
	}
	for _, c := range d.Categories {
		if c.Classification == store.DirIncome {
			if v := round(byIncomeCat[c.ID]); v != 0 {
				rep.IncomeCategories = append(rep.IncomeCategories,
					LabeledTotal{Key: c.ID, Label: c.Name, Color: c.Color, Cents: v})
			}
			continue
		}
		if v := round(byCategory[c.ID]); v != 0 {
			rep.Categories = append(rep.Categories,
				LabeledTotal{Key: c.ID, Label: c.Name, Color: c.Color, Cents: v})
		}
	}
	for _, t := range d.Tags {
		if v := round(byTag[t.ID]); v != 0 {
			rep.Tags = append(rep.Tags, LabeledTotal{Key: t.ID, Label: t.Name, Color: t.Color, Cents: v})
		}
	}

	sortByCentsDesc(rep.Sections)
	sortByCentsDesc(rep.Categories)
	sortByCentsDesc(rep.IncomeCategories)
	sortByCentsDesc(rep.Tags)
	return rep
}

// Trend builds one report per month, oldest first.
func Trend(d Data, months []string) []MonthReport {
	out := make([]MonthReport, 0, len(months))
	for _, m := range months {
		out = append(out, BuildMonthReport(d, m))
	}
	return out
}

func sortByCentsDesc(t []LabeledTotal) {
	sort.SliceStable(t, func(i, j int) bool { return t[i].Cents > t[j].Cents })
}

func round(f float64) int64 {
	return int64(math.Round(f))
}

func clampPercent(v float64) float64 {
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
