package server

import (
	"context"
	"net/http"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/web"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "overview", "/", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm web.OverviewVM
	if nav.ActiveHousehold.ID != 0 {
		rep, err := s.buildMonthReport(r.Context(), nav.ActiveHousehold.ID, nav.Month)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		vm.Report = rep
	}
	s.render(w, r, web.OverviewPage(nav, vm))
}

func (s *Server) handleExpenses(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "expenses", "/expenses", false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm web.ExpensesVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildExpensesVM(r.Context(), nav.ActiveHousehold.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, web.ExpensesPage(nav, vm))
}

func (s *Server) handleIncome(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "income", "/income", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm web.IncomeVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildIncomeVM(r.Context(), nav.ActiveHousehold.ID, nav.Month)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, web.IncomePage(nav, vm))
}

func (s *Server) handleStatistics(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "statistics", "/statistics", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm web.StatisticsVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildStatisticsVM(r.Context(), nav.ActiveHousehold.ID, nav.Month)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, web.StatisticsPage(nav, vm))
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
	s.render(w, r, web.SettingsPage(nav, vm))
}

// ---- view-model builders ----

func (s *Server) buildExpensesVM(ctx context.Context, householdID int64) (web.ExpensesVM, error) {
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return web.ExpensesVM{}, err
	}

	rowsBySection := make(map[int64][]web.ExpenseRow, len(data.sections)+1)
	for _, e := range data.expenses {
		var sid int64
		if e.SectionID != nil {
			sid = *e.SectionID
		}
		rowsBySection[sid] = append(rowsBySection[sid], web.ExpenseRow{Expense: e, Splits: data.splits[e.ID]})
	}

	groups := make([]web.SectionGroup, 0, len(data.sections)+1)
	for i := range data.sections {
		sec := data.sections[i]
		rows := rowsBySection[sec.ID]
		groups = append(groups, web.SectionGroup{Section: &sec, Expenses: rows, TotalCents: sumMonthly(rows)})
	}
	if rows := rowsBySection[0]; len(rows) > 0 || len(data.sections) == 0 {
		groups = append(groups, web.SectionGroup{Section: nil, Expenses: rows, TotalCents: sumMonthly(rows)})
	}

	return web.ExpensesVM{
		Groups:     groups,
		Members:    data.members,
		Sections:   data.sections,
		Categories: data.categories,
	}, nil
}

func (s *Server) buildIncomeVM(ctx context.Context, householdID int64, month string) (web.IncomeVM, error) {
	members, err := s.store.ListMembers(ctx, householdID)
	if err != nil {
		return web.IncomeVM{}, err
	}
	incomes, err := s.store.ListIncomes(ctx, householdID, month)
	if err != nil {
		return web.IncomeVM{}, err
	}
	byMember := make(map[int64][]store.Income)
	for _, in := range incomes {
		byMember[in.MemberID] = append(byMember[in.MemberID], in)
	}
	vm := web.IncomeVM{PrevMonth: web.ShiftMonth(month, -1)}
	for _, m := range members {
		lines := byMember[m.ID]
		var tot int64
		for _, l := range lines {
			tot += l.AmountCents
		}
		vm.Members = append(vm.Members, web.IncomeMemberVM{Member: m, Lines: lines, TotalCents: tot})
		vm.TotalCents += tot
	}
	return vm, nil
}

func (s *Server) buildStatisticsVM(ctx context.Context, householdID int64, month string) (web.StatisticsVM, error) {
	const window = 12

	months := make([]string, window)
	for i := range months {
		months[i] = web.ShiftMonth(month, i-(window-1))
	}

	// The whole window is aggregated from a single load of the household data
	// plus one range query for the income lines.
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return web.StatisticsVM{}, err
	}
	incomes, err := s.store.ListIncomesRange(ctx, householdID, months[0], month)
	if err != nil {
		return web.StatisticsVM{}, err
	}

	vm := web.StatisticsVM{Months: make([]web.StatMonth, 0, window)}
	var sumIncome, sumExpense int64
	var dataMonths int64
	for _, mm := range months {
		rep := data.report(mm, incomes[mm])
		vm.Months = append(vm.Months, web.StatMonth{
			Month:        mm,
			IncomeCents:  rep.IncomeCents,
			ExpenseCents: rep.ExpenseCents,
			BalanceCents: rep.BalanceCents,
		})
		vm.MaxCents = max(vm.MaxCents, rep.IncomeCents, rep.ExpenseCents)
		if rep.IncomeCents != 0 || rep.ExpenseCents != 0 {
			sumIncome += rep.IncomeCents
			sumExpense += rep.ExpenseCents
			dataMonths++
		}
		if mm == month {
			vm.Current = rep
		}
	}
	if dataMonths > 0 {
		vm.AvgIncome = sumIncome / dataMonths
		vm.AvgExpense = sumExpense / dataMonths
	}
	return vm, nil
}

func (s *Server) buildSettingsVM(ctx context.Context) (web.SettingsVM, error) {
	households, err := s.store.ListHouseholds(ctx)
	if err != nil {
		return web.SettingsVM{}, err
	}
	activeID, err := s.store.ActiveHouseholdID(ctx)
	if err != nil {
		return web.SettingsVM{}, err
	}
	var (
		members    []store.Member
		sections   []store.Section
		categories []store.Category
	)
	if activeID != 0 {
		if members, err = s.store.ListMembers(ctx, activeID); err != nil {
			return web.SettingsVM{}, err
		}
		if sections, err = s.store.ListSections(ctx, activeID); err != nil {
			return web.SettingsVM{}, err
		}
		if categories, err = s.store.ListCategories(ctx, activeID); err != nil {
			return web.SettingsVM{}, err
		}
	}
	return web.SettingsVM{
		Households: households,
		ActiveID:   activeID,
		Members:    members,
		Sections:   sections,
		Categories: categories,
	}, nil
}

func sumMonthly(rows []web.ExpenseRow) int64 {
	var total int64
	for _, r := range rows {
		total += calc.MonthlyCents(r.Expense)
	}
	return total
}
