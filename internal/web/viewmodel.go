package web

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// jsonString quotes a value for an hx-vals attribute.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

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

// CentsToInput formats cents as a plain decimal string for a number input.
// Zero renders empty and a trailing ",00" is dropped, so a fresh field invites
// typing instead of having to clear a placeholder figure first.
func CentsToInput(c int64) string {
	if c == 0 {
		return ""
	}
	return strings.TrimSuffix(formatDecimal(c), ".00")
}

// OverviewVM is the view model of the overview page.
type OverviewVM struct {
	Report calc.MonthReport
}

// BookingRow couples a booking with everything needed to show and edit it.
type BookingRow struct {
	Booking  store.Booking
	Category store.Category
	Payer    store.Member
	// Carriers are the members who carry the booking, in household order.
	Carriers  []store.Member
	Splits    []store.BookingSplit
	TagIDs    []int64
	Overrides []store.BookingOverride
	// Month is the month the displayed amount is computed for, because a
	// temporary override makes that amount depend on when you look.
	Month string
	// MemberCount is how many people the household has, which is what decides
	// whether "carried alone" says anything at all.
	MemberCount int
}

// IsIncome reports whether the booking adds to the budget.
func (r BookingRow) IsIncome() bool { return r.Booking.Direction == store.DirIncome }

// HasMember reports whether a member participates in the split.
func (r BookingRow) HasMember(id int64) bool {
	for _, s := range r.Splits {
		if s.MemberID == id {
			return true
		}
	}
	return false
}

