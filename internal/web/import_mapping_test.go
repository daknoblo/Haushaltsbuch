package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	if err != nil || location.Path != "/imports/mapping" || location.Query().Get("preview") == "" {
		t.Fatal("multi-account upload did not open mapping")
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
	body := get(t, h, importMappingURL(token)).Body.String()
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
	if w := get(t, h, importMappingURL(token)); w.Code != http.StatusConflict {
		t.Fatal("canceled mapping still accessible")
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
	if w := get(t, h, importMappingURL(token)); w.Code != http.StatusNotFound {
		t.Fatalf("foreign preview read: %d", w.Code)
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
	if w := get(t, h, importMappingURL(token)); w.Code != http.StatusConflict {
		t.Fatal("reset retained pending mapping")
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
