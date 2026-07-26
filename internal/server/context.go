package server

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

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

// householdData bundles the household-scoped data that every month report
// needs. Loading it once allows several months to be aggregated without
// re-querying the same rows.
type householdData struct {
	members    []store.Member
	sections   []store.Section
	categories []store.Category
	expenses   []store.Expense
	splits     map[int64][]store.ExpenseSplit
}

// loadHouseholdData reads members, sections, categories, expenses and splits of
// a household.
func (s *Server) loadHouseholdData(householdID int64) (householdData, error) {
	var d householdData
	var err error
	if d.members, err = s.store.ListMembers(householdID); err != nil {
		return householdData{}, err
	}
	if d.sections, err = s.store.ListSections(householdID); err != nil {
		return householdData{}, err
	}
	if d.categories, err = s.store.ListCategories(householdID); err != nil {
		return householdData{}, err
	}
	if d.expenses, err = s.store.ListExpenses(householdID); err != nil {
		return householdData{}, err
	}
	if d.splits, err = s.store.ListSplitsForHousehold(householdID); err != nil {
		return householdData{}, err
	}
	return d, nil
}

// report aggregates the already loaded household data for a single month.
func (d householdData) report(month string, incomes []store.Income) calc.MonthReport {
	return calc.BuildMonthReport(month, d.members, d.sections, d.categories,
		d.expenses, d.splits, incomes)
}

// buildMonthReport loads all data for a household/month and aggregates it.
func (s *Server) buildMonthReport(householdID int64, month string) (calc.MonthReport, error) {
	data, err := s.loadHouseholdData(householdID)
	if err != nil {
		return calc.MonthReport{}, err
	}
	incomes, err := s.store.ListIncomes(householdID, month)
	if err != nil {
		return calc.MonthReport{}, err
	}
	return data.report(month, incomes), nil
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
	if err != nil || id < 0 {
		return 0
	}
	return id
}

// maxNameLen bounds free-text names so that a single request cannot store an
// unbounded amount of data.
const maxNameLen = 120

// cleanName trims a user supplied name and truncates it to maxNameLen runes.
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxNameLen {
		s = string(r[:maxNameLen])
	}
	return s
}

// hexColorRe matches the "#rrggbb" values produced by <input type="color">.
var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// cleanColor returns c when it is a valid hex color and "" otherwise.
func cleanColor(c string) string {
	c = strings.TrimSpace(c)
	if hexColorRe.MatchString(c) {
		return strings.ToLower(c)
	}
	return ""
}

// cleanMonth returns ym when it is a valid "YYYY-MM" value and "" otherwise.
// Empty means "no bound" for the active_from/active_until fields.
func cleanMonth(ym string) string {
	ym = strings.TrimSpace(ym)
	if ym == "" || !web.ValidMonth(ym) {
		return ""
	}
	return ym
}

// cleanDate returns d when it is a valid "YYYY-MM-DD" value and "" otherwise.
func cleanDate(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return ""
	}
	return d
}

// hxRefresh instructs htmx to perform a full page refresh.
func hxRefresh(w http.ResponseWriter) {
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
