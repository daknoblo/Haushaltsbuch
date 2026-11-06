package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

type testBankClassifier struct {
	testBankImporter
	calls int
	reply func(bankimport.ClassificationRequest) []bankimport.ClassificationSuggestion
}

func (c *testBankClassifier) Classify(_ context.Context, _ string, input bankimport.ClassificationRequest) ([]bankimport.ClassificationSuggestion, error) {
	c.calls++
	return c.reply(input), nil
}

func analysisForm(srv *Server, household int64) url.Values {
	return url.Values{"household": {strconv.FormatInt(household, 10)}, "upload_generation": {strconv.FormatUint(srv.importPreviews.epoch(), 10)}, "month": {"2020-01"}}
}

func workflowVM(t *testing.T, srv *Server, household int64) ImportVM {
	t.Helper()
	vm, err := srv.buildImportVM(t.Context(), household, "2020-01")
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func batchForm(t *testing.T, srv *Server, household int64, operation string, rows ...ImportRow) url.Values {
	t.Helper()
	form := analysisForm(srv, household)
	form.Set("operation", operation)
	for _, row := range rows {
		id := strconv.FormatInt(row.Payment.ID, 10)
		token, err := row.ReviewToken()
		if err != nil {
			t.Fatal(err)
		}
		form.Add("payment", id)
		form.Set("token_"+id, token)
	}
	return form
}

func TestAIClassifiesOnlyUnresolvedRowsAndCachesAdvisoryMetadata(t *testing.T) {
	srv, handler, household, _ := quickReviewFixture(t)
	before := workflowVM(t, srv, household.ID)
	classifier := &testBankClassifier{}
	classifier.reply = func(input bankimport.ClassificationRequest) []bankimport.ClassificationSuggestion {
		if len(input.Transactions) != 1 || input.Transactions[0].Name != "Unrecognized recipient" {
			t.Fatalf("local matches sent to AI: %+v", input.Transactions)
		}
		category := input.Categories[0].ID
		for _, c := range input.Categories {
			if c.Direction == store.DirExpense {
				category = c.ID
				break
			}
		}
		return []bankimport.ClassificationSuggestion{{ID: input.Transactions[0].ID, CategoryID: &category, Reason: "Synthetic category evidence."}}
	}
	srv.SetBankImporter(classifier)
	if err := srv.store.SetAIDeployment(t.Context(), "synthetic-router"); err != nil {
		t.Fatal(err)
	}
	form := analysisForm(srv, household.ID)
	w := postMappingJSON(t, handler, "/imports/analyze", form)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"updated":1`) {
		t.Fatalf("analysis: %d %s", w.Code, w.Body)
	}
	after := workflowVM(t, srv, household.ID)
	if len(before.Ledger.Transactions) != len(after.Ledger.Transactions) || len(after.Data.Bookings) != len(before.Data.Bookings) {
		t.Fatal("analysis created financial rows or plans")
	}
	for _, p := range after.Ledger.Transactions {
		if p.Decision != "pending" || p.CategoryID != 0 {
			t.Fatal("analysis accepted or edited an original payment")
		}
	}
	for _, row := range after.Rows {
		if row.Payment.Name == "Unrecognized recipient" && (row.Stage != "suggested" || row.Suggestion != "ai" || !row.CanQuick || row.AIReason == "") {
			t.Fatalf("AI suggestion not visibly distinguished: %+v", row)
		}
	}
	if w := postMappingJSON(t, handler, "/imports/analyze", form); w.Code != http.StatusOK || classifier.calls != 1 {
		t.Fatal("unchanged suggestions were billed again")
	}
	snapshot, err := srv.store.Export(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 5 {
		t.Fatal("advisory backup version missing")
	}
	if err := srv.store.Import(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	restored := workflowVM(t, srv, household.ID)
	if !reflect.DeepEqual(after.Ledger, restored.Ledger) {
		t.Fatal("analysis metadata lost during restore")
	}
}

func TestAIRejectsStaleOrInventedResults(t *testing.T) {
	for _, mutation := range []string{"payment", "model", "categories", "foreign-category", "wrong-direction"} {
		t.Run(mutation, func(t *testing.T) {
			srv, handler, household, _ := quickReviewFixture(t)
			if err := srv.store.SetAIDeployment(t.Context(), "synthetic-router"); err != nil {
				t.Fatal(err)
			}
			classifier := &testBankClassifier{}
			classifier.reply = func(input bankimport.ClassificationRequest) []bankimport.ClassificationSuggestion {
				category := int64(0)
				for _, c := range input.Categories {
					if c.Direction == store.DirExpense {
						category = c.ID
						break
					}
				}
				switch mutation {
				case "payment":
					vm := workflowVM(t, srv, household.ID)
					p, _, err := findBankPayment(vm.Ledger, input.Transactions[0].ID)
					if err != nil {
						t.Fatal(err)
					}
					p.Name += " corrected"
					if err := srv.store.ReviewBankTransaction(t.Context(), household.ID, p, false); err != nil {
						t.Fatal(err)
					}
				case "model":
					if err := srv.store.SetAIDeployment(t.Context(), "different-model"); err != nil {
						t.Fatal(err)
					}
				case "categories":
					if _, err := srv.store.CreateCategory(t.Context(), household.ID, store.Category{Name: "New category", Classification: store.DirExpense}); err != nil {
						t.Fatal(err)
					}
				case "foreign-category":
					category = 999999
				case "wrong-direction":
					for _, c := range input.Categories {
						if c.Direction == store.DirIncome {
							category = c.ID
							break
						}
					}
				}
				return []bankimport.ClassificationSuggestion{{ID: input.Transactions[0].ID, CategoryID: &category, Reason: "Synthetic response."}}
			}
			srv.SetBankImporter(classifier)
			w := postMappingJSON(t, handler, "/imports/analyze", analysisForm(srv, household.ID))
			want := http.StatusConflict
			if mutation == "foreign-category" || mutation == "wrong-direction" {
				want = http.StatusBadGateway
			}
			if w.Code != want {
				t.Fatalf("status %d want %d: %s", w.Code, want, w.Body)
			}
			for _, p := range workflowVM(t, srv, household.ID).Ledger.Transactions {
				if p.Analysis != nil || p.Decision != "pending" {
					t.Fatal("invalid analysis changed payments")
				}
			}
		})
	}
}

func TestBulkAcceptanceIsExplicitAtomicAndSnapshotBound(t *testing.T) {
	srv, handler, household, vm := quickReviewFixture(t)
	var selected []ImportRow
	for _, row := range vm.Rows {
		if row.CanQuick {
			selected = append(selected, row)
		}
	}
	form := batchForm(t, srv, household.ID, "accept", selected...)
	bad := form.Encode()
	form.Set("token_"+strconv.FormatInt(selected[len(selected)-1].Payment.ID, 10), "stale")
	if w := postMappingJSON(t, handler, "/imports/review-batch", form); w.Code != http.StatusConflict {
		t.Fatalf("stale batch: %d", w.Code)
	}
	for _, p := range workflowVM(t, srv, household.ID).Ledger.Transactions {
		if p.Decision != "pending" {
			t.Fatal("failed batch partially accepted")
		}
	}
	form, err := url.ParseQuery(bad)
	if err != nil {
		t.Fatal(err)
	}
	w := postMappingJSON(t, handler, "/imports/review-batch", form)
	if w.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	after := workflowVM(t, srv, household.ID)
	var expected int64
	for _, row := range selected {
		expected += row.Payment.AmountCents
	}
	actual := calc.BuildMonthReport(calc.WithBankLedger(after.Data, after.Ledger, calc.BasisActual, "2026-01"), "2020-01", calc.Everyone)
	if actual.ExpenseCents != expected || after.DecisionCount("accepted") != len(selected) {
		t.Fatal("bulk actual totals or decisions incorrect")
	}
	if w := postMappingJSON(t, handler, "/imports/review-batch", form); w.Code != http.StatusConflict {
		t.Fatal("batch replayed")
	}
}

func TestBulkCategoryKeepsDraftSharesAndDoesNotCountDrafts(t *testing.T) {
	srv, handler, household, vm := quickReviewFixture(t)
	var source, target ImportRow
	for _, row := range vm.Rows {
		if row.Payment.Name == "REWE Example" {
			if source.Payment.ID == 0 {
				source = row
			} else {
				target = row
			}
		}
	}
	p := source.Payment
	p.Decision = "accepted"
	p.Splits = []store.BankSplit{{MemberID: vm.Data.Members[0].ID, Cents: 100}, {MemberID: vm.Data.Members[1].ID, Cents: p.AmountCents - 100}}
	if err := srv.store.ReviewBankTransaction(t.Context(), household.ID, p, false); err != nil {
		t.Fatal(err)
	}
	vm = workflowVM(t, srv, household.ID)
	for _, row := range vm.Rows {
		if row.Payment.ID == target.Payment.ID {
			target = row
		}
	}
	if target.Allocation != "history" {
		t.Fatal("history allocation not available")
	}
	form := batchForm(t, srv, household.ID, "category", target)
	form.Set("category", strconv.FormatInt(target.Payment.CategoryID, 10))
	w := postMappingJSON(t, handler, "/imports/review-batch", form)
	if w.Code != http.StatusOK {
		t.Fatalf("category: %d %s", w.Code, w.Body)
	}
	vm = workflowVM(t, srv, household.ID)
	for _, row := range vm.Rows {
		if row.Payment.ID == target.Payment.ID {
			if row.Allocation != "draft" || !reflect.DeepEqual(row.Payment.Splits, target.Payment.Splits) {
				t.Fatal("bulk category changed proposed shares")
			}
			if w := postMappingJSON(t, handler, row.URL(), quickReviewForm(t, row, vm, household.ID)); w.Code != http.StatusOK {
				t.Fatalf("accept draft: %d %s", w.Code, w.Body)
			}
		}
	}
	beforeActual := calc.BuildMonthReport(calc.WithBankLedger(vm.Data, vm.Ledger, calc.BasisActual, "2026-01"), "2020-01", calc.Everyone)
	if beforeActual.ExpenseCents != p.AmountCents {
		t.Fatal("pending draft counted as actual")
	}
	after := workflowVM(t, srv, household.ID)
	actual := calc.BuildMonthReport(calc.WithBankLedger(after.Data, after.Ledger, calc.BasisActual, "2026-01"), "2020-01", calc.Everyone)
	if actual.ExpenseCents != p.AmountCents+target.Payment.AmountCents {
		t.Fatal("accepted draft missing or counted twice")
	}
}

func TestUnknownAIResultsRemainUnresolvedWithoutAutomaticRebilling(t *testing.T) {
	srv, handler, household, _ := quickReviewFixture(t)
	classifier := &testBankClassifier{reply: func(input bankimport.ClassificationRequest) []bankimport.ClassificationSuggestion {
		return []bankimport.ClassificationSuggestion{{ID: input.Transactions[0].ID, Reason: "The reference does not identify the purchase."}}
	}}
	srv.SetBankImporter(classifier)
	if err := srv.store.SetAIDeployment(t.Context(), "synthetic-router"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if w := postMappingJSON(t, handler, "/imports/analyze", analysisForm(srv, household.ID)); w.Code != http.StatusOK {
			t.Fatalf("analysis: %d %s", w.Code, w.Body)
		}
	}
	if classifier.calls != 1 {
		t.Fatal("unchanged unknown result retried automatically")
	}
	for _, row := range workflowVM(t, srv, household.ID).Rows {
		if row.Payment.Name == "Unrecognized recipient" && (row.CanQuick || row.Stage != "needs" || row.Category != "" || row.AIReason == "") {
			t.Fatal("unknown result presented as a classification")
		}
	}
}

func TestReviewStrengthDistinguishesAccountDefaultsAndWeakSimilarity(t *testing.T) {
	_, _, _, vm := quickReviewFixture(t)
	account := vm.Ledger.Accounts[0].ID
	for i := range vm.Ledger.Accounts {
		if vm.Ledger.Accounts[i].ID == account {
			vm.Ledger.Accounts[i].Name = "Einkaufen Lebensmittel"
		}
	}
	for i := range vm.Ledger.Transactions {
		p := &vm.Ledger.Transactions[i]
		if p.Name == "REWE Example" {
			p.BookedOn, p.Reference = "2020-01-15", "Repeated generic shop reference"
		}
	}
	for _, row := range importRows(t.Context(), vm.Ledger, vm.Data, "2020-01") {
		switch row.Payment.Name {
		case "Unrecognized recipient":
			if row.Suggestion != "account" || row.Stage != "suggested" || !row.NatureDefault || !row.ClassDefault || !row.AllocationDefault {
				t.Fatalf("account fallback looks verified: %+v", row)
			}
		case "REWE Example":
			if row.SimilarCandidate == "" || !row.CanQuick || row.Stage != "suggested" {
				t.Fatal("weak similarity blocks explicit acceptance")
			}
		case "Netflix":
			if row.Stage != "ready" {
				t.Fatal("clear merchant rule not ready")
			}
		}
	}
}

func TestTransferPairRejectsAmbiguityAndChangedCounterparts(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(strconv.FormatBool(changed), func(t *testing.T) {
			srv, handler, hh := newTestServer(t)
			accounts, err := srv.store.ListBankAccounts(t.Context(), hh.ID)
			if err != nil {
				t.Fatal(err)
			}
			for i, direction := range []store.Direction{store.DirExpense, store.DirIncome} {
				p := store.BankTransaction{Name: "Synthetic transfer", BookedOn: "2020-01-15", Direction: direction, AmountCents: 555, Decision: "pending", CostNature: store.CostVariable, BudgetClass: store.ClassNeed}
				if _, err := srv.store.ReconcileBankImports(t.Context(), hh.ID, []store.BankImportBatch{{Import: store.BankImport{AccountID: accounts[i].ID, Digest: strings.Repeat("a", 64), Filename: "synthetic.csv"}, Payments: []store.BankTransaction{p}}}); err != nil {
					t.Fatal(err)
				}
			}
			vm := workflowVM(t, srv, hh.ID)
			row := vm.Rows[0]
			form := batchForm(t, srv, hh.ID, "transfer", row)
			form.Set("pair_token", row.TransferPairToken)
			partner, _, err := findBankPayment(vm.Ledger, row.TransferPairID)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				partner.AmountCents++
				if err := srv.store.ReviewBankTransaction(t.Context(), hh.ID, partner, false); err != nil {
					t.Fatal(err)
				}
			} else {
				partner.ID = 0
				partner.Name = "Second possible counterpart"
				for _, batch := range vm.Ledger.Imports {
					if batch.ID == partner.ImportID {
						if _, err := srv.store.ReconcileBankImports(t.Context(), hh.ID, []store.BankImportBatch{{Import: store.BankImport{AccountID: batch.AccountID, Digest: strings.Repeat("b", 64), Filename: "synthetic.csv"}, Payments: []store.BankTransaction{partner}}}); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if w := postMappingJSON(t, handler, "/imports/review-batch", form); w.Code != http.StatusConflict {
				t.Fatalf("unsafe pair: %d %s", w.Code, w.Body)
			}
			for _, p := range workflowVM(t, srv, hh.ID).Ledger.Transactions {
				if p.Decision != "pending" {
					t.Fatal("unsafe pair partially changed")
				}
			}
		})
	}
}
func TestAnalysisBatchesSurfacePartialFailureAndRetryOnlyMissingResults(t *testing.T) {
	srv, handler, household := newTestServer(t)
	accounts, err := srv.store.ListBankAccounts(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	payments := make([]store.BankTransaction, 0, 51)
	for i := range 51 {
		payments = append(payments, store.BankTransaction{Name: "Synthetic unknown " + strconv.Itoa(i), BookedOn: "2020-01-15",
			Direction: store.DirExpense, AmountCents: int64(100 + i), Decision: "pending", CostNature: store.CostVariable, BudgetClass: store.ClassNeed})
	}
	if _, err := srv.store.ReconcileBankImports(t.Context(), household.ID, []store.BankImportBatch{{Import: store.BankImport{AccountID: accounts[0].ID, Digest: strings.Repeat("a", 64), Filename: "synthetic.csv"}, Payments: payments}}); err != nil {
		t.Fatal(err)
	}
	classifier := &testBankClassifier{}
	classifier.reply = func(input bankimport.ClassificationRequest) []bankimport.ClassificationSuggestion {
		if len(input.Transactions) > 50 {
			t.Fatal("unbounded AI batch")
		}
		var category int64
		for _, c := range input.Categories {
			if c.Direction == store.DirExpense {
				category = c.ID
				break
			}
		}
		if classifier.calls == 2 {
			category = 999999
		}
		results := make([]bankimport.ClassificationSuggestion, 0, len(input.Transactions))
		for _, p := range input.Transactions {
			results = append(results, bankimport.ClassificationSuggestion{ID: p.ID, CategoryID: &category, Reason: "Synthetic batch evidence."})
		}
		return results
	}
	srv.SetBankImporter(classifier)
	if err := srv.store.SetAIDeployment(t.Context(), "synthetic-router"); err != nil {
		t.Fatal(err)
	}
	w := postMappingJSON(t, handler, "/imports/analyze", analysisForm(srv, household.ID))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), T(t.Context(), "review.aiFailed")) {
		t.Fatalf("partial failure hidden: %d %s", w.Code, w.Body)
	}
	analyzed := 0
	for _, p := range workflowVM(t, srv, household.ID).Ledger.Transactions {
		if p.Analysis != nil {
			analyzed++
		}
		if p.Decision != "pending" || p.CategoryID != 0 {
			t.Fatal("analysis persisted financial decisions")
		}
	}
	if analyzed != 50 || classifier.calls != 2 {
		t.Fatalf("analyzed %d, calls %d", analyzed, classifier.calls)
	}
	w = postMappingJSON(t, handler, "/imports/analyze", analysisForm(srv, household.ID))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"updated":1`) || classifier.calls != 3 {
		t.Fatalf("retry repeated completed batches: %d %s", w.Code, w.Body)
	}
}

