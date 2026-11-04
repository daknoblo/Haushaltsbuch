package web

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

const syntheticCSV = "Buchungstag;Name;Verwendungszweck;Betrag;Währung\n" +
	"15.01.2020;Synthetic rent;Synthetic reference;-12,34;EUR\n" +
	"16.01.2020;Synthetic salary;Synthetic salary;123,45;EUR\n"

func syntheticXLSX(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	for row, values := range [][]any{
		{"Buchungstag", "Name", "Verwendungszweck", "Betrag", "Währung"},
		{"15.01.2020", "Synthetic rent", "Synthetic reference", -12.34, "EUR"},
		{"16.01.2020", "Synthetic salary", "Synthetic salary", 123.45, "EUR"},
	} {
		for col, value := range values {
			cell, err := excelize.CoordinatesToCellName(col+1, row+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue("Sheet1", cell, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func uploadStatement(t *testing.T, h http.Handler, household, account int64, filename string, data []byte, options url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := url.Values{"household": {strconv.FormatInt(household, 10)}, "account": {strconv.FormatInt(account, 10)}}
	for key, values := range options {
		fields[key] = values
	}
	for key, values := range fields {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	file, err := writer.CreateFormFile("statement", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/imports/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

func TestStructuredStatementUploadsDoNotNeedOrCallAI(t *testing.T) {
	for _, kind := range []string{"csv", "xlsx"} {
		for _, enabled := range []bool{false, true} {
			name := kind + " without AI"
			if enabled {
				name = kind + " with AI"
			}
			t.Run(name, func(t *testing.T) {
				srv, h, hh := newTestServer(t)
				mock := &testBankImporter{}
				if enabled {
					srv.SetBankImporter(mock)
				}
				accounts, err := srv.store.ListBankAccounts(t.Context(), hh.ID)
				if err != nil {
					t.Fatal(err)
				}
				data := []byte(syntheticCSV)
				if kind == "xlsx" {
					data = syntheticXLSX(t)
				}
				w := uploadStatement(t, h, hh.ID, accounts[0].ID, "statement."+kind, data, nil)
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/imports?m=2020-01" {
					t.Fatalf("upload: %d %s", w.Code, w.Body)
				}
				if mock.calls != 0 || mock.discoveryCalls != 0 {
					t.Fatal("structured file invoked AI")
				}
				ledger, err := srv.store.BankLedger(t.Context(), hh.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(ledger.Imports) != 1 || len(ledger.Transactions) != 2 || ledger.Transactions[0].AmountCents != 1234 ||
					ledger.Transactions[0].Direction != store.DirExpense || ledger.Transactions[1].AmountCents != 12345 {
					t.Fatalf("stored statement: %+v", ledger)
				}
				for _, p := range ledger.Transactions {
					if p.Decision != "pending" {
						t.Fatal("automatic acceptance")
					}
				}
				bookings, err := srv.store.ListBookings(t.Context(), hh.ID)
				if err != nil || len(bookings) != 0 {
					t.Fatal("upload created plans")
				}
				if w := uploadStatement(t, h, hh.ID, accounts[0].ID, "renamed."+kind, data, nil); w.Code != http.StatusConflict {
					t.Fatalf("duplicate file: %d", w.Code)
				}
				body := get(t, h, "/imports?m=2020-01").Body.String()
				if !strings.Contains(body, "Synthetic rent") || !strings.Contains(body, `value="accepted"`) {
					t.Fatal("review workflow missing")
				}
			})
		}
	}
}

func TestStructuredImportMappingAndAtomicFailure(t *testing.T) {
	srv, h, hh := newTestServer(t)
	accounts, _ := srv.store.ListBankAccounts(t.Context(), hh.ID)
	options := url.Values{
		"column_date": {"When"}, "column_name": {"Counter party"}, "column_amount": {"Value"}, "column_reference": {"Reason"},
		"table_decimal": {"dot"}, "table_date_order": {"mdy"}, "table_header": {"2"},
	}
	data := []byte("Synthetic export\nWhen;Counter party;Value;Reason\n01/15/2020;Example;-12.34;Reference\n")
	if w := uploadStatement(t, h, hh.ID, accounts[0].ID, "mapping.csv", data, options); w.Code != http.StatusSeeOther {
		t.Fatalf("explicit mapping: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		name    string
		data    []byte
		options url.Values
	}{
		{"bad.csv", []byte(syntheticCSV + "31.02.2020;private-name-marker;private-note;1,00;EUR\n"), nil},
		{"invalid.csv", []byte(syntheticCSV), url.Values{"table_decimal": {"unknown"}}},
		{"bad.xlsx", []byte("not an Excel file"), nil},
		{"old.xls", syntheticXLSX(t), nil},
		{"image.png", []byte("unsupported"), nil},
	} {
		w := uploadStatement(t, h, hh.ID, accounts[0].ID, tc.name, tc.data, tc.options)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "private-name-marker") || strings.Contains(w.Body.String(), "private-note") {
			t.Fatal("private cells reflected in error")
		}
	}
	ledger, _ := srv.store.BankLedger(t.Context(), hh.ID)
	if len(ledger.Imports) != 1 || len(ledger.Transactions) != 1 {
		t.Fatal("failed import left partial rows")
	}
}

func TestStructuredImportScopesAndClosedMonths(t *testing.T) {
	srv, h, hh := newTestServer(t)
	ctx := t.Context()
	accounts, _ := srv.store.ListBankAccounts(ctx, hh.ID)
	other, err := srv.store.CreateHouseholdSeeded(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := srv.store.ListBankAccounts(ctx, other.ID)
	if w := uploadStatement(t, h, hh.ID, foreign[0].ID, "foreign.csv", []byte(syntheticCSV), nil); w.Code != http.StatusNotFound {
		t.Fatalf("foreign account: %d", w.Code)
	}
	if w := uploadStatement(t, h, other.ID, foreign[0].ID, "stale.xlsx", syntheticXLSX(t), nil); w.Code != http.StatusConflict {
		t.Fatalf("stale household: %d", w.Code)
	}
	if err := srv.store.SetMonthReconciled(ctx, hh.ID, "2020-01", true); err != nil {
		t.Fatal(err)
	}
	if w := uploadStatement(t, h, hh.ID, accounts[0].ID, "closed.csv", []byte(syntheticCSV), nil); w.Code != http.StatusConflict {
		t.Fatalf("closed month: %d", w.Code)
	}
	ledger, _ := srv.store.BankLedger(ctx, hh.ID)
	if len(ledger.Imports) != 0 || len(ledger.Transactions) != 0 {
		t.Fatal("closed-month import partially persisted")
	}
	if w := uploadStatement(t, h, hh.ID, accounts[0].ID, "unconfigured.pdf", []byte("%PDF-1.4"), nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("PDF unexpectedly enabled: %d", w.Code)
	}
}

func TestCrossFormatDuplicateProtection(t *testing.T) {
	srv, h, hh := newTestServer(t)
	ctx := t.Context()
	accounts, err := srv.store.ListBankAccounts(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	account := accounts[0]
	if w := uploadStatement(t, h, hh.ID, account.ID, "statement.csv", []byte(syntheticCSV), nil); w.Code != http.StatusSeeOther {
		t.Fatal(w.Body.String())
	}
	ledger, err := srv.store.BankLedger(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	first := ledger.Transactions[0]
	categories, err := srv.store.ListCategories(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range categories {
		if c.Classification == store.DirExpense {
			first.CategoryID = c.ID
			break
		}
	}
	first.Decision = "accepted"
	first.Splits = []store.BankSplit{{MemberID: account.MemberID, Cents: first.AmountCents}}
	if err := srv.store.ReviewBankTransaction(ctx, hh.ID, first, false); err != nil {
		t.Fatal(err)
	}
	if w := uploadStatement(t, h, hh.ID, account.ID, "statement.xlsx", syntheticXLSX(t), nil); w.Code != http.StatusSeeOther {
		t.Fatal(w.Body.String())
	}
	ledger, err = srv.store.BankLedger(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ledger.Transactions {
		if p.ImportID == first.ImportID || p.Name != first.Name {
			continue
		}
		p.CategoryID, p.Decision, p.Splits = first.CategoryID, "accepted", first.Splits
		if err := srv.store.ReviewBankTransaction(ctx, hh.ID, p, false); !errors.Is(err, store.ErrDuplicatePayment) {
			t.Fatalf("cross-format duplicate accepted: %v", err)
		}
		return
	}
	t.Fatal("matching XLSX transaction not found")
}

func TestStructuredUploadUIIsAvailableWithoutAI(t *testing.T) {
	_, h, _ := newTestServer(t)
	for _, lang := range []string{"de", "en"} {
		r := httptest.NewRequest(http.MethodGet, "/imports", nil)
		r.Header.Set("Accept-Language", lang)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		body := w.Body.String()
		for _, fragment := range []string{`data-bank-upload`, `data-table-options`, `.pdf,.csv,.xlsx`, `name="table_sheet"`, `name="column_date"`} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("%s: missing %s", lang, fragment)
			}
		}
		if strings.Contains(body, `name="deployment"`) {
			t.Fatal("upload overrides global model")
		}
	}
}
