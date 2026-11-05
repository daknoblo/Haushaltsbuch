package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

type statusBankImporter struct {
	testBankImporter
	identity bankimport.Identity
}

func (i *statusBankImporter) Environment() []bankimport.EnvironmentVariable {
	return i.identity.Environment()
}

func TestImportEnvironmentStatusNeverDisclosesValues(t *testing.T) {
	srv, handler, _ := newTestServer(t)
	mock := &statusBankImporter{identity: bankimport.Identity{
		ResourceID: "private-resource-value", TenantID: " ", ClientSecret: "private-secret-value",
	}}
	srv.SetBankImporter(mock)
	for _, lang := range []i18n.Lang{i18n.German, i18n.English} {
		r := httptest.NewRequest(http.MethodGet, "/settings", nil)
		r.Header.Set("Accept-Language", string(lang))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		body := w.Body.String()
		if w.Code != http.StatusOK {
			t.Fatalf("settings: %d", w.Code)
		}
		if strings.Contains(body, mock.identity.ResourceID) || strings.Contains(body, mock.identity.ClientSecret) {
			t.Fatal("environment value leaked into HTML")
		}
		if strings.Count(body, `data-env-variable=`) != 4 ||
			strings.Count(body, `data-env-set="true"`) != 2 || strings.Count(body, `data-env-set="false"`) != 2 {
			t.Fatal("missing or incorrect environment statuses")
		}
		if !strings.Contains(body, `class="badge badge-ok whitespace-nowrap" data-env-set="true">`+i18n.T(lang, "settings.envSet")) ||
			!strings.Contains(body, `class="badge badge-danger whitespace-nowrap" data-env-set="false">`+i18n.T(lang, "settings.envMissing")) {
			t.Fatal("environment status must include color and localized text")
		}
		last := -1
		for _, name := range []string{"AZURE_RESOURCE_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"} {
			at := strings.Index(body, `data-env-variable="`+name+`"`)
			if at <= last {
				t.Fatal("environment order changed")
			}
			last = at
		}
	}
	srv.SetBankImporter(nil)
	body := get(t, handler, "/settings").Body.String()
	if strings.Count(body, `data-env-set="false"`) != 4 {
		t.Fatal("disabled configuration must show four missing variables")
	}
}

func TestImportEnvironmentStatusAlwaysVisible(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		vm := ImportAISettingsVM{
			Enabled:     enabled,
			Environment: (bankimport.Identity{ResourceID: "synthetic-resource"}).Environment(),
		}
		var rendered bytes.Buffer
		if err := importAISettings(vm).Render(t.Context(), &rendered); err != nil {
			t.Fatal(err)
		}
		body := rendered.String()
		if strings.Contains(body, "<details") || strings.Contains(body, "<summary") {
			t.Fatal("environment status must not require opening a disclosure")
		}
		if strings.Count(body, `data-env-variable=`) != 4 ||
			!strings.Contains(body, `aria-labelledby="azure-environment-title"`) ||
			!strings.Contains(body, `<h3 id="azure-environment-title"`) {
			t.Fatal("always-visible environment status section is incomplete")
		}
	}
}

