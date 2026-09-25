package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestMissingIncomeKeepsDashboardSectionsAndExpensePlanning(t *testing.T) {
	srv, handler, hh, _, members := incomeCoverageFixture(t)
	saving := newExpenseBooking(t, srv, hh.ID)
	saving.Name, saving.AmountCents, saving.BudgetClass = "Planned savings", 12345, store.ClassSaving
	saving.StartsOn, saving.EndsOn = "2026-09-01", "2026-12-31"
	tag, err := srv.store.CreateTag(t.Context(), hh.ID, "Future plan", "#14b8a6")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SaveBooking(t.Context(), saving, []store.SplitInput{{MemberID: members[0].ID}}, []int64{tag.ID}); err != nil {
		t.Fatal(err)
	}
	data, err := srv.loadHouseholdData(t.Context(), hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []int64{calc.Everyone, members[0].ID, members[1].ID} {
		for _, period := range []string{"1m", "3m", periodYear} {
			vm, err := srv.buildDashboardVM(t.Context(), hh.ID, "2026-10", period, member, "")
			if err != nil {
				t.Fatal(err)
			}
			months := rangeMonths(period, "2026-10")
			recorded := calc.MonthsWithIncome(data, months, member)
			basis := recorded
			if len(recorded) == 0 {
				basis = months
			}
			if !reflect.DeepEqual(vm.ExpenseReport, calc.PeriodReport(data, basis, member)) {
				t.Errorf("member=%d period=%s: expense basis is incorrect", member, period)
			}
			if !reflect.DeepEqual(vm.Report, calc.PeriodReport(data, recorded, member)) {
				t.Error("expense planning must not change income-based metrics")
			}
			if !reflect.DeepEqual(vm.FixedTop, calc.FixedCosts(data, basis, member, fixedCostTop)) {
				t.Error("fixed-cost ranking uses a different period")
			}
			if !vm.HasRecordedIncome() {
				var out bytes.Buffer
				if err := savingsCard(vm).Render(t.Context(), &out); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out.String(), FormatEUR(vm.ExpenseReport.SavingCents())) ||
					!strings.Contains(out.String(), "—") ||
					!strings.Contains(out.String(), T(t.Context(), "dash.savingsRate")) {
					t.Error("savings card lost planned amounts or unavailable metrics")
				}
				if !vm.Rule.Empty() || !vm.Sankey.Empty() {
					t.Error("missing income must not produce fictitious income-based diagrams")
				}
			}
		}
	}
	body := get(t, handler, "/dashboard?m=2026-10&p=1m").Body.String()
	for _, key := range []string{"dash.fixedCosts", "dash.savingsRate", "dash.rule503020", "dash.flow", "dash.topCategories", "dash.byTag"} {
		if !strings.Contains(body, T(t.Context(), key)) {
			t.Errorf("missing section %s without income", key)
		}
	}
	for _, value := range []string{"Future plan", "123,45 €", "9.999,00 €", T(t.Context(), "dash.incomeMetricsMissing")} {
		if !strings.Contains(body, value) {
			t.Errorf("missing planning data or explanation %q", value)
		}
	}
}

