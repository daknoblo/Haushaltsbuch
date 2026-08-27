package web

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "overview", "/", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm OverviewVM
	if nav.ActiveHousehold.ID != 0 {
		rep, err := s.buildMonthReport(r.Context(), nav.ActiveHousehold.ID, nav.Month)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		vm.Report = rep
	}
	s.render(w, r, OverviewPage(nav, vm))
}

func (s *Server) handleBookings(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "bookings", "/bookings", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm BookingsVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildBookingsVM(r.Context(), nav.ActiveHousehold.ID, nav.Month,
			r.URL.Query().Get("s"))
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, BookingsPage(nav, vm))
}

// handleBookingList re-renders only the list, which is how the page catches up
// after a dialog changed something without reloading.
func (s *Server) handleBookingList(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	vm, err := s.buildBookingsVM(r.Context(), active,
		NormalizeMonth(r.URL.Query().Get("m")), r.URL.Query().Get("s"))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, BookingList(vm))
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "dashboard", "/dashboard", false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm DashboardVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildDashboardVM(r.Context(), nav.ActiveHousehold.ID, nav.Month,
			r.URL.Query().Get("p"), parseID(r.URL.Query().Get("view")), r.URL.Query().Get("g"))
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, DashboardPage(nav, vm))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "settings", "/settings", false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vm, err := s.buildSettingsVM(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, SettingsPage(nav, vm))
}

// ---- view-model builders ----

func (s *Server) buildBookingsVM(ctx context.Context, householdID int64, month, sortKey string) (BookingsVM, error) {
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return BookingsVM{}, err
	}

	categories := make(map[int64]store.Category, len(data.Categories))
	for _, c := range data.Categories {
		categories[c.ID] = c
	}
	members := make(map[int64]store.Member, len(data.Members))
	for _, m := range data.Members {
		members[m.ID] = m
	}

	vm := BookingsVM{
		Month:    month,
		Sort:     cleanSort(sortKey),
		Report:   calc.BuildMonthReport(data, month, calc.Everyone),
		Bookings: make([]BookingRow, 0, len(data.Bookings)),
	}
	for _, b := range data.Bookings {
		row := BookingRow{
			Booking:     b,
			Category:    categories[b.CategoryID],
			Splits:      data.Splits[b.ID],
			TagIDs:      data.TagLinks[b.ID],
			Overrides:   data.Overrides[b.ID],
			Month:       month,
			MemberCount: len(data.Members),
		}
		if b.PayerMemberID != nil {
			row.Payer = members[*b.PayerMemberID]
		}
		row.Carriers = carriers(b, data.Members, row.Splits)
		vm.Bookings = append(vm.Bookings, row)
	}
	sortBookings(vm.Bookings, vm.Sort)
	return vm, nil
}

// carriers names the members a booking is split between, keeping the household
// order. Nobody picked means nobody carries it, and so does a share of zero:
// the row must not name someone the report then leaves out.
func carriers(b store.Booking, members []store.Member, splits []store.BookingSplit) []store.Member {
	out := make([]store.Member, 0, len(splits))
	for _, m := range members {
		for _, s := range splits {
			if s.MemberID != m.ID {
				continue
			}
			if b.SplitMode != store.SplitEqual && s.Value == 0 {
				break
			}
			out = append(out, m)
			break
		}
	}
	return out
}

// sortBookings orders the list by the chosen key. Every key falls back to the
// monthly amount, so equal names or categories still read largest first.
func sortBookings(rows []BookingRow, key string) {
	byAmount := func(i, j int) bool { return rows[i].MonthlyCents() > rows[j].MonthlyCents() }
	// A grouping key only says which bucket a row lands in, so the amount
	// decides inside the bucket.
	groupBy := func(rank func(BookingRow) int) func(i, j int) bool {
		return func(i, j int) bool {
			if a, b := rank(rows[i]), rank(rows[j]); a != b {
				return a < b
			}
			return byAmount(i, j)
		}
	}

	var less func(i, j int) bool
	switch cleanSort(key) {
	case SortAmount:
		less = byAmount
	case SortName:
		less = func(i, j int) bool { return lessName(rows[i].Booking.Name, rows[j].Booking.Name) }
	case SortCategory:
		less = func(i, j int) bool {
			if a, b := rows[i].Category.Name, rows[j].Category.Name; a != b {
				return lessName(a, b)
			}
			return byAmount(i, j)
		}
	case SortPayer:
		less = func(i, j int) bool {
			if a, b := rows[i].Payer.Name, rows[j].Payer.Name; a != b {
				return lessName(a, b)
			}
			return byAmount(i, j)
		}
	case SortCarriers:
		less = groupBy(func(r BookingRow) int { return len(r.Carriers) })
	case SortFrequency:
		less = groupBy(func(r BookingRow) int { return frequencyRank(r.Booking.Frequency) })
	case SortDue:
		less = groupBy(func(r BookingRow) int { return duePointRank(r.Booking) })
	case SortNature:
		less = groupBy(func(r BookingRow) int {
			if r.Booking.CostNature == store.CostFix {
				return 0
			}
			return 1
		})
	case SortClass:
		less = groupBy(func(r BookingRow) int { return classRank(r.Booking.BudgetClass) })
	case SortUpdated:
		less = func(i, j int) bool {
			if a, b := rows[i].Booking.UpdatedAt, rows[j].Booking.UpdatedAt; a != b {
				return a > b
			}
			return byAmount(i, j)
		}
	default:
		less = func(i, j int) bool {
			if rows[i].IsIncome() != rows[j].IsIncome() {
				return rows[i].IsIncome()
			}
			return byAmount(i, j)
		}
	}
	sort.SliceStable(rows, less)
}

