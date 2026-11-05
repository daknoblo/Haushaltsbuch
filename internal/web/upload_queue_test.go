package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func queueUpload(t *testing.T, h http.Handler, household, account int64, filename string, data []byte, options url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := statementRequest(t, household, account, filename, data, options)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

func TestUploadDetectsPerFileAndRetainsManualCorrection(t *testing.T) {
	srv, handler, household := newTestServer(t)
	accounts, err := srv.store.ListBankAccounts(t.Context(), household.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := "Date,Payee,Amount\n01/15/2020,Private synthetic payee,-12.34\n"
	response := queueUpload(t, handler, household.ID, accounts[0].ID, "us.csv", []byte(input), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("automatic US import: %d %s", response.Code, response.Body)
	}
	var result uploadOutcome
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	detection := strings.Join(result.Detected, "\n")
	if !strings.Contains(detection, "Date") || !strings.Contains(detection, T(t.Context(), "import.decimalDot")) || strings.Contains(detection, "Private synthetic payee") {
		t.Fatal("detection details missing or leaking cell values")
	}
	ambiguous := []byte("Date;Payee;Amount\n01/02/2020;Example;-1.23\n")
	response = queueUpload(t, handler, household.ID, accounts[0].ID, "ambiguous.csv", ambiguous, nil)
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Import-Configure") != "1" {
		t.Fatal("ambiguous date did not request correction")
	}
	ledger, err := srv.store.BankLedger(t.Context(), household.ID)
	if err != nil || len(ledger.Imports) != 1 {
		t.Fatal("ambiguous upload partially persisted")
	}
	response = queueUpload(t, handler, household.ID, accounts[0].ID, "ambiguous.csv", ambiguous, url.Values{"table_date_order": {"mdy"}})
	if response.Code != http.StatusCreated {
		t.Fatalf("corrected import: %d %s", response.Code, response.Body)
	}
	page := get(t, handler, "/imports").Body.String()
	if !strings.Contains(page, T(t.Context(), "import.uploadAutomatic")) || strings.Contains(page, "Wartet auf Start") {
		t.Fatal("manual-start interface remains")
	}
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var visit func(*html.Node, bool)
	visit = func(node *html.Node, inUpload bool) {
		if node.Type == html.ElementNode && node.Data == "noscript" {
			return
		}
		kind := ""
		for _, attr := range node.Attr {
			if attr.Key == "data-bank-upload" {
				inUpload = true
			}
			if attr.Key == "type" {
				kind = attr.Val
			}
		}
		if inUpload && node.Data == "button" && (kind == "" || kind == "submit") {
			t.Error("upload start button remains for JavaScript users")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, inUpload)
		}
	}
	visit(doc, false)
}

func TestImportMonthURLRemainsLocal(t *testing.T) {
	for _, month := range []string{"2020-01", "//foreign.invalid/path", "https://foreign.invalid/", "x&redirect=//foreign.invalid"} {
		target, err := url.Parse(importMonthURL(month))
		if err != nil || target.IsAbs() || target.Host != "" || target.Path != "/imports" || target.Query().Get("m") != month {
			t.Fatalf("unsafe month URL: %q", month)
		}
	}
}

