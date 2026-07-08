package server

import (
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
		rep, err := s.buildMonthReport(nav.ActiveHousehold.ID, nav.Month)
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
		vm, err = s.buildExpensesVM(nav.ActiveHousehold.ID)
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
		vm, err = s.buildIncomeVM(nav.ActiveHousehold.ID, nav.Month)
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
		vm, err = s.buildStatisticsVM(nav.ActiveHousehold.ID, nav.Month)
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
	vm, err := s.buildSettingsVM()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.SettingsPage(nav, vm))
}

// ---- view-model builders ----

func (s *Server) buildExpensesVM(householdID int64) (web.ExpensesVM, error) {
	members, err := s.store.ListMembers(householdID)
	if err != nil {
		return web.ExpensesVM{}, err
	}
	sections, err := s.store.ListSections(householdID)
	if err != nil {
		return web.ExpensesVM{}, err
	}
	categories, err := s.store.ListCategories(householdID)
	if err != nil {
		return web.ExpensesVM{}, err
	}
	expenses, err := s.store.ListExpenses(householdID)
	if err != nil {
		return web.ExpensesVM{}, err
	}
	splits, err := s.store.ListSplitsForHousehold(householdID)
	if err != nil {
		return web.ExpensesVM{}, err
	}

	rowsBySection := make(map[int64][]web.ExpenseRow)
	for _, e := range expenses {
		var sid int64
		if e.SectionID != nil {
			sid = *e.SectionID
		}
		rowsBySection[sid] = append(rowsBySection[sid], web.ExpenseRow{Expense: e, Splits: splits[e.ID]})
	}

	var groups []web.SectionGroup
	for i := range sections {
		sec := sections[i]
		rows := rowsBySection[sec.ID]
		groups = append(groups, web.SectionGroup{Section: &sec, Expenses: rows, TotalCents: sumMonthly(rows)})
	}
	if rows := rowsBySection[0]; len(rows) > 0 || len(sections) == 0 {
		groups = append(groups, web.SectionGroup{Section: nil, Expenses: rows, TotalCents: sumMonthly(rows)})
	}

	return web.ExpensesVM{Groups: groups, Members: members, Sections: sections, Categories: categories}, nil
}

func (s *Server) buildIncomeVM(householdID int64, month string) (web.IncomeVM, error) {
	members, err := s.store.ListMembers(householdID)
	if err != nil {
		return web.IncomeVM{}, err
	}
	incomes, err := s.store.ListIncomes(householdID, month)
	if err != nil {
		return web.IncomeVM{}, err
	}
	byMember := make(map[int64][]store.Income)
	for _, in := range incomes {
		byMember[in.MemberID] = append(byMember[in.MemberID], in)
	}
	vm := web.IncomeVM{PrevMonth: web.ShiftMonth(month, -1), HasPrev: true}
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

func (s *Server) buildStatisticsVM(householdID int64, month string) (web.StatisticsVM, error) {
	const window = 12

	months := make([]string, 0, window)
	m := web.ShiftMonth(month, -(window - 1))
	for i := 0; i < window; i++ {
		months = append(months, m)
		m = web.ShiftMonth(m, 1)
	}

	var vm web.StatisticsVM
	var sumIncome, sumExpense int64
	var dataMonths int64
	for _, mm := range months {
		rep, err := s.buildMonthReport(householdID, mm)
		if err != nil {
			return web.StatisticsVM{}, err
		}
		vm.Months = append(vm.Months, web.StatMonth{
			Month:        mm,
			IncomeCents:  rep.IncomeCents,
			ExpenseCents: rep.ExpenseCents,
			BalanceCents: rep.BalanceCents,
		})
		if rep.IncomeCents > vm.MaxCents {
			vm.MaxCents = rep.IncomeCents
		}
		if rep.ExpenseCents > vm.MaxCents {
			vm.MaxCents = rep.ExpenseCents
		}
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

	cur, err := s.buildMonthReport(householdID, month)
	if err != nil {
		return web.StatisticsVM{}, err
	}
	vm.Current = cur
	return vm, nil
}

func (s *Server) buildSettingsVM() (web.SettingsVM, error) {
	households, err := s.store.ListHouseholds()
	if err != nil {
		return web.SettingsVM{}, err
	}
	activeID, err := s.store.ActiveHouseholdID()
	if err != nil {
		return web.SettingsVM{}, err
	}
	var (
		members    []store.Member
		sections   []store.Section
		categories []store.Category
	)
	if activeID != 0 {
		if members, err = s.store.ListMembers(activeID); err != nil {
			return web.SettingsVM{}, err
		}
		if sections, err = s.store.ListSections(activeID); err != nil {
			return web.SettingsVM{}, err
		}
		if categories, err = s.store.ListCategories(activeID); err != nil {
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