func TestCategoryNameMatchDoesNotClaimDefaultFieldsWereDetected(t *testing.T) {
	_, _, _, vm := quickReviewFixture(t)
	for i := range vm.Ledger.Transactions {
		if vm.Ledger.Transactions[i].Name == "Unrecognized recipient" {
			vm.Ledger.Transactions[i].Name = "Lebensmittel"
		}
	}
	for _, row := range importRows(t.Context(), vm.Ledger, vm.Data, "2020-01") {
		if row.Payment.Name == "Lebensmittel" && (row.Suggestion != "rule" || row.Category == "" || !row.NatureDefault || !row.ClassDefault || !row.AllocationDefault) {
			t.Fatal("category word match presented default cost/budget/owner fields as detected")
		}
	}
}

func TestBulkCategoryDoesNotInventSharesFromZeroHistory(t *testing.T) {
	srv, handler, household, vm := quickReviewFixture(t)
	var source, target ImportRow
	for _, row := range vm.Rows {
		if row.Payment.Name == "REWE Example" {
			if source.Payment.ID == 0 {
				source = row
			} else {
				target = row
			}
		}
	}
	p := source.Payment
	p.Decision, p.AmountCents = "accepted", 0
	p.Splits = []store.BankSplit{{MemberID: vm.Data.Members[0].ID}, {MemberID: vm.Data.Members[1].ID}}
	if err := srv.store.ReviewBankTransaction(t.Context(), household.ID, p, false); err != nil {
		t.Fatal(err)
	}
	vm = workflowVM(t, srv, household.ID)
	for _, row := range vm.Rows {
		if row.Payment.ID == target.Payment.ID {
			target = row
		}
	}
	if target.Allocation != "history" || len(target.Payment.Splits) != 0 {
		t.Fatal("expected indeterminate shared allocation from a zero payment")
	}
	_, markup, found := strings.Cut(get(t, handler, "/imports?m=2020-01").Body.String(), `id="`+target.ID()+`"`)
	attributes, _, _ := strings.Cut(markup, ">")
	if !found || !strings.Contains(attributes, `data-review-category-ready="false"`) {
		t.Fatal("unsafe category action was not marked unavailable in the UI")
	}
	form := batchForm(t, srv, household.ID, "category", target)
	form.Set("category", strconv.FormatInt(target.Payment.CategoryID, 10))
	w := postMappingJSON(t, handler, "/imports/review-batch", form)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), T(t.Context(), "import.allocationMissing")) {
		t.Fatalf("indeterminate shares silently replaced: %d %s", w.Code, w.Body)
	}
	for _, row := range workflowVM(t, srv, household.ID).Rows {
		if row.Payment.ID == target.Payment.ID && (row.Allocation != "history" || len(row.Payment.Splits) != 0 || row.CanQuick) {
			t.Fatal("category action invented account-owner shares")
		}
	}
}

