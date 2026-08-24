package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func newTestServer(t *testing.T) (*Server, http.Handler, store.Household) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := t.Context()
	if err := st.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hs, err := st.ListHouseholds(ctx)
	if err != nil {
		t.Fatalf("list households: %v", err)
	}

	srv := New(st, slog.New(slog.DiscardHandler))
	return srv, srv.Handler(), hs[0]
}

// post issues a same-origin form POST, which the CSRF middleware requires.
func post(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	body := ""
	if form != nil {
		body = form.Encode()
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPagesRender(t *testing.T) {
	_, h, _ := newTestServer(t)
	for _, path := range []string{"/", "/expenses", "/income", "/statistics", "/settings"} {
		if got := get(t, h, path).Code; got != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, got)
		}
	}
}

func TestHealthzIgnoresDatabase(t *testing.T) {
	srv, h, _ := newTestServer(t)
	if got := get(t, h, "/healthz").Code; got != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", got)
	}

	// Liveness must not depend on the store, otherwise a stalled database
	// would make the container be restarted instead of just reported unready.
	if err := srv.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if got := get(t, h, "/healthz").Code; got != http.StatusOK {
		t.Errorf("GET /healthz after close = %d, want 200", got)
	}
}

func TestReadyzChecksDatabase(t *testing.T) {
	srv, h, _ := newTestServer(t)
	if got := get(t, h, "/readyz").Code; got != http.StatusOK {
		t.Fatalf("GET /readyz = %d, want 200", got)
	}

	if err := srv.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if got := get(t, h, "/readyz").Code; got != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz after close = %d, want 503", got)
	}
}

func TestVersionEndpoint(t *testing.T) {
	_, h, _ := newTestServer(t)
	w := get(t, h, "/version")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /version = %d, want 200", w.Code)
	}

	var payload map[string]string
	if err := json.NewDecoder(w.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"version", "commit", "date"} {
		if payload[key] == "" {
			t.Errorf("field %q is empty", key)
		}
	}
}

func TestRateLimitRejectsFloods(t *testing.T) {
	_, h, _ := newTestServer(t)

	var last int
	for i := 0; i < rateBurst+5; i++ {
		last = get(t, h, "/").Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("after %d requests = %d, want 429", rateBurst+5, last)
	}

	// Probes and assets stay reachable even once the bucket is empty.
	if got := get(t, h, "/healthz").Code; got != http.StatusOK {
		t.Errorf("/healthz while limited = %d, want 200", got)
	}
	if got := get(t, h, "/static/app.css").Code; got != http.StatusOK {
		t.Errorf("/static while limited = %d, want 200", got)
	}
}

func TestLanguageFollowsAcceptLanguage(t *testing.T) {
	_, h, _ := newTestServer(t)

	r := httptest.NewRequest(http.MethodGet, "/expenses", nil)
	r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, "Changes are saved automatically.") {
		t.Error("English page did not use the English catalog")
	}
	if strings.Contains(body, "Änderungen werden automatisch gespeichert.") {
		t.Error("English page still contains German text")
	}
}

func TestCrossOriginPostIsRejected(t *testing.T) {
	_, h, _ := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/expenses/new", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", w.Code)
	}
}

func TestExpenseMutationsAreHouseholdScoped(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	other, err := srv.store.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	foreign, err := srv.store.CreateExpense(ctx, store.Expense{
		HouseholdID: other.ID,
		Name:        "Fremde Ausgabe",
		AmountCents: 5000,
		Frequency:   store.FreqMonthly,
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
	}, nil)
	if err != nil {
		t.Fatalf("create foreign expense: %v", err)
	}
	if active.ID == other.ID {
		t.Fatal("test setup: households must differ")
	}

	id := strconv.FormatInt(foreign.ID, 10)
	if got := post(t, h, "/expenses/"+id, url.Values{"name": {"gekapert"}}).Code; got != http.StatusNotFound {
		t.Errorf("update foreign expense = %d, want 404", got)
	}
	if got := post(t, h, "/expenses/"+id+"/delete", nil).Code; got != http.StatusNotFound {
		t.Errorf("delete foreign expense = %d, want 404", got)
	}

	unchanged, err := srv.store.GetExpense(ctx, foreign.ID)
	if err != nil {
		t.Fatalf("get foreign expense: %v", err)
	}
	if unchanged.Name != "Fremde Ausgabe" {
		t.Errorf("foreign expense was modified: %q", unchanged.Name)
	}
}

func TestExpenseUpdateIgnoresForeignSection(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	other, err := srv.store.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	foreignSections, _ := srv.store.ListSections(ctx, other.ID)

	own, err := srv.store.CreateExpense(ctx, store.Expense{
		HouseholdID: active.ID,
		Name:        "Miete",
		Frequency:   store.FreqMonthly,
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
	}, nil)
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}

	w := post(t, h, "/expenses/"+strconv.FormatInt(own.ID, 10), url.Values{
		"name":       {"Miete"},
		"amount":     {"100"},
		"section_id": {strconv.FormatInt(foreignSections[0].ID, 10)},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d, want 200", w.Code)
	}

	updated, err := srv.store.GetExpense(ctx, own.ID)
	if err != nil {
		t.Fatalf("get expense: %v", err)
	}
	if updated.SectionID != nil {
		t.Errorf("foreign section was stored: %d", *updated.SectionID)
	}
}

