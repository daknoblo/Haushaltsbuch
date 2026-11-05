package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

const syntheticMultiCSV = "Buchungsdatum;Zahlungsempfänger;Betrag;Kontonummer;Kontoname\n" +
	"15.01.2020;Synthetic rent;-12,34;SYNTHETIC0001;Source checking\n" +
	"16.01.2020;Synthetic salary;123,45;SYNTHETIC0002;Source savings\n"

func mappingPreview(t *testing.T, srv *Server, h http.Handler, household int64) (string, []store.BankAccount) {
	t.Helper()
	accounts, err := srv.store.ListBankAccounts(t.Context(), household)
	if err != nil {
		t.Fatal(err)
	}
	w := uploadStatement(t, h, household, accounts[0].ID, "synthetic-multi.csv", []byte(syntheticMultiCSV), nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil || location.Path != "/imports" || location.Query().Get("preview") == "" || location.Fragment != importMappingID(location.Query().Get("preview")) {
		t.Fatal("multi-account upload did not return to inline mapping")
	}
	return location.Query().Get("preview"), accounts
}

func TestImportAccountMappingWorkflow(t *testing.T) {
	srv, h, hh := newTestServer(t)
	token, accounts := mappingPreview(t, srv, h, hh.ID)
	ledger, _ := srv.store.BankLedger(t.Context(), hh.ID)
	if len(ledger.Imports) != 0 || len(ledger.Transactions) != 0 {
		t.Fatal("upload persisted before account mapping")
	}
	body := get(t, h, mappingRequestURL(token)).Body.String()
	for _, part := range []string{"Source checking", "Source savings", `name="target_0"`, `name="target_1"`, "0001", "0002"} {
		if !strings.Contains(body, part) {
			t.Fatalf("missing mapping display %q", part)
		}
	}
	if strings.Contains(body, "SYNTHETIC0001") || strings.Contains(body, "SYNTHETIC0002") {
		t.Fatal("full source number exposed")
	}
	if !strings.Contains(get(t, h, "/imports").Body.String(), "synthetic-multi.csv") {
		t.Fatal("pending mapping not resumable")
	}
	form := url.Values{"household": {strconv.FormatInt(hh.ID, 10)}, "preview": {token}, "target_0": {strconv.FormatInt(accounts[0].ID, 10)}}
	if w := post(t, h, "/imports/mapping", form); w.Code != http.StatusBadRequest {
		t.Fatalf("missing destination: %d", w.Code)
	}
	form.Set("target_1", form.Get("target_0"))
	if w := post(t, h, "/imports/mapping", form); w.Code != http.StatusBadRequest {
		t.Fatalf("same destination: %d", w.Code)
	}
	other, err := srv.store.CreateHouseholdSeeded(t.Context(), "Other")
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := srv.store.ListBankAccounts(t.Context(), other.ID)
	form.Set("target_1", strconv.FormatInt(foreign[0].ID, 10))
	if w := post(t, h, "/imports/mapping", form); w.Code != http.StatusNotFound {
		t.Fatalf("foreign destination: %d", w.Code)
	}
	ledger, _ = srv.store.BankLedger(t.Context(), hh.ID)
	if len(ledger.Imports) != 0 {
		t.Fatal("invalid mapping partially saved")
	}
	form.Set("target_1", strconv.FormatInt(accounts[1].ID, 10))
	if w := post(t, h, "/imports/mapping", form); w.Code != http.StatusSeeOther {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	ledger, _ = srv.store.BankLedger(t.Context(), hh.ID)
	if len(ledger.Imports) != 2 || len(ledger.Transactions) != 2 {
		t.Fatal("mapped imports missing")
	}
	for _, p := range ledger.Transactions {
		if p.Decision != "pending" {
			t.Fatal("mapping accepted payment automatically")
		}
	}
	for _, batch := range ledger.Imports {
		for _, p := range ledger.Transactions {
			if p.ImportID != batch.ID {
				continue
			}
			if (p.Name == "Synthetic rent" && batch.AccountID != accounts[0].ID) ||
				(p.Name == "Synthetic salary" && batch.AccountID != accounts[1].ID) {
				t.Fatal("payment mapped to wrong target")
			}
		}
	}
	if w := post(t, h, "/imports/mapping", form); w.Code != http.StatusConflict {
		t.Fatalf("replayed preview: %d", w.Code)
	}
	token, _ = mappingPreview(t, srv, h, hh.ID)
	form.Set("preview", token)
	if w := post(t, h, "/imports/mapping", form); w.Code != http.StatusConflict {
		t.Fatalf("duplicate file groups: %d", w.Code)
	}
	ledger, _ = srv.store.BankLedger(t.Context(), hh.ID)
	if len(ledger.Imports) != 2 {
		t.Fatal("repeat upload duplicated records")
	}
	if w := post(t, h, "/imports/mapping/cancel", form); w.Code != http.StatusSeeOther {
		t.Fatalf("cancel: %d", w.Code)
	}
	if w := get(t, h, mappingRequestURL(token)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), T(t.Context(), "import.previewExpired")) {
		t.Fatal("canceled mapping did not recover through Import")
	}
}

func TestImportMappingScopeAndResetInvalidation(t *testing.T) {
	srv, h, hh := newTestServer(t)
	token, _ := mappingPreview(t, srv, h, hh.ID)
	other, err := srv.store.CreateHouseholdSeeded(t.Context(), "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetActiveHousehold(t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
	if w := get(t, h, mappingRequestURL(token)); w.Code != http.StatusOK || strings.Contains(w.Body.String(), "synthetic-multi.csv") {
		t.Fatalf("foreign preview not isolated: %d", w.Code)
	}
	if strings.Contains(get(t, h, "/imports").Body.String(), "synthetic-multi.csv") {
		t.Fatal("foreign preview listed")
	}
	if w := post(t, h, "/imports/mapping/cancel", url.Values{"household": {strconv.FormatInt(other.ID, 10)}, "preview": {token}}); w.Code != http.StatusNotFound {
		t.Fatalf("foreign preview cancellation: %d", w.Code)
	}
	if err := srv.store.SetActiveHousehold(t.Context(), hh.ID); err != nil {
		t.Fatal(err)
	}
	if w := post(t, h, "/settings/reset-bookings", nil); w.Code != http.StatusNoContent {
		t.Fatalf("reset: %d", w.Code)
	}
	if w := get(t, h, mappingRequestURL(token)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), T(t.Context(), "import.previewExpired")) {
		t.Fatal("reset mapping did not recover through Import")
	}
}

func mappingRequestURL(token string) string {
	path, _, _ := strings.Cut(importMappingURL(token), "#")
	return path
}

func postMappingJSON(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

func TestInlineMappingHasNoOrphanPageAndSupportsJSONActions(t *testing.T) {
	srv, handler, household := newTestServer(t)
	token, accounts := mappingPreview(t, srv, handler, household.ID)
	legacy := get(t, handler, "/imports/mapping?preview="+token)
	if legacy.Code != http.StatusSeeOther || legacy.Header().Get("Location") != importMappingURL(token) {
		t.Fatal("legacy mapping URL still renders a separate page")
	}
	page := get(t, handler, mappingRequestURL(token))
	if !strings.Contains(page.Body.String(), `data-import-mapping`) || !strings.Contains(page.Body.String(), `name="target_0"`) || strings.Contains(page.Body.String(), `href="/imports/mapping?`) {
		t.Fatal("mapping is not embedded in normal Import navigation")
	}
	form := url.Values{"household": {strconv.FormatInt(household.ID, 10)}, "preview": {token}, "target_0": {strconv.FormatInt(accounts[0].ID, 10)}, "target_1": {strconv.FormatInt(accounts[0].ID, 10)}}
	failed := post(t, handler, "/imports/mapping", form)
	if failed.Code != http.StatusBadRequest || !strings.HasPrefix(failed.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(failed.Body.String(), T(t.Context(), "import.mappingTargets")) || !strings.Contains(failed.Body.String(), `data-import-mapping`) {
		t.Fatal("native form error stranded the user outside Import")
	}
	for _, field := range []string{"target_0", "target_1"} {
		start := strings.Index(failed.Body.String(), `name="`+field+`"`)
		if start < 0 {
			t.Fatal("missing submitted mapping field")
		}
		selectHTML, _, _ := strings.Cut(failed.Body.String()[start:], "</select>")
		if !strings.Contains(selectHTML, `value="`+form.Get(field)+`" selected`) {
			t.Fatal("mapping choice lost on native validation error")
		}
	}
	if response := postMappingJSON(t, handler, "/imports/mapping", form); response.Code != http.StatusBadRequest {
		t.Fatal("duplicate destinations accepted")
	}
	form.Set("target_1", strconv.FormatInt(accounts[1].ID, 10))
	response := postMappingJSON(t, handler, "/imports/mapping", form)
	var outcome uploadOutcome
	if err := json.Unmarshal(response.Body.Bytes(), &outcome); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || outcome.Status != "imported" || outcome.Accounts != 2 || outcome.Transactions != 2 || response.Header().Get("Location") != "" {
		t.Fatalf("confirmation navigates or returns wrong outcome: %+v", outcome)
	}
	if response := postMappingJSON(t, handler, "/imports/mapping", form); response.Code != http.StatusConflict {
		t.Fatal("mapping replayed")
	}
	token, _ = mappingPreview(t, srv, handler, household.ID)
	form.Set("preview", token)
	if response := postMappingJSON(t, handler, "/imports/mapping/cancel", form); response.Code != http.StatusNoContent || response.Header().Get("Location") != "" {
		t.Fatal("cancellation navigated away")
	}
	for _, input := range []string{"https://foreign.invalid/", "//foreign.invalid/", "bad#token", "\r\nLocation: https://foreign.invalid"} {
		response := get(t, handler, "/imports/mapping?preview="+url.QueryEscape(input))
		target, err := url.Parse(response.Header().Get("Location"))
		if err != nil || target.Path != "/imports" || target.Host != "" {
			t.Fatal("legacy mapping redirect escaped Import")
		}
	}
}

func TestMappingSuggestionsRemainDistinctAndUnambiguous(t *testing.T) {
	vm := ImportMappingVM{
		Sources:         []bankimport.SourceAccount{{Name: " Main "}, {Name: "Savings"}},
		Accounts:        []store.BankAccount{{ID: 1, Name: "Main"}, {ID: 2, Name: "Savings"}, {ID: 3, Name: "MAIN"}},
		SelectedAccount: 3,
	}
	if got := mappingSuggestions(vm); got[0] != 3 || got[1] != 2 {
		t.Fatalf("selected account not reused as a matching suggestion: %v", got)
	}
	vm.SelectedAccount = 0
	if got := mappingSuggestions(vm); got[0] != 0 || got[1] != 2 {
		t.Fatalf("ambiguous destination guessed: %v", got)
	}
	vm.SelectedAccount = 3
	vm.Sources = append(vm.Sources, bankimport.SourceAccount{Name: "Main"})
	if got := mappingSuggestions(vm); got[0] != 0 || got[1] != 2 || got[2] != 0 {
		t.Fatalf("ambiguous sources merged: %v", got)
	}
}

func TestImportPreviewBoundsExpiryAndConcurrentCommit(t *testing.T) {
	var previews importPreviews
	generation := previews.epoch()
	tokens := make([]string, 0, maxImportPreviews)
	for range maxImportPreviews {
		token, err := previews.put(generation, pendingImport{household: 1})
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	if _, err := previews.put(generation, pendingImport{household: 1}); !errors.Is(err, errImportPreviewFull) {
		t.Fatal("unbounded previews")
	}
	item := previews.items[tokens[0]]
	item.expires = time.Now().Add(-time.Second)
	previews.items[tokens[0]] = item
	if _, err := previews.get(tokens[0], 1); !errors.Is(err, errImportPreview) {
		t.Fatal("expired preview accessible")
	}
	var commits atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			err := previews.commit(tokens[1], 1, func(pendingImport) error { commits.Add(1); return nil })
			if err != nil && !errors.Is(err, errImportPreview) {
				t.Errorf("unexpected commit failure: %v", err)
			}
		})
	}
	wg.Wait()
	if commits.Load() != 1 {
		t.Fatal("confirmation replayed")
	}
	if err := previews.reset(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := previews.put(generation, pendingImport{household: 1}); !errors.Is(err, errImportPreview) {
		t.Fatal("in-flight upload survived reset")
	}
	if err := previews.current(generation, func() error { t.Fatal("stale upload persisted"); return nil }); !errors.Is(err, errImportPreview) {
		t.Fatal("stale upload accepted")
	}
}