func TestUploadQueueOutcomesAndPersistentHistory(t *testing.T) {
	srv, h, hh := newTestServer(t)
	ctx := t.Context()
	accounts, err := srv.store.ListBankAccounts(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(syntheticCSV)
	response := queueUpload(t, h, hh.ID, accounts[0].ID, "queue.csv", data, nil)
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("queue response: %d %v", response.Code, response.Header())
	}
	var result uploadOutcome
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	if result.Status != "imported" || result.Transactions != 2 || result.Accounts != 1 ||
		result.URL != "/imports?m=2020-01" || result.Digest != hex.EncodeToString(hash[:]) {
		t.Fatalf("unexpected outcome: %+v", result)
	}
	historyURL := "/imports/status?part=history&m=2020-01&household=" + strconv.FormatInt(hh.ID, 10)
	history := get(t, h, historyURL)
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), `id="import-upload-history"`) ||
		!strings.Contains(history.Body.String(), "queue.csv") || !strings.Contains(history.Body.String(), `data-uploaded-transactions="2"`) {
		t.Fatal("persisted file missing from status fragment")
	}
	if strings.Contains(history.Body.String(), `data-bank-upload`) {
		t.Fatal("status update would replace active file queue")
	}
	page := get(t, h, "/imports?m=2020-01").Body.String()
	if !strings.Contains(page, "queue.csv") || !strings.Contains(page, `data-uploaded-digest="`+result.Digest+`"`) {
		t.Fatal("file history lost after reload")
	}
	review := get(t, h, "/imports/status?part=review&m=2020-01&household="+strconv.FormatInt(hh.ID, 10))
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), `id="import-review"`) || !strings.Contains(review.Body.String(), "Synthetic rent") {
		t.Fatal("review fragment missing imported transactions")
	}
	if duplicate := queueUpload(t, h, hh.ID, accounts[0].ID, "duplicate.csv", data, nil); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate returned success: %d", duplicate.Code)
	}
	if bad := queueUpload(t, h, hh.ID, accounts[0].ID, "bad.csv", []byte("not a valid statement"), nil); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid upload returned success: %d", bad.Code)
	}
	if valid := queueUpload(t, h, hh.ID, accounts[1].ID, "later.xlsx", syntheticXLSX(t), nil); valid.Code != http.StatusCreated {
		t.Fatalf("later valid file failed: %d %s", valid.Code, valid.Body)
	}
}

