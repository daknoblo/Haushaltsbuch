package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// PlausibilityVM shows a point-in-time check, never an automatically saved report.
type PlausibilityVM struct {
	Year            int
	Result          *calc.PlausibilityResult
	Members         []store.Member
	Plans, Payments int
}

func (vm PlausibilityVM) scope(ctx context.Context, id int64) string {
	for _, m := range vm.Members {
		if m.ID == id {
			return m.Name
		}
	}
	return T(ctx, "dash.viewHousehold")
}

func (s *Server) handlePlausibility(w http.ResponseWriter, r *http.Request) {
	nav, err := s.buildNav(r, "settings", "/settings/plausibility", false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	now := time.Now()
	vm := PlausibilityVM{Year: now.Year()}
	if raw := r.URL.Query().Get("year"); raw != "" {
		vm.Year, err = strconv.Atoi(raw)
		if err != nil || vm.Year < 1 || vm.Year > 9999 {
			s.clientError(w, r, http.StatusBadRequest, "error.invalidInput")
			return
		}
	}
	if nav.ActiveHousehold.ID != 0 && r.URL.Query().Get("run") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		snapshot, stats, err := s.store.InspectHousehold(ctx, nav.ActiveHousehold.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		result, err := calc.CheckPlausibility(ctx, snapshot, vm.Year, now.Format("2006-01"))
		if err == nil {
			result.CheckInventory(snapshot, stats)
		}
		if err == nil && result.Calculated {
			err = checkDisplayedValues(ctx, &result, snapshot, vm.Year, now.Format("2006-01"))
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		vm.Result = &result
		vm.Members, vm.Plans, vm.Payments = snapshot.Members, len(snapshot.Bookings), len(snapshot.Bank.Transactions)
	}
	s.render(w, r, PlausibilityPage(nav, vm))
}

func checkDisplayedValues(ctx context.Context, result *calc.PlausibilityResult, snapshot store.HouseholdSnapshot, year int, current string) error {
	scopes := make([]int64, 1, 1+len(snapshot.Members))
	for _, member := range snapshot.Members {
		scopes = append(scopes, member.ID)
	}
	for _, basis := range []calc.Basis{calc.BasisPlan, calc.BasisActual, calc.BasisHybrid} {
		data := calc.WithBankLedger(calc.FromSnapshot(snapshot), snapshot.Bank, basis, current)
		for month := 1; month <= 12; month++ {
			anchor := fmt.Sprintf("%04d-%02d", year, month)
			report := calc.BuildMonthReport(data, anchor, calc.Everyone)
			f := calc.Finding{Code: "render", Basis: basis, Month: anchor}
			expected := map[string]string{
				T(ctx, "overview.income"):   referenceMoney(report.IncomeCents),
				T(ctx, "overview.expenses"): referenceMoney(report.ExpenseCents),
				T(ctx, "overview.balance"):  referenceMoney(report.BalanceCents),
			}
			if err := checkRenderedStats(ctx, result, f, overviewTiles(Nav{Month: anchor}, report), expected); err != nil {
				return err
			}
			for _, scope := range scopes {
				if err := ctx.Err(); err != nil {
					return err
				}
				vm := dashboardFromData(ctx, data, anchor, "1m", scope, calc.GroupCategory)
				expected := calc.ReferenceMetrics(snapshot, []string{anchor}, current, basis, scope)
				if err := checkHeadline(ctx, result, f, vm, expected); err != nil {
					return err
				}
			}
		}
		for _, scope := range scopes {
			for _, period := range []string{"3m", "6m", periodYear} {
				if err := ctx.Err(); err != nil {
					return err
				}
				anchor := fmt.Sprintf("%04d-06", year)
				vm := dashboardFromData(ctx, data, anchor, period, scope, calc.GroupCategory)
				expected := calc.ReferenceMetrics(snapshot, rangeMonths(period, anchor), current, basis, scope)
				if err := checkHeadline(ctx, result, calc.Finding{Code: "render", Basis: basis, Month: anchor}, vm, expected); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkHeadline(ctx context.Context, result *calc.PlausibilityResult, f calc.Finding, vm DashboardVM, reference calc.MetricReference) error {
	f.Member = vm.ViewMember
	income, household, own := "—", "—", "—"
	result.CompareText(f, strings.Join(reference.Months, ","), strings.Join(vm.RecordedMonths, ","))
	result.Compare(f, reference.Report.ExpenseCents, vm.Report.ExpenseCents)
	result.Compare(f, reference.Report.BalanceCents, vm.Report.BalanceCents)
	if len(reference.Months) > 0 {
		income = referenceMoney(reference.Report.IncomeCents)
		household = referenceMoney(reference.Household.FixedCents())
		own = referenceMoney(reference.Report.FixedCents())
	}
	rate := "—"
	if reference.Report.IncomeCents > 0 {
		value := float64(reference.Report.SavingCents()+reference.Report.BalanceCents) / float64(reference.Report.IncomeCents) * 100
		rate = strings.ReplaceAll(strconv.FormatFloat(value, 'f', 1, 64), ".", ",") + " %"
	}
	expected := map[string]string{
		T(ctx, "overview.income"): income, T(ctx, "dash.fixedCosts"): household,
		T(ctx, "dash.ownFixedCosts"): own, T(ctx, "dash.savingsRate"): rate,
	}
	return checkRenderedStats(ctx, result, f, headlineTiles(vm), expected)
}

// referenceMoney independently formats even aggregates larger than one input.
func referenceMoney(cents int64) string {
	digits := strconv.FormatInt(cents, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign = "-"
		digits = digits[1:]
	}
	for len(digits) < 3 {
		digits = "0" + digits
	}
	whole, fraction := digits[:len(digits)-2], digits[len(digits)-2:]
	for at := len(whole) - 3; at > 0; at -= 3 {
		whole = whole[:at] + "." + whole[at:]
	}
	return sign + whole + "," + fraction + " €"
}

func checkRenderedStats(ctx context.Context, result *calc.PlausibilityResult, f calc.Finding, component templ.Component, expected map[string]string) error {
	var body bytes.Buffer
	if err := component.Render(ctx, &body); err != nil {
		return err
	}
	document, err := html.Parse(&body)
	if err != nil {
		return err
	}
	actual := make(map[string]string)
	counts := make(map[string]int64)
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var out strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			out.WriteString(text(child))
		}
		return out.String()
	}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		for _, attr := range n.Attr {
			if attr.Key == "data-stat" {
				actual[attr.Val] = text(n)
				counts[attr.Val]++
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	for name, want := range expected {
		f.Name = name
		result.Compare(f, 1, counts[name])
		result.CompareText(f, want, actual[name])
	}
	return nil
}

func plausibilityKey(code string) string {
	switch code {
	case "month":
		return "check.month"
	case "allocation":
		return "check.allocation"
	case "period":
		return "check.period"
	case "matrix":
		return "check.matrix"
	case "chart":
		return "check.chart"
	case "settlement":
		return "check.settlement"
	case "costs":
		return "check.costs"
	case "inventory":
		return "check.inventory"
	case "render":
		return "check.render"
	case "reference":
		return "check.reference"
	case "date":
		return "check.date"
	case "draft":
		return "check.draft"
	case "incomplete":
		return "check.incomplete"
	case "payer":
		return "check.payer"
	case "duplicatePlan":
		return "check.duplicatePlan"
	case "overlapPlan":
		return "check.overlapPlan"
	case "duplicatePayment":
		return "check.duplicatePayment"
	case "defaultAccount":
		return "check.defaultAccount"
	case "fingerprint":
		return "check.fingerprint"
	case "closedPending":
		return "check.closedPending"
	case "excluded":
		return "check.excluded"
	case "paymentAllocation":
		return "check.paymentAllocation"
	default:
		return "check.data"
	}
}