func TestExpenseUpdateRejectsOutOfRangeAmount(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	e, err := srv.store.CreateExpense(ctx, store.Expense{
		HouseholdID: active.ID,
		Name:        "Miete",
		AmountCents: 120000,
		Frequency:   store.FreqMonthly,
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
	}, nil)
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	path := "/expenses/" + strconv.FormatInt(e.ID, 10)

	for _, amount := range []string{"Inf", "NaN", "1e30"} {
		if got := post(t, h, path, url.Values{"name": {"Miete"}, "amount": {amount}}).Code; got != http.StatusBadRequest {
			t.Errorf("amount %q = %d, want 400", amount, got)
		}
	}

	// A partially typed amount keeps the stored value instead of zeroing it.
	if got := post(t, h, path, url.Values{"name": {"Miete"}, "amount": {"-"}}).Code; got != http.StatusOK {
		t.Fatalf("partial amount = %d, want 200", got)
	}
	after, _ := srv.store.GetExpense(ctx, e.ID)
	if after.AmountCents != 120000 {
		t.Errorf("amount = %d, want 120000", after.AmountCents)
	}
}

func TestIncomeCopyRejectsRepeat(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()
	members, _ := srv.store.ListMembers(ctx, active.ID)

	if _, err := srv.store.CreateIncome(ctx, active.ID, members[0].ID, "2026-07", "Gehalt", 300000); err != nil {
		t.Fatalf("create income: %v", err)
	}
	if got := post(t, h, "/income/copy?from=2026-07&to=2026-08", nil).Code; got != http.StatusNoContent {
		t.Fatalf("first copy = %d, want 204", got)
	}
	if got := post(t, h, "/income/copy?from=2026-07&to=2026-08", nil).Code; got != http.StatusConflict {
		t.Errorf("second copy = %d, want 409", got)
	}

	lines, _ := srv.store.ListIncomes(ctx, active.ID, "2026-08")
	if len(lines) != 1 {
		t.Errorf("want 1 income line, got %d", len(lines))
	}
}

func TestMemberUpdateKeepsColorOnInvalidInput(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()
	members, _ := srv.store.ListMembers(ctx, active.ID)
	before := members[0]

	w := post(t, h, "/members/"+strconv.FormatInt(before.ID, 10), url.Values{
		"name":  {"Neuer Name"},
		"color": {"javascript:alert(1)"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("update = %d, want 204", w.Code)
	}

	after, err := srv.store.GetMember(ctx, before.ID)
	if err != nil {
		t.Fatalf("get member: %v", err)
	}
	if after.Color != before.Color {
		t.Errorf("color = %q, want %q", after.Color, before.Color)
	}
	if after.Name != "Neuer Name" {
		t.Errorf("name = %q, want %q", after.Name, "Neuer Name")
	}
}

func TestExportFilenameIsEncoded(t *testing.T) {
	srv, h, active := newTestServer(t)
	if err := srv.store.RenameHousehold(t.Context(), active.ID, `Bad" name`); err != nil {
		t.Fatalf("rename: %v", err)
	}

	w := get(t, h, "/export/expenses.pdf")
	if w.Code != http.StatusOK {
		t.Fatalf("export = %d, want 200", w.Code)
	}
	cd := w.Header().Get("Content-Disposition")
	disposition, params, err := mime.ParseMediaType(cd)
	if err != nil {
		t.Fatalf("Content-Disposition %q is malformed: %v", cd, err)
	}
	if disposition != "attachment" {
		t.Errorf("disposition = %q, want attachment", disposition)
	}
	if strings.ContainsAny(params["filename"], `"\`) {
		t.Errorf("filename contains quoting characters: %q", params["filename"])
	}
}

func TestReorderSectionViaHTTP(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()
	before, _ := srv.store.ListSections(ctx, active.ID)

	w := post(t, h, "/sections/"+strconv.FormatInt(before[1].ID, 10)+"/move?dir=up", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("move = %d, want 204", w.Code)
	}
	if w.Header().Get("HX-Refresh") != "true" {
		t.Errorf("missing HX-Refresh header")
	}

	after, _ := srv.store.ListSections(ctx, active.ID)
	if after[0].ID != before[1].ID {
		t.Errorf("section was not moved: %d stays first", after[0].ID)
	}

	if got := post(t, h, "/sections/"+strconv.FormatInt(before[1].ID, 10)+"/move?dir=sideways", nil).Code; got != http.StatusBadRequest {
		t.Errorf("invalid direction = %d, want 400", got)
	}
}

func TestAssetsCacheHeader(t *testing.T) {
	_, h, _ := newTestServer(t)
	w := get(t, h, "/static/app.css?v=test")
	if w.Code != http.StatusOK {
		t.Fatalf("GET asset = %d, want 200", w.Code)
	}
	// Tests run against the "dev" version, where caching must stay off so a
	// rebuild is picked up despite the unchanged asset URL.
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache for a dev build", cc)
	}
	if _, err := io.ReadAll(w.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
}

func TestExpenseRowKeepsExpandedState(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	e, err := srv.store.CreateExpense(ctx, store.Expense{
		HouseholdID: active.ID,
		Name:        "Miete",
		AmountCents: 120000,
		Frequency:   store.FreqMonthly,
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
	}, nil)
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	path := "/expenses/" + strconv.FormatInt(e.ID, 10)

	open := post(t, h, path, url.Values{"name": {"Miete"}, "amount": {"1200"}, "expanded": {"1"}})
	if !strings.Contains(open.Body.String(), "<details class=\"exp\" open") {
		t.Error("expanded row was rendered collapsed")
	}

	closed := post(t, h, path, url.Values{"name": {"Miete"}, "amount": {"1200"}, "expanded": {"0"}})
	if strings.Contains(closed.Body.String(), "<details class=\"exp\" open") {
		t.Error("collapsed row was rendered expanded")
	}
}
