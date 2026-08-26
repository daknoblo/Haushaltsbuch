package web

import (
	"context"
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
			r.URL.Query().Get("p"), parseID(r.URL.Query().Get("view")))
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
		row.Carriers = carriers(data.Members, row.Splits)
		vm.Bookings = append(vm.Bookings, row)
	}
	sortBookings(vm.Bookings, vm.Sort)
	return vm, nil
}

// carriers names the members a booking is split between, keeping the household
// order. No split at all means everyone, which is how the allocation reads it.
func carriers(members []store.Member, splits []store.BookingSplit) []store.Member {
	if len(splits) == 0 {
		return members
	}
	out := make([]store.Member, 0, len(splits))
	for _, m := range members {
		for _, s := range splits {
			if s.MemberID == m.ID {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// sortBookings orders the list by the chosen key. Every key falls back to the
// monthly amount, so equal names or categories still read largest first.
func sortBookings(rows []BookingRow, key string) {
	byAmount := func(i, j int) bool { return rows[i].MonthlyCents() > rows[j].MonthlyCents() }
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
var periodMonths = map[string]int{"1m": 1, "2m": 2, "3m": 3, "6m": 6, "12m": 12}

// periodOrder keeps the selector in a sensible order, which a map cannot.
var periodOrder = []struct{ key, label string }{
	{"1m", "dash.rangeMonth"},
	{"2m", "dash.range2m"},
	{"3m", "dash.rangeQuarter"},
	{"6m", "dash.rangeHalf"},
	{"12m", "dash.rangeYear"},
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
func rangeMonths(key, anchor string) []string {
	n := periodMonths[cleanPeriod(key)]
	out := make([]string, n)
	back := (n - 1) / 2
	for i := range out {
		out[i] = ShiftMonth(anchor, i-back)
	}
	return out
}

// dashboardURL builds a link that keeps every dashboard control in the query,
// so switching one of them does not reset the others.
func dashboardURL(month, period string, member int64) string {
	return "/dashboard?m=" + month + "&p=" + cleanPeriod(period) +
		"&view=" + strconv.FormatInt(member, 10)
}

// Canvas sizes in user space; both SVGs scale to their container.
const (
	sankeyWidth  = 900.0
	sankeyHeight = 460.0
	chartWidth   = 760.0
	chartHeight  = 260.0
	fixedCostTop = 8
)

func (s *Server) buildDashboardVM(ctx context.Context, householdID int64, month, period string, member int64) (DashboardVM, error) {
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return DashboardVM{}, err
	}
	period = cleanPeriod(period)
	member = knownMember(data.Members, member)
	months := rangeMonths(period, month)
	span := len(months)

	vm := DashboardVM{
		Report:     calc.PeriodReport(data, months, member),
		Trend:      calc.Trend(data, months, member),
		PeriodKey:  period,
		ViewMember: member,
		PrevURL:    dashboardURL(ShiftMonth(month, -span), period, member),
		NextURL:    dashboardURL(ShiftMonth(month, span), period, member),
		RangeLabel: rangeLabel(ctx, months),
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
			URL:    dashboardURL(month, p.key, member),
		})
	}

	vm.Views = append(vm.Views, ViewOption{
		Member: calc.Everyone,
		Label:  T(ctx, "dash.viewHousehold"),
		Active: member == calc.Everyone,
		URL:    dashboardURL(month, period, calc.Everyone),
	})
	for _, m := range data.Members {
		vm.Views = append(vm.Views, ViewOption{
			Member: m.ID,
			Label:  m.Name,
			Color:  m.Color,
			Active: member == m.ID,
			URL:    dashboardURL(month, period, m.ID),
		})
	}

	vm.Chart = calc.BuildTrendChart(vm.Trend, chartWidth, chartHeight)
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
