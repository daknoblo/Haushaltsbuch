package web

import (
	"context"
	"net/url"
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
// relative to total.
func BarWidth(part, total int64) string {
	if total <= 0 {
		return "0%"
	}
	p := float64(part) / float64(total) * 100
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

// ColorOr returns c when it is a valid hex color and a neutral fallback
// otherwise. Values are validated on write, so this only guards against rows
// edited outside the application.
func ColorOr(c string) string {
	if hexColorRe.MatchString(c) {
		return c
	}
	return "#94a3b8"
}

// Nav holds the data shared by the page chrome (header, navigation, month bar).
type Nav struct {
	Active          string // overview|bookings|dashboard|settings
	Path            string // base path of the current page, e.g. "/bookings"
	Households      []store.Household
	ActiveHousehold store.Household
	Month           string
	ShowMonthNav    bool
	Version         string
}

// IsActive reports whether the given nav item is the active page.
func (n Nav) IsActive(name string) bool { return n.Active == name }

// balanceTone returns the text color classes for a balance figure.
func balanceTone(cents int64) string {
	if cents < 0 {
		return "text-rose-600 dark:text-rose-400"
	}
	return "text-slate-900 dark:text-slate-100"
}

// memberChipClass returns the classes of a member chip in the split editor.
func memberChipClass(selected bool) string {
	base := "inline-flex cursor-pointer select-none items-center gap-2 rounded-full border px-3 py-1.5 text-sm transition "
	if selected {
		return base + "border-indigo-400 bg-indigo-500/10 text-indigo-700 dark:border-indigo-500/60 dark:text-indigo-200"
	}
	return base + "border-slate-300 text-slate-600 hover:border-slate-400 dark:border-slate-700 dark:text-slate-400 dark:hover:border-slate-500"
}

// PrevMonth returns the month before the current one.
func (n Nav) PrevMonth() string { return ShiftMonth(n.Month, -1) }

// NextMonth returns the month after the current one.
func (n Nav) NextMonth() string { return ShiftMonth(n.Month, 1) }

// CurrentMonthLabel returns the human-readable label for the active month.
func (n Nav) CurrentMonthLabel(ctx context.Context) string { return MonthLabel(ctx, n.Month) }

// MonthURL returns the URL for the current page with the given month selected.
func (n Nav) MonthURL(m string) string {
	p := n.Path
	if p == "" {
		p = "/"
	}
	return p + "?m=" + m
}

// AssetURL returns the URL of a static asset with the build version appended.
// Assets are served as immutable, so the version is what makes a new binary
// invalidate the cached copies.
func (n Nav) AssetURL(name string) string {
	v := n.Version
	if v == "" {
		v = "dev"
	}
	return "/static/" + name + "?v=" + url.QueryEscape(v)
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

// BookingRow couples a booking with its splits and tags for display and
// editing.
type BookingRow struct {
	Booking store.Booking
	Splits  []store.BookingSplit
	TagIDs  []int64
	// Expanded keeps the inline editor open across an auto-save round trip.
	Expanded bool
}

// ExpandedValue renders the open state for the hidden form field.
func (r BookingRow) ExpandedValue() string {
	if r.Expanded {
		return "1"
	}
	return "0"
}

// IsIncome reports whether the booking adds to the budget.
func (r BookingRow) IsIncome() bool { return r.Booking.Direction == store.DirIncome }

// BudgetClassBadge returns the badge modifier class for the budget class.
func (r BookingRow) BudgetClassBadge() string {
	switch r.Booking.BudgetClass {
	case store.ClassWant:
		return "badge-want"
	case store.ClassSaving:
		return "badge-saving"
	default:
		return "badge-need"
	}
}

// HasMember reports whether a member participates in the split.
func (r BookingRow) HasMember(id int64) bool {
	for _, s := range r.Splits {
		if s.MemberID == id {
			return true
		}
	}
	return false
}

// HasTag reports whether the booking carries the given tag.
func (r BookingRow) HasTag(id int64) bool {
	for _, t := range r.TagIDs {
		if t == id {
			return true
		}
	}
	return false
}

// SplitValue returns the stored split value for a member (0 if not present).
func (r BookingRow) SplitValue(id int64) float64 {
	for _, s := range r.Splits {
		if s.MemberID == id {
			return s.Value
		}
	}
	return 0
}

// MonthlyCents returns the monthly-equivalent amount of the booking.
func (r BookingRow) MonthlyCents() int64 { return calc.MonthlyCents(r.Booking) }

// SignedMonthlyCents is the monthly amount, negative for an expense, so the
// summary line reads like a ledger.
func (r BookingRow) SignedMonthlyCents() int64 {
	if r.IsIncome() {
		return r.MonthlyCents()
	}
	return -r.MonthlyCents()
}

// IDStr returns the booking id as a string.
func (r BookingRow) IDStr() string { return strconv.FormatInt(r.Booking.ID, 10) }

// DOMID returns the DOM element id for the booking row.
func (r BookingRow) DOMID() string { return "bk-" + r.IDStr() }

// PostURL returns the update endpoint for the booking.
func (r BookingRow) PostURL() string { return "/bookings/" + r.IDStr() }

// DeleteURL returns the delete endpoint for the booking.
func (r BookingRow) DeleteURL() string { return "/bookings/" + r.IDStr() + "/delete" }

// PercentInput returns the percent split value for a member as an input string.
func (r BookingRow) PercentInput(id int64) string {
	if !r.HasMember(id) {
		return ""
	}
	return strconv.FormatFloat(r.SplitValue(id), 'f', -1, 64)
}

// FixedInput returns the fixed split value (cents) for a member as a Euro input
// string.
func (r BookingRow) FixedInput(id int64) string {
	if !r.HasMember(id) {
		return ""
	}
	return formatDecimal(int64(r.SplitValue(id)))
}

// IntervalInput returns the recurrence interval as an input string.
func (r BookingRow) IntervalInput() string {
	if r.Booking.Interval < 1 {
		return "1"
	}
	return strconv.Itoa(r.Booking.Interval)
}

// StartMonth returns the start of the active range as YYYY-MM.
func (r BookingRow) StartMonth() string {
	if len(r.Booking.StartsOn) >= 7 {
		return r.Booking.StartsOn[:7]
	}
	return ""
}

// EndMonth returns the end of the active range as YYYY-MM.
func (r BookingRow) EndMonth() string {
	if len(r.Booking.EndsOn) >= 7 {
		return r.Booking.EndsOn[:7]
	}
	return ""
}

// SectionGroup groups booking rows under a section (nil = no section).
type SectionGroup struct {
	Section    *store.Section
	Bookings   []BookingRow
	TotalCents int64
}

// Title returns the section name or a placeholder for the ungrouped rows.
func (g SectionGroup) Title(ctx context.Context) string {
	if g.Section == nil {
		return T(ctx, "bookings.noSection")
	}
	return g.Section.Name
}

// SectionID returns the section id or 0 for the ungrouped rows.
func (g SectionGroup) SectionID() int64 {
	if g.Section == nil {
		return 0
	}
	return g.Section.ID
}

// BookingsVM is the view model of the bookings page, the single place where
// every planned figure is maintained.
type BookingsVM struct {
	Groups     []SectionGroup
	Income     []BookingRow
	Members    []store.Member
	Sections   []store.Section
	Categories []store.Category
	Tags       []store.Tag
	Report     calc.MonthReport
}

// ExpenseCategories returns only the categories bookable as an expense.
func (v BookingsVM) ExpenseCategories() []store.Category {
	return filterCategories(v.Categories, store.DirExpense)
}

// IncomeCategories returns only the categories bookable as income.
func (v BookingsVM) IncomeCategories() []store.Category {
	return filterCategories(v.Categories, store.DirIncome)
}

func filterCategories(in []store.Category, d store.Direction) []store.Category {
	out := make([]store.Category, 0, len(in))
	for _, c := range in {
		if c.Classification == d {
			out = append(out, c)
		}
	}
	return out
}

// categoriesFor returns the categories a row may pick from, which depends on
// whether the booking brings money in or takes it out.
func categoriesFor(vm BookingsVM, income bool) []store.Category {
	if income {
		return vm.IncomeCategories()
	}
	return vm.ExpenseCategories()
}

// tagStyle tints a tag badge with the tag's own color.
func tagStyle(color string) string {
	c := ColorOr(color)
	return "border-color:" + c + ";color:" + c
}

// SharePercent formats part as a percentage of total.
func SharePercent(part, total int64) string {
	if total <= 0 {
		return "–"
	}
	return FormatPercent(float64(part) / float64(total) * 100)
}

// TargetLabel renders the 50/30/20 target next to the actual share.
func TargetLabel(ctx context.Context, target int) string {
	return Tf(ctx, "dash.target", target)
}

// SankeyViewBox returns the SVG viewBox of a laid-out diagram.
func SankeyViewBox(s calc.Sankey) string {
	return "0 0 " + Coord(s.Width) + " " + Coord(s.Height)
}

// Coord formats a layout coordinate for an SVG attribute.
func Coord(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// StatMonth is one data point in the trend timeline.
type StatMonth struct {
	Month        string
	IncomeCents  int64
	ExpenseCents int64
	FixedCents   int64
	BalanceCents int64
}

// Label returns the short month label.
func (s StatMonth) Label(ctx context.Context) string { return MonthShort(ctx, s.Month) }

// RangeOption is one entry of the period selector.
type RangeOption struct {
	Key    string
	Label  string
	Active bool
}

// DashboardVM is the view model of the dashboard page: the headline figures,
// the breakdowns, the trend and the flow diagram for the selected period.
// Report describes a typical month of that period, so every card answers for
// the whole range rather than only its last month.
type DashboardVM struct {
	Report     calc.MonthReport
	Months     []StatMonth
	MaxCents   int64
	Sankey     calc.Sankey
	Ranges     []RangeOption
	RangeKey   string
	RangeLabel string
	FixedTop   []calc.LabeledTotal
}

// SettingsVM is the view model of the settings page.
type SettingsVM struct {
	Households []store.Household
	ActiveID   int64
	Members    []store.Member
	Sections   []store.Section
	Categories []store.Category
	Tags       []store.Tag
	CatUsage   map[int64]int
}

// UsageOf returns how many bookings still reference a category.
func (v SettingsVM) UsageOf(id int64) int { return v.CatUsage[id] }