// frequencyRank orders rhythms from the most frequent to the rarest, which is
// how often the money actually moves.
func frequencyRank(f store.Frequency) int {
	switch f {
	case store.FreqWeekly:
		return 0
	case store.FreqMonthly:
		return 1
	case store.FreqQuarterly:
		return 2
	case store.FreqYearly:
		return 3
	default:
		return 4
	}
}

// classRank keeps the 50/30/20 classes in the order the rule names them.
func classRank(c store.BudgetClass) int {
	switch c {
	case store.ClassNeed:
		return 0
	case store.ClassWant:
		return 1
	default:
		return 2
	}
}

// duePointRank walks the month from front to back, so the list reads in the
// order the money actually leaves. A one-off has no due point and goes last.
func duePointRank(b store.Booking) int {
	if !b.Frequency.Recurring() {
		return 3
	}
	switch b.DuePoint {
	case store.DueMiddle:
		return 1
	case store.DueEnd:
		return 2
	default:
		return 0
	}
}

// lessName compares captions the way a reader would: case is irrelevant and a
// booking without a name belongs at the end rather than at the top.
func lessName(a, b string) bool {
	switch {
	case a == b:
		return false
	case a == "":
		return false
	case b == "":
		return true
	}
	return strings.ToLower(a) < strings.ToLower(b)
}

// periodMonths is how many months each period key spans.
var periodMonths = map[string]int{"1m": 1, "2m": 2, "3m": 3, "6m": 6, periodYear: 12}

// periodYear covers the calendar year rather than a window around the anchor.
const periodYear = "12m"

// periodOrder keeps the selector in a sensible order, which a map cannot.
var periodOrder = []struct{ key, label string }{
	{"1m", "dash.rangeMonth"},
	{"2m", "dash.range2m"},
	{"3m", "dash.rangeQuarter"},
	{"6m", "dash.rangeHalf"},
	{periodYear, "dash.rangeYear"},
}

// cleanPeriod falls back to a single month for anything unknown.
func cleanPeriod(key string) string {
	if _, ok := periodMonths[key]; ok {
		return key
	}
	return "1m"
}

// rangeMonths returns the months a period covers, centered on the anchor month
// so the month you picked stays in the middle of the chart. An even span puts
// the extra month after it, because a plan looks forward rather than back.
// The year is the exception: a household book is kept per calendar year, and a
// window running from March to February compares against nothing.
func rangeMonths(key, anchor string) []string {
	key = cleanPeriod(key)
	if key == periodYear {
		return calendarYear(anchor)
	}
	n := periodMonths[key]
	out := make([]string, n)
	back := (n - 1) / 2
	for i := range out {
		out[i] = ShiftMonth(anchor, i-back)
	}
	return out
}

// calendarYear returns January to December of the anchor's year.
func calendarYear(anchor string) []string {
	year := NormalizeMonth(anchor)[:4]
	out := make([]string, 0, 12)
	for m := 1; m <= 12; m++ {
		out = append(out, fmt.Sprintf("%s-%02d", year, m))
	}
	return out
}

// dashboardURL builds a link that keeps every dashboard control in the query,
// so switching one of them does not reset the others.
func dashboardURL(month, period string, member int64, grouping string) string {
	return "/dashboard?m=" + month + "&p=" + cleanPeriod(period) +
		"&view=" + strconv.FormatInt(member, 10) +
		"&g=" + calc.CleanGrouping(grouping)
}

// Canvas sizes in user space; the SVGs scale to their container.
const (
	sankeyWidth  = 900.0
	sankeyHeight = 460.0
	chartWidth   = 760.0
	chartHeight  = 260.0
	stackWidth   = 900.0
	stackHeight  = 320.0
	fixedCostTop = 8
)

