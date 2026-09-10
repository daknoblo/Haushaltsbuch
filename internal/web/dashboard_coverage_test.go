package web

import (
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
