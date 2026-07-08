package web

import (
	"strconv"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Option is a value/label pair for select inputs.
type Option struct {
	Value string
	Label string
}

// BarWidth returns a CSS width percentage string for a bar of size part
// relative to max.
func BarWidth(part, max int64) string {
	if max <= 0 {
		return "0%"
	}
	p := float64(part) / float64(max) * 100
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return strconv.FormatFloat(p, 'f', 2, 64) + "%"
}

// MaxLabeled returns the largest Cents value in a slice of labeled totals.
func MaxLabeled(items []calc.LabeledTotal) int64 {
	var m int64
	for _, it := range items {
		if it.Cents > m {
			m = it.Cents
		}
	}
	return m
}

// ColorOr returns c or a neutral fallback colour when c is empty.
func ColorOr(c string) string {
	if c == "" {
		return "#94a3b8"
	}
	return c
}

// Nav holds the data shared by the page chrome (header, navigation, month bar).
type Nav struct {
	Active          string // overview|expenses|income|statistics|settings
	Path            string // base path of the current page, e.g. "/income"
	Households      []store.Household
	ActiveHousehold store.Household
	Month           string
	ShowMonthNav    bool
	Version         string
}

// IsActive reports whether the given nav item is the active page.
func (n Nav) IsActive(name string) bool { return n.Active == name }

// PrevMonth returns the month before the current one.
func (n Nav) PrevMonth() string { return ShiftMonth(n.Month, -1) }

// NextMonth returns the month after the current one.
func (n Nav) NextMonth() string { return ShiftMonth(n.Month, 1) }

// CurrentMonthLabel returns the human-readable label for the active month.
func (n Nav) CurrentMonthLabel() string { return MonthLabel(n.Month) }

// MonthURL returns the URL for the current page with the given month selected.
func (n Nav) MonthURL(m string) string {
	p := n.Path
	if p == "" {
		p = "/"
	}
	return p + "?m=" + m
}

// FrequencyLabel returns the German label for a frequency.
func FrequencyLabel(f store.Frequency) string {
	switch f {
	case store.FreqWeekly:
		return "Wöchentlich"
	case store.FreqYearly:
		return "Jährlich"
	default:
		return "Monatlich"
	}
}

// CostNatureLabel returns the German label for a cost nature.
func CostNatureLabel(c store.CostNature) string {
	if c == store.CostVariable {
		return "Variabel"
	}
	return "Fix"
}

// BudgetClassLabel returns the German label for a budget class.
func BudgetClassLabel(b store.BudgetClass) string {
	switch b {
	case store.ClassWant:
		return "Wunsch"
	case store.ClassSaving:
		return "Sparen"
	default:
		return "Bedarf"
	}
}

// SplitModeLabel returns the German label for a split mode.
func SplitModeLabel(m store.SplitMode) string {
	switch m {
	case store.SplitPercent:
		return "Prozentual"
	case store.SplitFixed:
		return "Feste Beträge"
	default:
		return "Gleichmäßig"
	}
}

// FrequencyOptions returns the selectable frequencies.
func FrequencyOptions() []Option {
	return []Option{
		{string(store.FreqMonthly), "Monatlich"},
		{string(store.FreqWeekly), "Wöchentlich"},
		{string(store.FreqYearly), "Jährlich"},
	}
}

// CostNatureOptions returns the selectable cost natures.
func CostNatureOptions() []Option {
	return []Option{
		{string(store.CostFix), "Fix"},
		{string(store.CostVariable), "Variabel"},
	}
}

// BudgetClassOptions returns the selectable budget classes.
func BudgetClassOptions() []Option {
	return []Option{
		{string(store.ClassNeed), "Bedarf"},
		{string(store.ClassWant), "Wunsch"},
		{string(store.ClassSaving), "Sparen"},
	}
}

// SplitModeOptions returns the selectable split modes.
func SplitModeOptions() []Option {
	return []Option{
		{string(store.SplitEqual), "Gleichmäßig"},
		{string(store.SplitPercent), "Prozentual"},
		{string(store.SplitFixed), "Feste Beträge"},
	}
}

// CentsToInput formats cents as a plain decimal string for a number input
// (e.g. 123456 -> "1234.56").
func CentsToInput(c int64) string {
	return formatDecimal(c)
}

// OverviewVM is the view model of the overview page.
type OverviewVM struct {
	Report calc.MonthReport
}

// ExpenseRow couples an expense with its splits for display and editing.
type ExpenseRow struct {
	Expense store.Expense
	Splits  []store.ExpenseSplit
}

// HasMember reports whether a member participates in the split.
func (r ExpenseRow) HasMember(id int64) bool {
	for _, s := range r.Splits {
		if s.MemberID == id {
			return true
		}
	}
	return false
}

// SplitValue returns the stored split value for a member (0 if not present).
func (r ExpenseRow) SplitValue(id int64) float64 {
	for _, s := range r.Splits {
		if s.MemberID == id {
			return s.Value
		}
	}
	return 0
}

// MonthlyCents returns the monthly-equivalent amount of the expense.
func (r ExpenseRow) MonthlyCents() int64 {
	return calc.MonthlyCents(r.Expense)
}

// IDStr returns the expense id as a string.
func (r ExpenseRow) IDStr() string { return strconv.FormatInt(r.Expense.ID, 10) }

// DOMID returns the DOM element id for the expense row.
func (r ExpenseRow) DOMID() string { return "exp-" + r.IDStr() }

// PostURL returns the update endpoint for the expense.
func (r ExpenseRow) PostURL() string { return "/expenses/" + r.IDStr() }

// DeleteURL returns the delete endpoint for the expense.
func (r ExpenseRow) DeleteURL() string { return "/expenses/" + r.IDStr() + "/delete" }

// PercentInput returns the percent split value for a member as an input string.
func (r ExpenseRow) PercentInput(id int64) string {
	if !r.HasMember(id) {
		return ""
	}
	return strconv.FormatFloat(r.SplitValue(id), 'f', -1, 64)
}

// FixedInput returns the fixed split value (cents) for a member as a Euro input
// string.
func (r ExpenseRow) FixedInput(id int64) string {
	if !r.HasMember(id) {
		return ""
	}
	return formatDecimal(int64(r.SplitValue(id)))
}

// SectionGroup groups expense rows under a section (nil = no section).
type SectionGroup struct {
	Section    *store.Section
	Expenses   []ExpenseRow
	TotalCents int64
}

// Title returns the section name or a placeholder for the unsectioned group.
func (g SectionGroup) Title() string {
	if g.Section == nil {
		return "Ohne Sektion"
	}
	return g.Section.Name
}

// SectionID returns the section id or 0 for the unsectioned group.
func (g SectionGroup) SectionID() int64 {
	if g.Section == nil {
		return 0
	}
	return g.Section.ID
}

// ExpensesVM is the view model of the expenses page.
type ExpensesVM struct {
	Groups     []SectionGroup
	Members    []store.Member
	Sections   []store.Section
	Categories []store.Category
}

// IncomeMemberVM holds a member's income lines for a month.
type IncomeMemberVM struct {
	Member     store.Member
	Lines      []store.Income
	TotalCents int64
}

// IncomeVM is the view model of the income page.
type IncomeVM struct {
	Members    []IncomeMemberVM
	TotalCents int64
	PrevMonth  string
	HasPrev    bool
}

// StatMonth is one data point in the statistics timeline.
type StatMonth struct {
	Month        string
	IncomeCents  int64
	ExpenseCents int64
	BalanceCents int64
}

// Label returns the short month label.
func (s StatMonth) Label() string { return MonthShort(s.Month) }

// StatisticsVM is the view model of the statistics page.
type StatisticsVM struct {
	Months     []StatMonth
	MaxCents   int64
	Current    calc.MonthReport
	AvgIncome  int64
	AvgExpense int64
}

// SettingsVM is the view model of the settings page.
type SettingsVM struct {
	Households []store.Household
	ActiveID   int64
	Members    []store.Member
	Sections   []store.Section
	Categories []store.Category
}