// compactUIFixture is deliberately synthetic and runs only against t.TempDir.
func compactUIFixture(t *testing.T) (*Server, http.Handler, store.Household) {
	t.Helper()
	srv, handler, hh := newTestServer(t)
	ctx := t.Context()
	members, err := srv.store.ListMembers(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Alex", "Sam"} {
		if err := srv.store.UpdateMember(ctx, hh.ID, members[i].ID, name, members[i].Color); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.store.RenameHousehold(ctx, hh.ID, "Demohaushalt"); err != nil {
		t.Fatal(err)
	}
	ids := plausibilityBook(t, srv, hh)
	for _, name := range []string{"Gehalt", "Streaming"} {
		b, err := srv.store.GetBooking(ctx, hh.ID, ids[name])
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 10; i++ {
			b.ID = 0
			b.Name = fmt.Sprintf("Test %s %02d", name, i)
			if _, err := srv.store.CreateBooking(ctx, b, []store.SplitInput{{MemberID: members[0].ID}}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	long, err := srv.store.GetBooking(ctx, hh.ID, ids["Versicherung"])
	if err != nil {
		t.Fatal(err)
	}
	long.Name = "Synthetische Familienversicherung mit einer besonders langen Buchungsbezeichnung"
	if err := srv.store.SaveBooking(ctx, long, []store.SplitInput{{MemberID: members[0].ID}}, nil); err != nil {
		t.Fatal(err)
	}
	accounts, err := srv.store.ListBankAccounts(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"Demo-Girokonto", "Demo-Partnerkonto"} {
		if err := srv.store.RenameBankAccount(ctx, hh.ID, accounts[i].ID, name); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := srv.store.GetBooking(ctx, hh.ID, ids["Miete"])
	if err != nil {
		t.Fatal(err)
	}
	base := store.BankTransaction{BookedOn: "2020-01-03", Direction: store.DirExpense, AmountCents: 100000,
		Name: "Miete", Reference: "Synthetische Zahlung", CategoryID: plan.CategoryID, PlanID: plan.ID,
		Decision: "pending", CostNature: store.CostFix, BudgetClass: store.ClassNeed, Settle: true}
	payments := make([]store.BankTransaction, 0, 6)
	payments = append(payments, base, base, base, base, base)
	payments[2].Name, payments[2].AmountCents, payments[2].PlanID = "Synthetischer Einkauf mit langem Namen und langem Verwendungszweck", 12345, 0
	payments[2].Reference = strings.Repeat("Synthetischer Verwendungszweck ", 8)
	payments[3].Name, payments[3].AmountCents, payments[3].PlanID = "Ausgeschlossene Testzeile", 990, 0
	payments[4].Name, payments[4].AmountCents, payments[4].PlanID = "Interner Transfer", 15000, 0
	salary, err := srv.store.GetBooking(ctx, hh.ID, ids["Gehalt"])
	if err != nil {
		t.Fatal(err)
	}
	income := base
	income.Direction, income.CategoryID, income.PlanID = store.DirIncome, salary.CategoryID, salary.ID
	income.Name, income.AmountCents = "Testgehalt", 345000
	payments = append(payments, income)
	if err := srv.store.CreateBankImport(ctx, hh.ID,
		store.BankImport{AccountID: accounts[0].ID, Digest: strings.Repeat("a", 64), Filename: "synthetic-statement.pdf"},
		payments); err != nil {
		t.Fatal(err)
	}
	ledger, err := srv.store.BankLedger(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range ledger.Transactions {
		switch i {
		case 0, 5:
			p.Decision = "accepted"
			p.Splits = []store.BankSplit{{MemberID: members[0].ID, Cents: p.AmountCents}}
		case 3:
			p.Decision = "ignored"
		case 4:
			p.Decision, p.TransferAccountID = "transfer", accounts[1].ID
		default:
			continue
		}
		if err := srv.store.ReviewBankTransaction(ctx, hh.ID, p, false); err != nil {
			t.Fatal(err)
		}
	}
	srv.SetBankImporter(&testBankImporter{})
	if err := srv.store.SetAIDeployment(ctx, "synthetic-router"); err != nil {
		t.Fatal(err)
	}
	return srv, handler, hh
}

func TestCompactImportSummaryKeepsReviewActions(t *testing.T) {
	srv, handler, hh := compactUIFixture(t)
	body := get(t, handler, "/imports?m=2020-01").Body.String()
	if strings.Count(body, `class="import-payment" data-bank-payment=`) != 6 ||
		strings.Count(body, `class="import-payment-summary"`) != 6 {
		t.Fatal("statement rows must use compact expandable summaries")
	}
	if strings.Contains(body, "KI-Vorschläge können") || !strings.Contains(body, `data-import-upload-fields`) {
		t.Fatal("general AI warning remains or compact upload is missing")
	}
	if !strings.Contains(body, "+3.450,00") || !strings.Contains(body, "-1.000,00") ||
		!strings.Contains(body, "Dublette?") {
		t.Fatal("summary amounts or warning missing")
	}
	for _, decision := range []string{"accepted", "pending", "ignored", "transfer"} {
		if !strings.Contains(body, `name="decision" value="`+decision+`"`) {
			t.Fatalf("lost review action %s", decision)
		}
	}
	data, err := srv.loadHouseholdData(t.Context(), hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := srv.store.BankLedger(t.Context(), hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	vm := ImportVM{Rows: importRows(t.Context(), ledger, data, "2020-01")}
	for decision, want := range map[string]int{"pending": 2, "accepted": 2, "ignored": 1, "transfer": 1} {
		if vm.DecisionCount(decision) != want {
			t.Fatalf("incorrect %s count", decision)
		}
	}
}

func TestCategoryUsageKeepsFullCounts(t *testing.T) {
	for _, count := range []int{0, 1, 10, 100, 1000} {
		var rendered bytes.Buffer
		if err := CategoryRowView(store.Category{ID: 1, Name: "Example", Classification: store.DirExpense}, count, IconKeys()).Render(t.Context(), &rendered); err != nil {
			t.Fatal(err)
		}
		body := rendered.String()
		if !strings.Contains(body, "cat-usage") || !strings.Contains(body, strconv.Itoa(count)) {
			t.Fatalf("missing usage count %d", count)
		}
	}

}

func TestBookingSummaryKeepsCategoryAndFullName(t *testing.T) {
	row := BookingRow{
		Booking: store.Booking{ID: 1, Name: "Long synthetic booking name", Direction: store.DirExpense,
			AmountCents: 12345, Frequency: store.FreqMonthly, Interval: 1},
		Category: store.Category{ID: 1, Name: "Example category"}, Month: "2020-01",
	}
	var rendered bytes.Buffer
	if err := bookingRow(row).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	body := rendered.String()
	for _, expected := range []string{`class="booking-description`, `title="Long synthetic booking name"`, `title="Example category"`, "123,45"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("booking summary lost %q", expected)
		}
	}
}
