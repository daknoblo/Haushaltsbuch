package web

import (
	"context"
	"net/http"

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

func (s *Server) handleExpenses(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "expenses", "/expenses", false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm ExpensesVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildExpensesVM(r.Context(), nav.ActiveHousehold.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, ExpensesPage(nav, vm))
}

func (s *Server) handleIncome(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "income", "/income", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm IncomeVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildIncomeVM(r.Context(), nav.ActiveHousehold.ID, nav.Month)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, IncomePage(nav, vm))
}

func (s *Server) handleStatistics(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "statistics", "/statistics", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var vm StatisticsVM
	if nav.ActiveHousehold.ID != 0 {
		vm, err = s.buildStatisticsVM(r.Context(), nav.ActiveHousehold.ID, nav.Month)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.render(w, r, StatisticsPage(nav, vm))
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

func (s *Server) buildExpensesVM(ctx context.Context, householdID int64) (ExpensesVM, error) {
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return ExpensesVM{}, err
	}

	rowsBySection := make(map[int64][]ExpenseRow, len(data.sections)+1)
	for _, e := range data.expenses {
		var sid int64
		if e.SectionID != nil {
			sid = *e.SectionID
		}
		rowsBySection[sid] = append(rowsBySection[sid], ExpenseRow{Expense: e, Splits: data.splits[e.ID]})
	}

	groups := make([]SectionGroup, 0, len(data.sections)+1)
	for i := range data.sections {
		sec := data.sections[i]
		rows := rowsBySection[sec.ID]
		groups = append(groups, SectionGroup{Section: &sec, Expenses: rows, TotalCents: sumMonthly(rows)})
	}
	if rows := rowsBySection[0]; len(rows) > 0 || len(data.sections) == 0 {
		groups = append(groups, SectionGroup{Section: nil, Expenses: rows, TotalCents: sumMonthly(rows)})
	}

	return ExpensesVM{
		Groups:     groups,
		Members:    data.members,
		Sections:   data.sections,
		Categories: data.categories,
	}, nil
}

func (s *Server) buildIncomeVM(ctx context.Context, householdID int64, month string) (IncomeVM, error) {
	members, err := s.store.ListMembers(ctx, householdID)
	if err != nil {
		return IncomeVM{}, err
	}
	incomes, err := s.store.ListIncomes(ctx, householdID, month)
	if err != nil {
		return IncomeVM{}, err
	}
	byMember := make(map[int64][]store.Income)
	for _, in := range incomes {
		byMember[in.MemberID] = append(byMember[in.MemberID], in)
	}
	vm := IncomeVM{PrevMonth: ShiftMonth(month, -1)}
	for _, m := range members {
		lines := byMember[m.ID]
		var tot int64
		for _, l := range lines {
			tot += l.AmountCents
		}
		vm.Members = append(vm.Members, IncomeMemberVM{Member: m, Lines: lines, TotalCents: tot})
		vm.TotalCents += tot
	}
	return vm, nil
}

func (s *Server) buildStatisticsVM(ctx context.Context, householdID int64, month string) (StatisticsVM, error) {
	const window = 12

	months := make([]string, window)
	for i := range months {
		months[i] = ShiftMonth(month, i-(window-1))
	}

	// The whole window is aggregated from a single load of the household data
	// plus one range query for the income lines.
	data, err := s.loadHouseholdData(ctx, householdID)
	if err != nil {
		return StatisticsVM{}, err
	}
	incomes, err := s.store.ListIncomesRange(ctx, householdID, months[0], month)
	if err != nil {
		return StatisticsVM{}, err
	}

	vm := StatisticsVM{Months: make([]StatMonth, 0, window)}
	var sumIncome, sumExpense int64
	var dataMonths int64
	for _, mm := range months {
		rep := data.report(mm, incomes[mm])
		vm.Months = append(vm.Months, StatMonth{
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

func (s *Server) buildSettingsVM(ctx context.Context) (SettingsVM, error) {
	households, err := s.store.ListHouseholds(ctx)
	if err != nil {
		return SettingsVM{}, err
	}
	activeID, err := s.store.ActiveHouseholdID(ctx)
	if err != nil {
		return SettingsVM{}, err
	}
	var (
		members    []store.Member
		sections   []store.Section
		categories []store.Category
	)
	if activeID != 0 {
		if members, err = s.store.ListMembers(ctx, activeID); err != nil {
			return SettingsVM{}, err
		}
		if sections, err = s.store.ListSections(ctx, activeID); err != nil {
			return SettingsVM{}, err
		}
		if categories, err = s.store.ListCategories(ctx, activeID); err != nil {
			return SettingsVM{}, err
		}
	}
	return SettingsVM{
		Households: households,
		ActiveID:   activeID,
		Members:    members,
		Sections:   sections,
		Categories: categories,
	}, nil
}

func sumMonthly(rows []ExpenseRow) int64 {
	var total int64
	for _, r := range rows {
		total += calc.MonthlyCents(r.Expense)
	}
	return total
}
