package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func quickReviewForm(t *testing.T, row ImportRow, vm ImportVM, household int64) url.Values {
	t.Helper()
	token, err := row.ReviewToken()
	if err != nil {
		t.Fatal(err)
	}
	p := row.Payment
	form := url.Values{
		"household": {strconv.FormatInt(household, 10)}, "upload_generation": {strconv.FormatUint(vm.UploadGeneration, 10)},
		"quick": {"yes"}, "review_token": {token}, "name": {p.Name}, "reference": {p.Reference}, "date": {p.BookedOn},
		"amount": {FormatCents(p.AmountCents)}, "direction": {string(p.Direction)}, "category": {strconv.FormatInt(p.CategoryID, 10)},
		"plan": {strconv.FormatInt(p.PlanID, 10)}, "nature": {string(p.CostNature)}, "class": {string(p.BudgetClass)},
		"allocation": {row.Allocation}, "history_id": {strconv.FormatInt(row.HistoryID, 10)},
		"transfer_account": {strconv.FormatInt(p.TransferAccountID, 10)},
	}
	if p.Settle {
		form.Set("settle", "yes")
	}
	return form
}

func quickReviewFixture(t *testing.T) (*Server, http.Handler, store.Household, ImportVM) {
	t.Helper()
	srv, handler, household := newTestServer(t)
	accounts, err := srv.store.ListBankAccounts(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("Datum;Name;Betrag\n15.01.2020;REWE Example;-12,34\n16.01.2020;REWE Example;-23,45\n17.01.2020;Netflix;-13,99\n18.01.2020;Unrecognized recipient;-17,34\n")
	if response := queueUpload(t, handler, household.ID, accounts[0].ID, "synthetic.csv", data, nil); response.Code != http.StatusCreated {
		t.Fatalf("upload: %d", response.Code)
	}
	vm, err := srv.buildImportVM(t.Context(), household.ID, "2020-01")
	if err != nil {
		t.Fatal(err)
	}
	return srv, handler, household, vm
}

func TestLocalSuggestionsAreVisibleReadOnlyAndDirectlyAcceptable(t *testing.T) {
	srv, handler, household, vm := quickReviewFixture(t)
	before, err := srv.store.ReadHousehold(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	page := get(t, handler, "/imports?m=2020-01")
	if !strings.Contains(page.Body.String(), `class="import-labels"`) || !strings.Contains(page.Body.String(), `data-bank-quick`) ||
		!strings.Contains(page.Body.String(), "Lebensmittel") || !strings.Contains(page.Body.String(), T(t.Context(), "import.suggestionRule")) {
		t.Fatal("collapsed review labels or direct actions missing")
	}
	after, err := srv.store.ReadHousehold(t.Context(), household.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("viewing suggestions changed stored data")
	}
	var groceries []ImportRow
	for _, row := range vm.Rows {
		switch row.Payment.Name {
		case "Unrecognized recipient":
			if row.CanQuick || row.Payment.CategoryID != 0 {
				t.Fatal("unknown payment was silently categorized")
			}
		case "Netflix":
			if !row.CanQuick || row.Payment.CostNature != store.CostFix || row.Payment.BudgetClass != store.ClassWant {
				t.Fatal("subscription suggestion incomplete")
			}
		case "REWE Example":
			if !row.CanQuick || row.Category != "Lebensmittel" || row.Allocation != "owner" {
				t.Fatalf("groceries suggestion: %+v", row)
			}
			groceries = append(groceries, row)
		}
	}
	for _, row := range groceries {
		form := quickReviewForm(t, row, vm, household.ID)
		response := postMappingJSON(t, handler, row.URL(), form)
		if response.Code != http.StatusOK {
			t.Fatalf("direct acceptance: %d %s", response.Code, response.Body)
		}
		var result struct{ ID, HTML string }
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.ID != strconv.FormatInt(row.Payment.ID, 10) || !strings.Contains(result.HTML, T(t.Context(), "import.accepted")) || strings.Contains(result.HTML, `data-bank-quick`) {
			t.Fatal("accepted row not returned")
		}
		if response := postMappingJSON(t, handler, row.URL(), form); response.Code != http.StatusConflict {
			t.Fatal("direct acceptance replayed")
		}
	}
	ledger, err := srv.store.BankLedger(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	accepted := 0
	for _, payment := range ledger.Transactions {
		if payment.Decision == "accepted" {
			accepted++
			if len(payment.Splits) != 1 || payment.Splits[0].Cents != payment.AmountCents {
				t.Fatal("visible owner allocation was not saved")
			}
		} else if payment.CategoryID != 0 {
			t.Fatal("other pending suggestions were materialized")
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted %d payments, want 2", accepted)
	}
}

func TestQuickReviewRejectsEditedStaleAndForeignRequests(t *testing.T) {
	srv, handler, household, vm := quickReviewFixture(t)
	row := vm.Rows[len(vm.Rows)-1]
	form := quickReviewForm(t, row, vm, household.ID)
	form.Set("amount", "99,99")
	if response := postMappingJSON(t, handler, row.URL(), form); response.Code != http.StatusConflict {
		t.Fatal("hidden edited amount accepted as the displayed suggestion")
	}
	form = quickReviewForm(t, row, vm, household.ID)
	original := row.original
	original.AmountCents++
	if err := srv.store.ReviewBankTransaction(t.Context(), household.ID, original, false); err != nil {
		t.Fatal(err)
	}
	if response := postMappingJSON(t, handler, row.URL(), form); response.Code != http.StatusConflict {
		t.Fatal("stale payment accepted")
	}
	other, err := srv.store.CreateHouseholdSeeded(t.Context(), "Other")
	if err != nil {
		t.Fatal(err)
	}
	form.Set("household", strconv.FormatInt(other.ID, 10))
	if response := postMappingJSON(t, handler, row.URL(), form); response.Code != http.StatusConflict {
		t.Fatal("foreign household request accepted")
	}
	form.Set("household", strconv.FormatInt(household.ID, 10))
	if response := post(t, handler, "/settings/reset-bookings", nil); response.Code != http.StatusNoContent {
		t.Fatal("reset failed")
	}
	if response := postMappingJSON(t, handler, row.URL(), form); response.Code != http.StatusConflict {
		t.Fatal("stale generation accepted")
	}
}

func TestQuickReviewChecksCurrentPlanAllocation(t *testing.T) {
	srv, handler, household := newTestServer(t)
	ctx := t.Context()
	members, err := srv.store.ListMembers(ctx, household.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan := newExpenseBooking(t, srv, household.ID)
	plan.Name, plan.AmountCents = "Synthetic rent", 10001
	plan.StartsOn, plan.EndsOn = "2020-01-01", "2020-12-31"
	plan.Frequency, plan.Interval, plan.SplitMode = store.FreqMonthly, 1, store.SplitEqual
	if err := srv.store.SaveBooking(ctx, plan, []store.SplitInput{{MemberID: members[0].ID}, {MemberID: members[1].ID}}, nil); err != nil {
		t.Fatal(err)
	}
	accounts, err := srv.store.ListBankAccounts(ctx, household.ID)
	if err != nil {
		t.Fatal(err)
	}
	response := queueUpload(t, handler, household.ID, accounts[0].ID, "plan.csv", []byte("Datum;Name;Betrag\n15.01.2020;Synthetic rent;-100,01\n"), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload: %d", response.Code)
	}
	vm, err := srv.buildImportVM(ctx, household.ID, "2020-01")
	if err != nil {
		t.Fatal(err)
	}
	row := vm.Rows[0]
	if !row.CanQuick || len(row.Payment.Splits) != 2 {
		t.Fatal("complete plan not suggested")
	}
	form := quickReviewForm(t, row, vm, household.ID)
	if err := srv.store.SaveBooking(ctx, plan, []store.SplitInput{{MemberID: members[1].ID}}, nil); err != nil {
		t.Fatal(err)
	}
	if response := postMappingJSON(t, handler, row.URL(), form); response.Code != http.StatusConflict {
		t.Fatal("changed plan allocation silently accepted")
	}
	vm, err = srv.buildImportVM(ctx, household.ID, "2020-01")
	if err != nil {
		t.Fatal(err)
	}
	if response := postMappingJSON(t, handler, row.URL(), quickReviewForm(t, vm.Rows[0], vm, household.ID)); response.Code != http.StatusOK {
		t.Fatalf("fresh allocation rejected: %d %s", response.Code, response.Body)
	}
	ledger, err := srv.store.BankLedger(ctx, household.ID)
	if err != nil || len(ledger.Transactions[0].Splits) != 1 || ledger.Transactions[0].Splits[0].MemberID != members[1].ID {
		t.Fatal("accepted allocation differs from displayed allocation")
	}
}

func TestQuickReviewLeavesDuplicatesTransfersAndClosedMonthsForDetails(t *testing.T) {
	srv, _, household, _ := quickReviewFixture(t)
	snapshot, err := srv.store.ReadHousehold(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := snapshot.Bank.Transactions[0]
	duplicate := first
	duplicate.ID = 100
	snapshot.Bank.Transactions = append(snapshot.Bank.Transactions, duplicate)
	for _, row := range importRows(t.Context(), snapshot.Bank, calc.FromSnapshot(snapshot), "2020-01") {
		if row.Payment.ID == first.ID && (!row.Duplicate || row.CanQuick) {
			t.Fatal("duplicate enabled direct acceptance")
		}
	}
	snapshot.Bank.Transactions = snapshot.Bank.Transactions[:len(snapshot.Bank.Transactions)-1]
	snapshot.Bank.Imports = append(snapshot.Bank.Imports, store.BankImport{ID: 99, AccountID: snapshot.Bank.Accounts[1].ID})
	opposite := first
	opposite.ID, opposite.ImportID, opposite.Direction = 101, 99, store.DirIncome
	snapshot.Bank.Transactions = append(snapshot.Bank.Transactions, opposite)
	for _, row := range importRows(t.Context(), snapshot.Bank, calc.FromSnapshot(snapshot), "2020-01") {
		if row.Payment.ID == first.ID && (row.TransferCandidate == "" || row.CanQuick) {
			t.Fatal("possible internal transfer enabled direct acceptance")
		}
	}
	snapshot.Bank.ClosedMonths = []string{"2020-01"}
	for _, row := range importRows(t.Context(), snapshot.Bank, calc.FromSnapshot(snapshot), "2020-01") {
		if row.CanQuick {
			t.Fatal("closed month enabled direct acceptance")
		}
	}
}

func TestLocalHistoryIsScopedAndConflictsAreNotGuessed(t *testing.T) {
	srv, _, household, vm := quickReviewFixture(t)
	row := vm.Rows[0]
	if row.Payment.Name != "Unrecognized recipient" {
		t.Fatal("fixture order changed")
	}
	categories, err := srv.store.ListCategories(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	var want, other int64
	for _, c := range categories {
		if c.Name == "Freizeit" {
			want = c.ID
		}
		if c.Name == "Lebensmittel" {
			other = c.ID
		}
	}
	snapshot, err := srv.store.ReadHousehold(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := snapshot.Bank.Transactions[0]
	base.ID = 100
	base.Name = row.Payment.Name
	base.Decision, base.CategoryID, base.CostNature, base.BudgetClass = "accepted", want, store.CostVariable, store.ClassWant
	base.Splits = []store.BankSplit{{MemberID: snapshot.Bank.Accounts[0].MemberID, Cents: base.AmountCents}}
	snapshot.Bank.Transactions = append(snapshot.Bank.Transactions, base)
	got := importRows(t.Context(), snapshot.Bank, calc.FromSnapshot(snapshot), "2020-01")
	for _, candidate := range got {
		if candidate.Payment.ID == row.Payment.ID && (!candidate.CanQuick || candidate.Suggestion != "history" || candidate.Payment.CategoryID != want) {
			t.Fatal("accepted history not reused")
		}
	}
	base.ID, base.CategoryID = 101, other
	snapshot.Bank.Transactions = append(snapshot.Bank.Transactions, base)
	got = importRows(t.Context(), snapshot.Bank, calc.FromSnapshot(snapshot), "2020-01")
	for _, candidate := range got {
		if candidate.Payment.ID == row.Payment.ID && (candidate.CanQuick || candidate.Payment.CategoryID != 0 || candidate.Suggestion != "conflict") {
			t.Fatal("conflicting history guessed")
		}
	}
}