func incomeCoverageFixture(t *testing.T) (*Server, http.Handler, store.Household, store.Booking, []store.Member) {
	t.Helper()
	srv, handler, household := newTestServer(t)
	members, err := srv.store.ListMembers(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	expense := newExpenseBooking(t, srv, household.ID)
	expense.AmountCents = 271300
	expense.StartsOn, expense.EndsOn = "2026-01-01", "2026-12-31"
	expense.PayerMemberID = &members[1].ID
	expense.Settle = true
	if err := srv.store.SaveBooking(t.Context(), expense, []store.SplitInput{{MemberID: members[0].ID}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CreateOverride(t.Context(), household.ID, store.BookingOverride{
		BookingID: expense.ID, StartsOn: "2026-09-01", AmountCents: 999900,
	}); err != nil {
		t.Fatal(err)
	}
	categories, err := srv.store.ListCategories(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	income := expense
	income.ID, income.CategoryID = 0, defaultCategory(categories, store.DirIncome)
	income.Name, income.Direction, income.Frequency = "Gehalt", store.DirIncome, store.FreqOnce
	income.AmountCents, income.EndsOn, income.Settle = 400000, "", false
	income.PayerMemberID = &members[0].ID
	for month := 1; month <= 8; month++ {
		income.StartsOn = fmt.Sprintf("2026-%02d-01", month)
		if _, err := srv.store.CreateBooking(t.Context(), income, []store.SplitInput{{MemberID: members[0].ID}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	return srv, handler, household, income, members
}

func TestDashboardAveragesOnlyMonthsWithIncome(t *testing.T) {
	srv, handler, household, _, members := incomeCoverageFixture(t)
	vm, err := srv.buildDashboardVM(t.Context(), household.ID, "2026-09", periodYear, members[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(vm.RecordedMonths) != 8 || vm.Report.IncomeCents != 400000 ||
		vm.Report.FixedCents() != 271300 || vm.Report.BalanceCents != 128700 {
		t.Fatalf("eight recorded salaries must not be divided by twelve: %+v", vm.Report)
	}
	if vm.HouseholdReport.FixedCents() != 271300 {
		t.Error("household comparison uses a different calculation period")
	}
	if len(vm.Trend) != 12 || len(vm.Chart.Line) != 8 ||
		vm.Matrix.Expense.Cents[8] != 999900 || vm.Matrix.Surplus.Active[8] {
		t.Error("future costs must remain planning, not a surplus deficit")
	}
	data, err := srv.loadHouseholdData(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantSettlement := calc.SettlementTotal(data, elapsedYearMonths("2026-09", NormalizeMonth("")))
	if !reflect.DeepEqual(vm.Settlement, wantSettlement) {
		t.Error("income coverage must not remove payable costs from settlement")
	}
	if !reflect.DeepEqual(vm.Costs, calc.CostsTotal(data, elapsedYearMonths("2026-09", NormalizeMonth("")))) {
		t.Error("income coverage must not hide carried costs")
	}
	body := get(t, handler, fmt.Sprintf("/dashboard?m=2026-09&p=12m&view=%d", members[0].ID)).Body.String()
	if !strings.Contains(body, "8 von 12 Monaten") || !strings.Contains(body, "4.000,00 €") ||
		strings.Contains(body, `class="chart-value"`) && strings.Contains(body, ">-9.999 €</text>") {
		t.Error("rendered dashboard does not explain/use the recorded period")
	}
}

func TestDashboardDistinguishesMissingIncomeAndExplicitZero(t *testing.T) {
	srv, _, household, income, members := incomeCoverageFixture(t)
	vm, err := srv.buildDashboardVM(t.Context(), household.ID, "2026-09", "1m", members[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if vm.HasRecordedIncome() || vm.ReportMoney(0) != "—" || len(vm.Chart.Line) != 0 {
		t.Fatal("no income entry must be shown as unavailable, not zero")
	}
	income.StartsOn, income.AmountCents = "2026-09-01", 0
	if _, err := srv.store.CreateBooking(t.Context(), income, []store.SplitInput{{MemberID: members[0].ID}}, nil); err != nil {
		t.Fatal(err)
	}
	vm, err = srv.buildDashboardVM(t.Context(), household.ID, "2026-09", "1m", members[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !vm.HasRecordedIncome() || vm.ReportMoney(vm.Report.IncomeCents) != "0,00 €" ||
		len(vm.Chart.Line) != 1 || vm.Chart.Line[0].Cents != -999900 {
		t.Fatal("explicit zero must count and retain the actual deficit")
	}
	if IncomeRate(vm.Report.IncomeCents, vm.Report.SavingsRate()) != "—" {
		t.Fatal("a percentage with zero income is undefined")
	}
	other, err := srv.buildDashboardVM(t.Context(), household.ID, "2026-09", periodYear, members[1].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if other.HasRecordedIncome() {
		t.Error("another member's income made the selected person appear complete")
	}
}

func TestMissingIncomeMatrixTotalIsUnavailable(t *testing.T) {
	if got := MatrixTotal(calc.MatrixRow{Gain: true}); got != "—" {
		t.Errorf("unknown income/surplus total = %q", got)
	}
	if got := MatrixTotal(calc.MatrixRow{Gain: true, ActiveMonths: 1}); got != "0 €" {
		t.Errorf("explicit zero total = %q", got)
	}
}

func TestStatisticsPDFKeepsDashboardScopeAndPeriod(t *testing.T) {
	srv, handler, household, _, members := incomeCoverageFixture(t)
	vm, err := srv.buildDashboardVM(t.Context(), household.ID, "2026-08", "1m", members[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	target := vm.StatisticsExportURL("2026-08")
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("m") != "2026-08" || u.Query().Get("p") != "1m" ||
		u.Query().Get("view") != strconv.FormatInt(members[0].ID, 10) {
		t.Fatalf("export lost dashboard controls: %s", target)
	}
	w := get(t, handler, target)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(w.Body.String(), "%PDF") {
		t.Fatalf("export failed: %d %s", w.Code, w.Body.String())
	}
}