func TestMarketplaceHistoryRequiresMeaningfulReferences(t *testing.T) {
	a := store.BankTransaction{ID: 1, Name: "Amazon"}
	b := store.BankTransaction{ID: 2, Name: "Amazon"}
	if historyKey(a) == historyKey(b) {
		t.Fatal("unrelated marketplace purchases grouped without evidence")
	}
	a.Reference, b.Reference = "Same order reference", "Same order reference"
	if historyKey(a) != historyKey(b) {
		t.Fatal("matching reference not reusable")
	}
	b.Reference = "Different order reference"
	if historyKey(a) == historyKey(b) {
		t.Fatal("different marketplace orders grouped")
	}
	a.Name, b.Name, a.Reference, b.Reference = "REWE", "REWE", "", ""
	if historyKey(a) != historyKey(b) {
		t.Fatal("normal merchant learning unnecessarily disabled")
	}
}

func TestPairedTransfersAreUniqueAtomicAndExcludedFromActuals(t *testing.T) {
	srv, handler, household := newTestServer(t)
	accounts, err := srv.store.ListBankAccounts(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	payments := make([]store.BankTransaction, 0, 2)
	for _, direction := range []store.Direction{store.DirExpense, store.DirIncome} {
		date := "2020-01-31"
		if direction == store.DirIncome {
			date = "2020-02-01"
		}
		payments = append(payments, store.BankTransaction{Name: "Synthetic own transfer", BookedOn: date, Direction: direction, AmountCents: 12345, Decision: "pending", CostNature: store.CostVariable, BudgetClass: store.ClassNeed, Settle: true})
	}
	for i, p := range payments {
		if _, err := srv.store.ReconcileBankImports(t.Context(), household.ID, []store.BankImportBatch{{Import: store.BankImport{AccountID: accounts[i].ID, Digest: strings.Repeat("f", 64), Filename: "synthetic.csv"}, Payments: []store.BankTransaction{p}}}); err != nil {
			t.Fatal(err)
		}
	}
	vm := workflowVM(t, srv, household.ID)
	row := vm.Rows[0]
	if row.TransferPairID == 0 || row.CanQuick {
		t.Fatal("pair was not separate from expense acceptance")
	}
	form := batchForm(t, srv, household.ID, "transfer", row)
	form.Set("pair_token", row.TransferPairToken)
	w := postMappingJSON(t, handler, "/imports/review-batch", form)
	var outcome struct {
		Updated int `json:"updated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &outcome); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || outcome.Updated != 2 {
		t.Fatalf("pair: %d %s", w.Code, w.Body)
	}
	vm = workflowVM(t, srv, household.ID)
	for _, p := range vm.Ledger.Transactions {
		if p.Decision != "transfer" || p.TransferAccountID == 0 {
			t.Fatal("only one transfer side changed")
		}
	}
	for _, month := range []string{"2020-01", "2020-02"} {
		report := calc.BuildMonthReport(calc.WithBankLedger(vm.Data, vm.Ledger, calc.BasisActual, "2026-01"), month, calc.Everyone)
		if report.ExpenseCents != 0 || report.IncomeCents != 0 {
			t.Fatal("transfer counted in actuals")
		}
	}
}
