package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/props"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

type testBankImporter struct{ calls int }

func (*testBankImporter) Deployments(context.Context) ([]string, error) {
	return []string{"synthetic-router"}, nil
}
func (i *testBankImporter) Extract(_ context.Context, deployment, content string) ([]store.BankTransaction, error) {
	i.calls++
	if deployment != "synthetic-router" || !strings.Contains(content, "Synthetic statement") {
		return nil, bankimport.ErrResult
	}
	return []store.BankTransaction{{
		BookedOn: "2020-01-15", Direction: store.DirExpense, AmountCents: 1234,
		Name: "Synthetic only", Reference: "No personal data", Decision: "pending",
		CostNature: store.CostVariable, BudgetClass: store.ClassNeed,
	}}, nil
}

func TestPDFImportReviewAndHybridHTTP(t *testing.T) {
	srv, handler, hh := newTestServer(t)
	mock := &testBankImporter{}
	srv.SetBankImporter(mock)
	ctx := t.Context()
	members, _ := srv.store.ListMembers(ctx, hh.ID)
	categories, _ := srv.store.ListCategories(ctx, hh.ID)
	var category int64
	for _, c := range categories {
		if c.Classification == store.DirExpense {
			category = c.ID
			break
		}
	}
	hhID := strconv.FormatInt(hh.ID, 10)
	owner := strconv.FormatInt(members[0].ID, 10)
	if w := post(t, handler, "/imports/accounts", url.Values{"household": {hhID}, "name": {"Synthetic"}, "member": {owner}}); w.Code != http.StatusSeeOther {
		t.Fatalf("account: %d %s", w.Code, w.Body)
	}
	ledger, _ := srv.store.BankLedger(ctx, hh.ID)
	account := strconv.FormatInt(ledger.Accounts[0].ID, 10)
	pdf := newPDF()
	pdf.AddRow(10, text.NewCol(12, "Synthetic statement 12.34 EUR", props.Text{}))
	doc, err := pdf.Generate()
	if err != nil {
		t.Fatal(err)
	}
	upload := func(consent bool) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for key, value := range map[string]string{"household": hhID, "account": account, "deployment": "synthetic-router"} {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		if consent {
			_ = writer.WriteField("consent", "yes")
		}
		part, err := writer.CreateFormFile("statement", "synthetic.pdf")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(doc.GetBytes())
		_ = writer.Close()
		r := httptest.NewRequest(http.MethodPost, "/imports/upload", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := upload(false); w.Code != http.StatusBadRequest || mock.calls != 0 {
		t.Fatalf("consent: %d calls %d", w.Code, mock.calls)
	}
	if w := upload(true); w.Code != http.StatusSeeOther {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if w := upload(true); w.Code != http.StatusConflict || mock.calls != 1 {
		t.Fatalf("duplicate file: %d calls %d", w.Code, mock.calls)
	}
	ledger, _ = srv.store.BankLedger(ctx, hh.ID)
	p := ledger.Transactions[0]
	path := "/imports/transactions/" + strconv.FormatInt(p.ID, 10)
	form := url.Values{
		"household": {hhID}, "date": {p.BookedOn}, "direction": {"expense"}, "amount": {"12,34"},
		"name": {p.Name}, "reference": {p.Reference}, "category": {strconv.FormatInt(category, 10)},
		"nature": {"variable"}, "class": {"need"}, "allocation": {"owner"}, "decision": {"accepted"},
	}
	if w := post(t, handler, path, form); w.Code != http.StatusSeeOther {
		t.Fatalf("review: %d %s", w.Code, w.Body)
	}
	if w := post(t, handler, path, form); w.Code != http.StatusBadRequest {
		t.Fatalf("replayed review: %d", w.Code)
	}
	closeForm := url.Values{"household": {hhID}, "month": {"2020-01"}, "closed": {"yes"}}
	if w := post(t, handler, "/imports/month", closeForm); w.Code != http.StatusBadRequest {
		t.Fatalf("closure consent: %d", w.Code)
	}
	closeForm.Set("complete", "yes")
	if w := post(t, handler, "/imports/month", closeForm); w.Code != http.StatusSeeOther {
		t.Fatalf("close: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{
		"/imports?m=2020-01", "/?m=2020-01&basis=actual", "/dashboard?m=2020-01&basis=hybrid",
		"/export/overview.pdf?m=2020-01&basis=actual", "/export/statistics.pdf?m=2020-01&basis=hybrid",
		"/export/year.pdf?m=2020-01&basis=actual",
	} {
		if w := get(t, handler, path); w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	if w := get(t, handler, "/?basis=unknown"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown basis: %d", w.Code)
	}
	if w := post(t, handler, "/imports/transactions/"+strconv.FormatInt(p.ID, 10), url.Values{
		"household": {hhID}, "decision": {"pending"},
	}); w.Code != http.StatusConflict {
		t.Fatalf("closed edit: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/report?month=2020-01&basis=hybrid", nil)
	r.Header.Set("Authorization", "Bearer "+testAPIToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"expense_cents":1234`) ||
		!strings.Contains(w.Body.String(), `"source":"actual"`) {
		t.Fatalf("API basis: %d %s", w.Code, w.Body)
	}
}

func TestImportDisabledAndStaleHousehold(t *testing.T) {
	srv, handler, hh := newTestServer(t)
	if w := get(t, handler, "/imports"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "KI-Import ist deaktiviert") {
		t.Fatalf("disabled page: %d %s", w.Code, w.Body)
	}
	if w := post(t, handler, "/imports/upload", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled upload: %d", w.Code)
	}
	members, _ := srv.store.ListMembers(t.Context(), hh.ID)
	if w := post(t, handler, "/imports/accounts", url.Values{
		"household": {"999999"}, "name": {"Test"}, "member": {strconv.FormatInt(members[0].ID, 10)},
	}); w.Code != http.StatusConflict {
		t.Fatalf("stale household: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/imports/accounts", strings.NewReader("household=1&name=forged&member=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://foreign.invalid")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross origin: %d", w.Code)
	}
}