// IsPayer reports whether the member fronts this booking.
func (r BookingRow) IsPayer(id int64) bool {
	return r.Booking.PayerMemberID != nil && *r.Booking.PayerMemberID == id
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

// ShareCount is how many members carry the booking, which is the "divided by"
// the summary line shows.
func (r BookingRow) ShareCount() int { return len(r.Splits) }

// PayerCarriesNothing reports whether the one who fronts the money has no
// share in it, so the row has to name them on their own.
func (r BookingRow) PayerCarriesNothing() bool {
	if r.Payer.ID == 0 {
		return false
	}
	for _, m := range r.Carriers {
		if m.ID == r.Payer.ID {
			return false
		}
	}
	return true
}

// FrequencyBadgeClass tints a rhythm badge, so a monthly booking is told from
// a yearly one before the label is read.
func FrequencyBadgeClass(f store.Frequency) string {
	switch f {
	case store.FreqMonthly:
		return "badge-info"
	case store.FreqYearly:
		return "badge-warn"
	case store.FreqQuarterly:
		return "badge-sky"
	case store.FreqWeekly:
		return "badge-violet"
	default:
		return "badge-muted"
	}
}

// MonthlyCents returns the monthly-equivalent amount, overrides applied.
func (r BookingRow) MonthlyCents() int64 {
	return calc.MonthlyCents(r.Booking, r.Overrides, r.Month)
}

// ActiveInMonth reports whether the booking counts in the displayed month. The
// list holds every booking so it stays editable, so a one-off from another
// month has to say that it contributes nothing here.
func (r BookingRow) ActiveInMonth() bool {
	return calc.ActiveIn(r.Booking, r.Month)
}

// AmountCents returns the amount charged in the displayed month, which differs
// from the stored one while an override is in force.
func (r BookingRow) AmountCents() int64 {
	return calc.AmountFor(r.Booking, r.Overrides, r.Month)
}

// Discounted reports whether an override currently replaces the base amount.
func (r BookingRow) Discounted() bool {
	return r.AmountCents() != r.Booking.AmountCents
}

// IDStr returns the booking id as a string.
func (r BookingRow) IDStr() string { return strconv.FormatInt(r.Booking.ID, 10) }

// DOMID returns the DOM element id for the booking row.
func (r BookingRow) DOMID() string { return "bk-" + r.IDStr() }

// PostURL returns the update endpoint for the booking.
func (r BookingRow) PostURL() string { return "/bookings/" + r.IDStr() }

// EditURL returns the endpoint that renders the booking dialog.
func (r BookingRow) EditURL() string { return "/bookings/" + r.IDStr() + "/edit" }

// DeleteURL returns the delete endpoint for the booking.
func (r BookingRow) DeleteURL() string { return "/bookings/" + r.IDStr() + "/delete" }

// DiscardURL returns the endpoint that drops the booking if it was never
// edited.
func (r BookingRow) DiscardURL() string { return "/bookings/" + r.IDStr() + "/discard" }

// PercentInput returns the percent split value for a member as an input string.
// It is the stored value only while percent is the active mode: the value
// column means something different in every mode, so any other mode falls back
// to an even share. That is what the mode says anyway, and it means switching
// modes lands on a sane figure instead of a reinterpreted one.
func (r BookingRow) PercentInput(id int64) string {
	if r.Booking.SplitMode == store.SplitPercent && r.HasMember(id) {
		return strconv.FormatFloat(r.SplitValue(id), 'f', -1, 64)
	}
	return strconv.FormatFloat(r.evenPercent(id), 'f', -1, 64)
}

// FixedInput returns the fixed split value (cents) for a member as a Euro input
// string, falling back to an even share for the same reason as PercentInput.
func (r BookingRow) FixedInput(id int64) string {
	if r.Booking.SplitMode == store.SplitFixed && r.HasMember(id) {
		return CentsToInput(int64(r.SplitValue(id)))
	}
	return CentsToInput(r.evenCents(id))
}

// carrierIndex is a member's position among those carrying the booking, -1 for
// anyone who carries none of it.
func (r BookingRow) carrierIndex(id int64) int {
	for i, s := range r.Splits {
		if s.MemberID == id {
			return i
		}
	}
	return -1
}

// evenPercent is one member's share of an even split. The remainder goes to the
// first carrier so the shares still add up to a hundred.
func (r BookingRow) evenPercent(id int64) float64 {
	n := len(r.Splits)
	i := r.carrierIndex(id)
	if n == 0 || i < 0 {
		return 0
	}
	base := math.Trunc(100 / float64(n))
	if i == 0 {
		return 100 - base*float64(n-1)
	}
	return base
}

// evenCents is one member's share of the monthly amount, split evenly.
func (r BookingRow) evenCents(id int64) int64 {
	n := int64(len(r.Splits))
	i := r.carrierIndex(id)
	if n == 0 || i < 0 {
		return 0
	}
	base := r.Booking.AmountCents / n
	if i == 0 {
		return r.Booking.AmountCents - base*(n-1)
	}
	return base
}

// PercentAllocated is how much of the booking the percent shares hand out. It
// is the figure the dialog warns about when it is not a hundred.
func (r BookingRow) PercentAllocated() string {
	var sum float64
	for _, s := range r.Splits {
		if r.Booking.SplitMode == store.SplitPercent {
			sum += s.Value
			continue
		}
		sum += r.evenPercent(s.MemberID)
	}
	return strconv.FormatFloat(sum, 'f', -1, 64)
}

// NameIsSuggested reports whether the name is still the one a fresh booking was
// created with, which is what the dialog clears on first focus.
func (r BookingRow) NameIsSuggested(ctx context.Context) bool {
	return nameIsSuggested(ctx, r.Booking.Name)
}

func nameIsSuggested(ctx context.Context, name string) bool {
	return name == T(ctx, "bookings.newExpense") || name == T(ctx, "bookings.newIncome")
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

// OccurredInput is the date a one-off booking falls on. A recurring booking
// carries the start of its range, so switching the rhythm off leaves a usable
// date behind instead of a booking that shows up in no month at all.
func (r BookingRow) OccurredInput() string {
	if len(r.Booking.StartsOn) == 10 {
		return r.Booking.StartsOn
	}
	if ValidMonth(r.Month) {
		return r.Month + "-01"
	}
	return ""
}

// CategoryGroup collects the bookings of one category. The page lists every
// booking on its own, so this only serves the printed list.
type CategoryGroup struct {
	Category   store.Category
	Bookings   []BookingRow
	TotalCents int64
}

// BookingsVM is the view model of the bookings page, the single place where
// every planned figure is maintained. Bookings are one flat list: the colored
// marker on each row says what it is, so no grouping has to.
type BookingsVM struct {
	Month    string
	Sort     string
	Bookings []BookingRow
	Report   calc.MonthReport
	Form     BookingFormVM
}

// Empty reports whether the household has nothing recorded yet.
func (v BookingsVM) Empty() bool { return len(v.Bookings) == 0 }

// Sort keys of the bookings list.
const (
	SortDirection = "dir"
	SortAmount    = "amount"
	SortName      = "name"
	SortCategory  = "category"
	SortPayer     = "payer"
	SortFrequency = "frequency"
	SortNature    = "nature"
	SortClass     = "class"
	SortCarriers  = "carriers"
	SortUpdated   = "updated"
)

// sortOrder keeps the selector in a fixed order, which a map cannot.
var sortOrder = []struct{ key, label string }{
	{SortDirection, "bookings.sortDefault"},
	{SortAmount, "bookings.sortAmount"},
	{SortName, "bookings.sortName"},
	{SortCategory, "bookings.sortCategory"},
	{SortPayer, "bookings.sortPayer"},
	{SortCarriers, "bookings.sortCarriers"},
	{SortFrequency, "bookings.sortFrequency"},
	{SortNature, "bookings.sortNature"},
	{SortClass, "bookings.sortClass"},
	{SortUpdated, "bookings.sortUpdated"},
}

// cleanSort falls back to the default order for anything unknown.
func cleanSort(key string) string {
	for _, o := range sortOrder {
		if o.key == key {
			return key
		}
	}
	return SortDirection
}

// SortOptions returns the selectable orders with the active one marked.
func (v BookingsVM) SortOptions(ctx context.Context) []PeriodOption {
	out := make([]PeriodOption, 0, len(sortOrder))
	for _, o := range sortOrder {
		out = append(out, PeriodOption{
			Key:    o.key,
			Label:  T(ctx, o.label),
			Active: o.key == cleanSort(v.Sort),
			URL:    "/bookings?m=" + v.Month + "&s=" + o.key,
		})
	}
	return out
}

// ListURL is what the list re-fetches itself from, sort included so an
// auto-save does not throw the chosen order away.
func (v BookingsVM) ListURL() string {
	return "/bookings/list?m=" + v.Month + "&s=" + cleanSort(v.Sort)
}

// BookingFormVM carries the pickers the booking dialog needs.
type BookingFormVM struct {
	Row        BookingRow
	Members    []store.Member
	Categories []store.Category
	Tags       []store.Tag
	// Draft marks a booking that was just created, so closing the dialog
	// without a single edit can throw it away again.
	Draft bool
}

// PickableCategories returns the categories matching the booking's direction,
// because an income cannot be filed under an expense category.
func (f BookingFormVM) PickableCategories() []store.Category {
	want := store.DirExpense
	if f.Row.IsIncome() {
		want = store.DirIncome
	}
	out := make([]store.Category, 0, len(f.Categories))
	for _, c := range f.Categories {
		if c.Classification == want {
			out = append(out, c)
		}
	}
	return out
}

// Title names the dialog after what it edits.
func (f BookingFormVM) Title(ctx context.Context) string {
	if f.Row.IsIncome() {
		return T(ctx, "bookings.editIncome")
	}
	return T(ctx, "bookings.editExpense")
}

// DiscardURL is set only on a fresh draft: an existing booking is never thrown
// away just because its dialog was closed.
func (f BookingFormVM) DiscardURL() string {
	if !f.Draft {
		return ""
	}
	return f.Row.DiscardURL()
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

// SankeyValue is the figure a node carries next to its name, so reading the
// diagram does not depend on hovering every box.
func SankeyValue(s calc.Sankey, n calc.SankeyNode) string {
	out := FormatEURShort(n.Cents)
	if s.TotalCents > 0 {
		out += " · " + FormatPercent(s.Share(n.Cents))
	}
	return out
}

// ChartViewBox returns the SVG viewBox of the trend chart.
func ChartViewBox(c calc.TrendChart) string {
	return "0 0 " + Coord(c.Width) + " " + Coord(c.Height)
}

// Coord formats a layout coordinate for an SVG attribute.
func Coord(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// PeriodOption is one entry of the period selector.
type PeriodOption struct {
	Key    string
	Label  string
	Active bool
	URL    string
}

// ViewOption is one entry of the household/person switch.
type ViewOption struct {
	Member int64
	Label  string
	Color  string
	Active bool
	URL    string
}

// DashboardVM is the view model of the dashboard page. Report describes a
// typical month of the selected period, so every card answers for the whole
// range rather than only its last month.
type DashboardVM struct {
	Report calc.MonthReport
	// HouseholdReport is always the whole household, so a person view can put
	// its own share next to what the household spends in total.
	HouseholdReport calc.MonthReport
	Trend           []calc.MonthReport
	Chart           calc.TrendChart
	Sankey          calc.Sankey
	FixedTop        []calc.LabeledTotal
	Periods         []PeriodOption
	PeriodKey       string
	PeriodLabel     string
	RangeLabel      string
	PrevURL         string
	NextURL         string
	Views           []ViewOption
	ViewMember      int64
	Settlement      calc.SettlementReport
}

// Positions is every member's paid/owed position of the period.
func (v DashboardVM) Positions() []calc.MemberPosition { return v.Settlement.Positions }

// Transfers are the payments that square the period.
func (v DashboardVM) Transfers() []calc.Transfer { return v.Settlement.Transfers }

// ShowSettlement hides the settlement while there is nobody to settle with.
func (v DashboardVM) ShowSettlement() bool { return len(v.Settlement.Positions) > 1 }

// SettlementEven reports whether nothing has to change hands.
func (v DashboardVM) SettlementEven() bool { return len(v.Settlement.Transfers) == 0 }

// ShareLines are the expenses the selected view carries: all of them for the
// household, only the ones the member has a share in for a person.
func (v DashboardVM) ShareLines() []calc.ShareLine {
	return v.Settlement.LinesFor(v.ViewMember)
}

// Carried splits what the selected view shoulders into the divided part and
// the part it carries alone; the two add up to the expenses shown above.
func (v DashboardVM) Carried() calc.Carried { return v.Settlement.CarriedBy(v.ViewMember) }

// Ledger lists what one member fronted and carries, booking by booking.
func (v DashboardVM) Ledger(member int64) []calc.LedgerLine {
	return v.Settlement.Ledger(member)
}

// HouseholdView reports whether the dashboard shows the whole household.
func (v DashboardVM) HouseholdView() bool { return v.ViewMember == calc.Everyone }

// SplitLabel names how a booking is divided, which is what tells a shared bill
// apart from one a single member carries alone.
func SplitLabel(ctx context.Context, l calc.ShareLine) string {
	if l.Shared() {
		return "÷ " + strconv.Itoa(l.Carriers)
	}
	return T(ctx, "dash.splitAlone")
}

// SettingsVM is the view model of the settings page.
type SettingsVM struct {
	Households  []store.Household
	ActiveID    int64
	Members     []store.Member
	Categories  []store.Category
	Tags        []store.Tag
	CatUsage    map[int64]int
	Suggestions []store.SeedCategory
	Icons       []string
}

// UsageOf returns how many bookings still reference a category.
func (v SettingsVM) UsageOf(id int64) int { return v.CatUsage[id] }