func (s *Server) buildDashboardVM(ctx context.Context, householdID int64, month, period string, member int64, grouping string) (DashboardVM, error) {
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return DashboardVM{}, err
	}
	period = cleanPeriod(period)
	member = knownMember(data.Members, member)
	grouping = calc.CleanGrouping(grouping)
	months := rangeMonths(period, month)
	span := len(months)

	vm := DashboardVM{
		Report:     calc.PeriodReport(data, months, member),
		Trend:      calc.Trend(data, months, member),
		PeriodKey:  period,
		ViewMember: member,
		Grouping:   grouping,
		PrevURL:    dashboardURL(ShiftMonth(month, -span), period, member, grouping),
		NextURL:    dashboardURL(ShiftMonth(month, span), period, member, grouping),
		RangeLabel: rangeLabel(ctx, months),
	}
	// In the household view both scopes are the same figure, so it is only
	// aggregated a second time when a person is selected.
	vm.HouseholdReport = vm.Report
	if member != calc.Everyone {
		vm.HouseholdReport = calc.PeriodReport(data, months, calc.Everyone)
	}

	for _, p := range periodOrder {
		label := T(ctx, p.label)
		if p.key == period {
			vm.PeriodLabel = label
		}
		vm.Periods = append(vm.Periods, PeriodOption{
			Key:    p.key,
			Label:  label,
			Active: p.key == period,
			URL:    dashboardURL(month, p.key, member, grouping),
		})
	}
	for _, g := range []struct{ key, label string }{
		{calc.GroupCategory, "dash.byCategory"},
		{calc.GroupClass, "dash.byClass"},
	} {
		vm.Groupings = append(vm.Groupings, PeriodOption{
			Key:    g.key,
			Label:  T(ctx, g.label),
			Active: g.key == grouping,
			URL:    dashboardURL(month, period, member, g.key),
		})
	}

	vm.Views = append(vm.Views, ViewOption{
		Member: calc.Everyone,
		Label:  T(ctx, "dash.viewHousehold"),
		Active: member == calc.Everyone,
		URL:    dashboardURL(month, period, calc.Everyone, grouping),
	})
	for _, m := range data.Members {
		vm.Views = append(vm.Views, ViewOption{
			Member: m.ID,
			Label:  m.Name,
			Color:  m.Color,
			Active: member == m.ID,
			URL:    dashboardURL(month, period, m.ID, grouping),
		})
	}

	vm.Chart = calc.BuildTrendChart(vm.Trend, chartWidth, chartHeight)
	vm.Stack = calc.BuildStackChart(data, months, member, grouping, stackWidth, stackHeight)
	// The year block is a year block: it keeps the calendar year whatever the
	// period control says, because one column of a single month tells nothing
	// the tiles above do not already say.
	vm.MatrixYear = NormalizeMonth(month)[:4]
	vm.Matrix = calc.BuildMatrix(data, calendarYear(month), member)
	vm.FixedTop = calc.FixedCosts(data, months, member, fixedCostTop)
	vm.Sankey = calc.BuildSankey(ctx, data, vm.Report, months, sankeyWidth, sankeyHeight)
	vm.Settlement = calc.Settlement(data, months)
	return vm, nil
}

// knownMember drops a member id that does not belong to the household, so a
// stale link falls back to the household view instead of an empty report.
func knownMember(members []store.Member, id int64) int64 {
	for _, m := range members {
		if m.ID == id {
			return id
		}
	}
	return calc.Everyone
}

// rangeLabel names the covered period, collapsing a single month to its label.
func rangeLabel(ctx context.Context, months []string) string {
	if len(months) == 0 {
		return ""
	}
	first, last := months[0], months[len(months)-1]
	if first == last {
		return MonthLabel(ctx, first)
	}
	return MonthLabel(ctx, first) + " – " + MonthLabel(ctx, last)
}

func (s *Server) buildSettingsVM(ctx context.Context) (SettingsVM, error) {
	households, err := s.store.ListHouseholds(ctx)
	if err != nil {
		return SettingsVM{}, err
	}
	activeID, err := s.store.ActiveHouseholdID(ctx)
	if err != nil {
		return SettingsVM{}, err
	}
	vm := SettingsVM{Households: households, ActiveID: activeID, Icons: IconKeys()}
	if activeID == 0 {
		return vm, nil
	}
	if vm.Members, err = s.store.ListMembers(ctx, activeID); err != nil {
		return SettingsVM{}, err
	}
	if vm.Categories, err = s.store.ListCategories(ctx, activeID); err != nil {
		return SettingsVM{}, err
	}
	if vm.Tags, err = s.store.ListTags(ctx, activeID); err != nil {
		return SettingsVM{}, err
	}
	if vm.CatUsage, err = s.store.CountCategoryUsage(ctx, activeID); err != nil {
		return SettingsVM{}, err
	}
	vm.Suggestions = suggestCategories(vm.Categories)
	return vm, nil
}