func TestQueuedMappingOutcomeAndHistoryResolution(t *testing.T) {
	srv, h, hh := newTestServer(t)
	accounts, err := srv.store.ListBankAccounts(t.Context(), hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	response := queueUpload(t, h, hh.ID, accounts[0].ID, "multi.csv", []byte(syntheticMultiCSV), nil)
	if response.Code != http.StatusAccepted {
		t.Fatalf("mapping outcome: %d %s", response.Code, response.Body)
	}
	var result uploadOutcome
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "mapping" || result.Accounts != 2 || result.Transactions != 2 {
		t.Fatalf("mapping shape: %+v", result)
	}
	target, err := url.Parse(result.URL)
	if err != nil || target.Path != "/imports/mapping" || target.Query().Get("preview") == "" {
		t.Fatal("mapping link invalid")
	}
	before := get(t, h, "/imports").Body.String()
	if !strings.Contains(before, `data-pending-upload-url="`+result.URL+`"`) || strings.Contains(before, `data-uploaded-digest=`) {
		t.Fatal("mapping was shown as already persisted")
	}
	form := url.Values{
		"household": {strconv.FormatInt(hh.ID, 10)}, "preview": {target.Query().Get("preview")},
		"target_0": {strconv.FormatInt(accounts[0].ID, 10)}, "target_1": {strconv.FormatInt(accounts[1].ID, 10)},
	}
	if saved := post(t, h, "/imports/mapping", form); saved.Code != http.StatusSeeOther {
		t.Fatalf("confirm mapping: %d", saved.Code)
	}
	after := get(t, h, "/imports").Body.String()
	if strings.Contains(after, `data-pending-upload-url=`) || strings.Count(after, `data-uploaded-digest=`) != 1 ||
		!strings.Contains(after, `data-uploaded-accounts="2"`) {
		t.Fatal("mapped file history did not resolve/group the source file")
	}
}

func TestUploadRejectsMultipleFilesInOneRequest(t *testing.T) {
	srv, h, hh := newTestServer(t)
	accounts, _ := srv.store.ListBankAccounts(t.Context(), hh.ID)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("household", strconv.FormatInt(hh.ID, 10)); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("account", strconv.FormatInt(accounts[0].ID, 10)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.csv", "second.csv"} {
		part, err := writer.CreateFormFile("statement", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(syntheticCSV)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/imports/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatal("multi-file body silently imported only one file")
	}
	ledger, _ := srv.store.BankLedger(t.Context(), hh.ID)
	if len(ledger.Imports) != 0 {
		t.Fatal("rejected multipart body partially persisted")
	}
}

func TestUploadQueueScopeAndGeneration(t *testing.T) {
	srv, h, hh := newTestServer(t)
	ctx := t.Context()
	accounts, _ := srv.store.ListBankAccounts(ctx, hh.ID)
	generation := strconv.FormatUint(srv.importPreviews.epoch(), 10)
	if response := post(t, h, "/settings/reset-bookings", nil); response.Code != http.StatusNoContent {
		t.Fatal("reset failed")
	}
	response := queueUpload(t, h, hh.ID, accounts[0].ID, "stale.csv", []byte(syntheticCSV), url.Values{"upload_generation": {generation}})
	if response.Code != http.StatusConflict {
		t.Fatalf("stale upload context: %d", response.Code)
	}
	ledger, _ := srv.store.BankLedger(ctx, hh.ID)
	if len(ledger.Imports) != 0 {
		t.Fatal("stale batch wrote to replacement data")
	}
	other, err := srv.store.CreateHouseholdSeeded(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if response := get(t, h, "/imports/status?part=history&household="+strconv.FormatInt(other.ID, 10)); response.Code != http.StatusConflict {
		t.Fatalf("foreign history: %d", response.Code)
	}
}

func TestUploadedFileReviewLinkTargetsPendingMonth(t *testing.T) {
	ledger := store.BankLedger{
		Imports:  []store.BankImport{{ID: 1, AccountID: 1, Digest: "synthetic", Filename: "year.csv"}},
		Accounts: []store.BankAccount{{ID: 1, MemberID: 1, Name: "Example"}},
		Transactions: []store.BankTransaction{
			{ImportID: 1, BookedOn: "2020-01-01", Decision: "accepted"},
			{ImportID: 1, BookedOn: "2020-02-01", Decision: "pending"},
			{ImportID: 1, BookedOn: "2020-03-01", Decision: "pending"},
		},
	}
	files := uploadedFiles(t.Context(), ledger, []store.Member{{ID: 1, Name: "Example member"}})
	if len(files) != 1 || files[0].ReviewURL != "/imports?m=2020-02" || files[0].Pending != 2 {
		t.Fatal("review link does not target the first unfinished month")
	}
}

func TestUploadedFilesGroupsAndOrdersHistory(t *testing.T) {
	srv, h, hh := newTestServer(t)
	accounts, _ := srv.store.ListBankAccounts(t.Context(), hh.ID)
	for i := 1; i <= 6; i++ {
		hash := sha256.Sum256([]byte(strconv.Itoa(i)))
		batch := store.BankImport{AccountID: accounts[0].ID, Digest: hex.EncodeToString(hash[:]), Filename: fmt.Sprintf("file-%d.csv", i)}
		payment := store.BankTransaction{BookedOn: "2020-01-01", Name: "Synthetic", Direction: store.DirExpense,
			AmountCents: 100, CostNature: store.CostVariable, BudgetClass: store.ClassNeed}
		if err := srv.store.CreateBankImport(t.Context(), hh.ID, batch, []store.BankTransaction{payment}); err != nil {
			t.Fatal(err)
		}
	}
	vm, err := srv.buildImportVM(t.Context(), hh.ID, "2020-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(vm.Uploads) != 6 || vm.Uploads[0].Filename != "file-6.csv" || vm.Uploads[5].Filename != "file-1.csv" {
		t.Fatal("history is not newest first")
	}
	body := get(t, h, "/imports").Body.String()
	if strings.Count(body, `data-uploaded-digest=`) != 6 || !strings.Contains(body, "Weitere Dateien (1)") {
		t.Fatal("older uploaded files are missing")
	}
	older := strings.Index(body, "Weitere Dateien (1)")
	if strings.Index(body, "file-1.csv") < older {
		t.Fatal("oldest file was not placed in older history")
	}
	if strings.Contains(body, `href="/settings#bank-accounts"`) {
		t.Fatal("bank-account configuration link remains in upload page")
	}
	if !strings.Contains(body, `data-upload-dropzone`) || !strings.Contains(body, `multiple`) || !strings.Contains(body, `imports.js`) {
		t.Fatal("multi-file drag/drop controls missing")
	}
}
