// Package calc computes monthly budget figures from stored data: it normalises
// recurring expenses to monthly equivalents, allocates expense shares to
// members according to each expense's split mode and aggregates the results.
package calc

import (
	"math"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// MemberBalance holds the income, allocated expense share and resulting balance
// for a single member in a month.
type MemberBalance struct {
	Member       store.Member
	IncomeCents  int64
	ExpenseCents int64
	BalanceCents int64
}

// LabeledTotal is a named monetary total used for breakdowns.
type LabeledTotal struct {
	Label string
	Cents int64
}

// MonthReport is the aggregated result for one month of one household.
type MonthReport struct {
	Month           string
	IncomeCents     int64
	ExpenseCents    int64
	UnassignedCents int64
	BalanceCents    int64
	Members         []MemberBalance
	Sections        []LabeledTotal
	Categories      []LabeledTotal
	ByCostNature    map[store.CostNature]int64
	ByBudgetClass   map[store.BudgetClass]int64
}

// ExpenseActiveIn reports whether an expense contributes to the given month.
func ExpenseActiveIn(e store.Expense, month string) bool {
	if e.IsOneOff {
		return len(e.OccurredOn) >= 7 && e.OccurredOn[:7] == month
	}
	if e.ActiveFrom != "" && month < e.ActiveFrom {
		return false
	}
	if e.ActiveUntil != "" && month > e.ActiveUntil {
		return false
	}
	return true
}

// monthlyCents returns the monthly-equivalent amount (in cents, as float) of an
// expense. One-off expenses contribute their full amount in their month.
func monthlyCents(e store.Expense) float64 {
	if e.IsOneOff {
		return float64(e.AmountCents)
	}
	return float64(e.AmountCents) * e.Frequency.MonthlyFactor()
}

// MonthlyCents returns the rounded monthly-equivalent amount (in cents) of an
// expense.
func MonthlyCents(e store.Expense) int64 {
	return round(monthlyCents(e))
}

// allocate distributes the monthly amount of an expense among members according
// to its split mode. It returns the per-member allocation (member id -> cents)
// and the unassigned remainder.
func allocate(amount float64, e store.Expense, splits []store.ExpenseSplit, members []store.Member) (map[int64]float64, float64) {
	res := make(map[int64]float64)

	switch e.SplitMode {
	case store.SplitPercent:
		for _, s := range splits {
			res[s.MemberID] += amount * s.Value / 100.0
		}
	case store.SplitFixed:
		factor := 1.0
		if !e.IsOneOff {
			factor = e.Frequency.MonthlyFactor()
		}
		for _, s := range splits {
			res[s.MemberID] += s.Value * factor
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

// BuildMonthReport aggregates all figures for a household in a given month.
func BuildMonthReport(
	month string,
	members []store.Member,
	sections []store.Section,
	categories []store.Category,
	expenses []store.Expense,
	splitsByExpense map[int64][]store.ExpenseSplit,
	incomes []store.Income,
) MonthReport {
	rep := MonthReport{
		Month:         month,
		ByCostNature:  make(map[store.CostNature]int64),
		ByBudgetClass: make(map[store.BudgetClass]int64),
	}

	memIncome := make(map[int64]float64)
	for _, in := range incomes {
		if in.YearMonth == month {
			memIncome[in.MemberID] += float64(in.AmountCents)
		}
	}

	memExpense := make(map[int64]float64)
	sectionTotals := make(map[int64]float64)
	categoryTotals := make(map[int64]float64)
	cnTmp := make(map[store.CostNature]float64)
	bcTmp := make(map[store.BudgetClass]float64)

	var totalExpense, totalUnassigned float64
	for _, e := range expenses {
		if !ExpenseActiveIn(e, month) {
			continue
		}
		amt := monthlyCents(e)
		totalExpense += amt

		alloc, un := allocate(amt, e, splitsByExpense[e.ID], members)
		totalUnassigned += un
		for id, v := range alloc {
			memExpense[id] += v
		}

		var sid int64
		if e.SectionID != nil {
			sid = *e.SectionID
		}
		sectionTotals[sid] += amt

		var cid int64
		if e.CategoryID != nil {
			cid = *e.CategoryID
		}
		categoryTotals[cid] += amt

		cnTmp[e.CostNature] += amt
		bcTmp[e.BudgetClass] += amt
	}

	var totalIncome float64
	for _, m := range members {
		inc := memIncome[m.ID]
		exp := memExpense[m.ID]
		rep.Members = append(rep.Members, MemberBalance{
			Member:       m,
			IncomeCents:  round(inc),
			ExpenseCents: round(exp),
			BalanceCents: round(inc - exp),
		})
		totalIncome += inc
	}

	// Sections (ordered by the provided section list, then "Ohne Sektion").
	for _, sec := range sections {
		if v, ok := sectionTotals[sec.ID]; ok && v != 0 {
			rep.Sections = append(rep.Sections, LabeledTotal{Label: sec.Name, Cents: round(v)})
		}
	}
	if v, ok := sectionTotals[0]; ok && v != 0 {
		rep.Sections = append(rep.Sections, LabeledTotal{Label: "Ohne Sektion", Cents: round(v)})
	}

	// Categories.
	catName := make(map[int64]string, len(categories))
	for _, c := range categories {
		catName[c.ID] = c.Name
	}
	for _, c := range categories {
		if v, ok := categoryTotals[c.ID]; ok && v != 0 {
			rep.Categories = append(rep.Categories, LabeledTotal{Label: c.Name, Cents: round(v)})
		}
	}
	if v, ok := categoryTotals[0]; ok && v != 0 {
		rep.Categories = append(rep.Categories, LabeledTotal{Label: "Ohne Kategorie", Cents: round(v)})
	}

	for k, v := range cnTmp {
		rep.ByCostNature[k] = round(v)
	}
	for k, v := range bcTmp {
		rep.ByBudgetClass[k] = round(v)
	}

	rep.IncomeCents = round(totalIncome)
	rep.ExpenseCents = round(totalExpense)
	rep.UnassignedCents = round(totalUnassigned)
	rep.BalanceCents = round(totalIncome - totalExpense)
	return rep
}

func round(f float64) int64 {
	return int64(math.Round(f))
}
