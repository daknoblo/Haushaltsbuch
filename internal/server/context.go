package server

import (
	"net/http"
	"strconv"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/version"
	"github.com/daknoblo/Haushaltsbuch/internal/web"
)

// activeHousehold returns the currently active household, or a zero value when
// none is set.
func (s *Server) activeHousehold() (store.Household, error) {
	id, err := s.store.ActiveHouseholdID()
	if err != nil {
		return store.Household{}, err
	}
	if id == 0 {
		return store.Household{}, nil
	}
	h, err := s.store.GetHousehold(id)
	if err != nil {
		return store.Household{}, err
	}
	return h, nil
}

// buildNav assembles the shared page chrome data.
func (s *Server) buildNav(r *http.Request, active, path string, showMonth bool) (web.Nav, error) {
	households, err := s.store.ListHouseholds()
	if err != nil {
		return web.Nav{}, err
	}
	ah, err := s.activeHousehold()
	if err != nil {
		return web.Nav{}, err
	}
	return web.Nav{
		Active:          active,
		Path:            path,
		Households:      households,
		ActiveHousehold: ah,
		Month:           web.NormalizeMonth(r.URL.Query().Get("m")),
		ShowMonthNav:    showMonth,
		Version:         version.Version,
	}, nil
}

// buildMonthReport loads all data for a household/month and aggregates it.
func (s *Server) buildMonthReport(householdID int64, month string) (calc.MonthReport, error) {
	members, err := s.store.ListMembers(householdID)
	if err != nil {
		return calc.MonthReport{}, err
	}
	sections, err := s.store.ListSections(householdID)
	if err != nil {
		return calc.MonthReport{}, err
	}
	categories, err := s.store.ListCategories(householdID)
	if err != nil {
		return calc.MonthReport{}, err
	}
	expenses, err := s.store.ListExpenses(householdID)
	if err != nil {
		return calc.MonthReport{}, err
	}
	splits, err := s.store.ListSplitsForHousehold(householdID)
	if err != nil {
		return calc.MonthReport{}, err
	}
	incomes, err := s.store.ListIncomes(householdID, month)
	if err != nil {
		return calc.MonthReport{}, err
	}
	return calc.BuildMonthReport(month, members, sections, categories, expenses, splits, incomes), nil
}

// expenseContext returns the members, sections and categories needed to render
// an expense row.
func (s *Server) expenseContext(householdID int64) (web.ExpensesVM, error) {
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
	return web.ExpensesVM{Members: members, Sections: sections, Categories: categories}, nil
}

// parseID parses a decimal id, returning 0 on failure.
func parseID(s string) int64 {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// hxRefresh instructs htmx to perform a full page refresh.
func hxRefresh(w http.ResponseWriter) {
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
