package web

import (
	"context"
	"net/http"
	"sort"

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
		vm, err = s.buildBookingsVM(r.Context(), nav.ActiveHousehold.ID, nav.Month)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, BookingsPage(nav, vm))
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "dashboard", "/dashboard", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm DashboardVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildDashboardVM(r.Context(), nav.ActiveHousehold.ID, nav.Month, r.URL.Query().Get("range"))
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

func (s *Server) buildBookingsVM(ctx context.Context, householdID int64, month string) (BookingsVM, error) {
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return BookingsVM{}, err
	}

	rowsBySection := make(map[int64][]BookingRow, len(data.Sections)+1)
	var income []BookingRow
	for _, b := range data.Bookings {
		row := BookingRow{Booking: b, Splits: data.Splits[b.ID], TagIDs: data.TagLinks[b.ID]}
		if b.Direction == store.DirIncome {
			income = append(income, row)
			continue
		}
		var sid int64
		if b.SectionID != nil {
			sid = *b.SectionID
		}
		rowsBySection[sid] = append(rowsBySection[sid], row)
	}

	groups := make([]SectionGroup, 0, len(data.Sections)+1)
	for i := range data.Sections {
		sec := data.Sections[i]
		rows := rowsBySection[sec.ID]
		groups = append(groups, SectionGroup{Section: &sec, Bookings: rows, TotalCents: sumMonthly(rows)})
	}
	if rows := rowsBySection[0]; len(rows) > 0 || len(data.Sections) == 0 {
		groups = append(groups, SectionGroup{Section: nil, Bookings: rows, TotalCents: sumMonthly(rows)})
	}

	return BookingsVM{
		Groups:     groups,
		Income:     income,
		Members:    data.Members,
		Sections:   data.Sections,
		Categories: data.Categories,
		Tags:       data.Tags,
		Report:     calc.BuildMonthReport(data, month),
	}, nil
}

// rangeMonths turns a period key into the months it covers, ending at the
// currently selected month.
func rangeMonths(key, month string) []string {
	switch key {
	case "last":
		return []string{ShiftMonth(month, -1)}
	case "ytd":
		if len(month) < 7 {
			return []string{month}
		}
		out := make([]string, 0, 12)
		for m := month[:4] + "-01"; m <= month && len(out) < 12; m = ShiftMonth(m, 1) {
			out = append(out, m)
		}
		if len(out) == 0 {
			out = append(out, month)
		}
		return out
	case "12m":
		out := make([]string, 12)
		for i := range out {
			out[i] = ShiftMonth(month, i-11)
		}
		return out
	default: // the selected month
		return []string{month}
	}
}

func rangeOptions(ctx context.Context, active string) []RangeOption {
	defs := []struct{ key, label string }{
		{"month", T(ctx, "dash.rangeMonth")},
		{"last", T(ctx, "dash.rangeLastMonth")},
		{"ytd", T(ctx, "dash.rangeYear")},
		{"12m", T(ctx, "dash.range12")},
	}
	out := make([]RangeOption, 0, len(defs))
	for _, d := range defs {
		out = append(out, RangeOption{Key: d.key, Label: d.label, Active: d.key == active})
	}
	return out
}

// Canvas of the flow diagram in user space; the SVG scales to its container.
const (
	sankeyWidth  = 900.0
	sankeyHeight = 460.0
)

func (s *Server) buildDashboardVM(ctx context.Context, householdID int64, month, rangeKey string) (DashboardVM, error) {
	switch rangeKey {
	case "last", "ytd", "12m":
	default:
		rangeKey = "month"
	}

	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return DashboardVM{}, err
	}

	// The headline figures describe the month the range ends on, while the
	// timeline below shows every month the range covers.
	months := rangeMonths(rangeKey, month)
	focus := months[len(months)-1]

	vm := DashboardVM{
		Report:   calc.BuildMonthReport(data, focus),
		RangeKey: rangeKey,
		Ranges:   rangeOptions(ctx, rangeKey),
	}
	for _, o := range vm.Ranges {
		if o.Active {
			vm.RangeLabel = o.Label
		}
	}

	var sumIncome, sumExpense, dataMonths int64
	for _, rep := range calc.Trend(data, months) {
		vm.Months = append(vm.Months, StatMonth{
			Month:        rep.Month,
			IncomeCents:  rep.IncomeCents,
			ExpenseCents: rep.ExpenseCents,
			FixedCents:   rep.FixedCents,
			BalanceCents: rep.BalanceCents,
		})
		vm.MaxCents = max(vm.MaxCents, rep.IncomeCents, rep.ExpenseCents)
		if rep.IncomeCents != 0 || rep.ExpenseCents != 0 {
			sumIncome += rep.IncomeCents
			sumExpense += rep.ExpenseCents
			dataMonths++
		}
	}
	if dataMonths > 0 {
		vm.AvgIncome = sumIncome / dataMonths
		vm.AvgExpense = sumExpense / dataMonths
	}

	vm.FixedTop = fixedCostBreakdown(data, focus)
	vm.Sankey = calc.BuildSankey(data, vm.Report, sankeyWidth, sankeyHeight)
	return vm, nil
}

// fixedCostBreakdown lists the fixed-cost bookings of a month, largest first,
// because that is the list worth renegotiating.
func fixedCostBreakdown(d calc.Data, month string) []calc.LabeledTotal {
	out := make([]calc.LabeledTotal, 0, 8)
	for _, b := range d.Bookings {
		if b.Direction != store.DirExpense || b.CostNature != store.CostFix {
			continue
		}
		if !calc.ActiveIn(b, month) {
			continue
		}
		out = append(out, calc.LabeledTotal{Key: b.ID, Label: b.Name, Cents: calc.MonthlyCents(b)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Cents > out[j].Cents })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
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
	vm := SettingsVM{Households: households, ActiveID: activeID}
	if activeID == 0 {
		return vm, nil
	}
	if vm.Members, err = s.store.ListMembers(ctx, activeID); err != nil {
		return SettingsVM{}, err
	}
	if vm.Sections, err = s.store.ListSections(ctx, activeID); err != nil {
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
	return vm, nil
}

func sumMonthly(rows []BookingRow) int64 {
	var total int64
	for _, r := range rows {
		total += calc.MonthlyCents(r.Booking)
	}
	return total
}
