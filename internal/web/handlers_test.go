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
	"slices"
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

	srv := New(st, slog.New(slog.DiscardHandler), testAPIToken)
	return srv, srv.Handler(), hs[0]
}

// testAPIToken keeps the machine-facing API reachable from the tests without
// letting it into a deployment that never set one.
const testAPIToken = "test-token"

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
	for _, path := range []string{"/", "/bookings", "/dashboard", "/settings"} {
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

	r := httptest.NewRequest(http.MethodGet, "/bookings", nil)
	r.Header.Set("Accept-Language", "en-US,en;q=0.9")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	body := w.Body.String()
	if !strings.Contains(body, `lang="en"`) {
		t.Error("the document language was not switched")
	}
	if !strings.Contains(body, "Changes are saved automatically.") {
		t.Error("English page did not use the English catalog")
	}
	if strings.Contains(body, "Änderungen werden automatisch gespeichert.") {
		t.Error("English page still contains German text")
	}
}

func TestCrossOriginPostIsRejected(t *testing.T) {
	_, h, _ := newTestServer(t)
	r := httptest.NewRequest(http.MethodPost, "/bookings/new", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", w.Code)
	}
}

// The order the list is read in is a question of the moment, so every key has
// to hold — including the tie-breaker that keeps the largest figure on top.
func TestSortBookingsOrdersByTheChosenKey(t *testing.T) {
	row := func(name, category, payer string, dir store.Direction, cents int64) BookingRow {
		return BookingRow{
			Booking: store.Booking{
				Name: name, Direction: dir, AmountCents: cents,
				Frequency: store.FreqMonthly, Interval: 1,
			},
			Category: store.Category{Name: category},
			Payer:    store.Member{Name: payer},
			Month:    "2026-05",
		}
	}
	rows := []BookingRow{
		row("miete", "Wohnen", "Nina", store.DirExpense, 100000),
		row("", "Wohnen", "Jonas", store.DirExpense, 20000),
		row("Gehalt", "Arbeit", "Jonas", store.DirIncome, 300000),
		row("Abo", "Freizeit", "Nina", store.DirExpense, 5000),
	}

	names := func(rows []BookingRow) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Booking.Name)
		}
		return out
	}

	for _, tc := range []struct {
		key  string
		want []string
	}{
		{SortDirection, []string{"Gehalt", "miete", "", "Abo"}},
		{SortAmount, []string{"Gehalt", "miete", "", "Abo"}},
		// Case is irrelevant and the nameless booking belongs at the end.
		{SortName, []string{"Abo", "Gehalt", "miete", ""}},
		{SortCategory, []string{"Gehalt", "Abo", "miete", ""}},
		{SortPayer, []string{"Gehalt", "", "miete", "Abo"}},
		{"nonsense", []string{"Gehalt", "miete", "", "Abo"}},
	} {
		got := append([]BookingRow(nil), rows...)
		sortBookings(got, tc.key)
		if diff := names(got); !slices.Equal(diff, tc.want) {
			t.Errorf("sort %q = %v, want %v", tc.key, diff, tc.want)
		}
	}
}

// newExpenseBooking stores an expense booking of the given household.
func newExpenseBooking(t *testing.T, srv *Server, householdID int64) store.Booking {
	dir := store.DirExpense
	t.Helper()
	ctx := t.Context()
	cats, err := srv.store.ListCategories(ctx, householdID)
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	var catID int64
	for _, c := range cats {
		if c.Classification == dir {
			catID = c.ID
			break
		}
	}
	if catID == 0 {
		t.Fatalf("no %s category available", dir)
	}
	members, err := srv.store.ListMembers(ctx, householdID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	b, err := srv.store.CreateBooking(ctx, store.Booking{
		HouseholdID:   householdID,
		CategoryID:    catID,
		PayerMemberID: &members[0].ID,
		Direction:     dir,
		Name:          "Miete",
		AmountCents:   120000,
		Frequency:     store.FreqMonthly,
		Interval:      1,
		DuePoint:      store.DueStart,
		StartsOn:      "2026-01-01",
		CostNature:    store.CostFix,
		BudgetClass:   store.ClassNeed,
		SplitMode:     store.SplitEqual,
		Settle:        true,
	}, nil, nil)
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	return b
}

func TestBookingMutationsAreHouseholdScoped(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	other, err := srv.store.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	if active.ID == other.ID {
		t.Fatal("test setup: households must differ")
	}
	foreign := newExpenseBooking(t, srv, other.ID)

	id := strconv.FormatInt(foreign.ID, 10)
	if got := post(t, h, "/bookings/"+id, url.Values{"name": {"gekapert"}}).Code; got != http.StatusNotFound {
		t.Errorf("update foreign booking = %d, want 404", got)
	}
	if got := post(t, h, "/bookings/"+id+"/delete", nil).Code; got != http.StatusNotFound {
		t.Errorf("delete foreign booking = %d, want 404", got)
	}

	unchanged, err := srv.store.GetBooking(ctx, other.ID, foreign.ID)
	if err != nil {
		t.Fatalf("get foreign booking: %v", err)
	}
	if unchanged.Name != "Miete" {
		t.Errorf("foreign booking was modified: %q", unchanged.Name)
	}
}

func TestBookingUpdateIgnoresForeignPayer(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	other, err := srv.store.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	foreignMembers, _ := srv.store.ListMembers(ctx, other.ID)
	own := newExpenseBooking(t, srv, active.ID)

	w := post(t, h, "/bookings/"+strconv.FormatInt(own.ID, 10), url.Values{
		"name":            {"Miete"},
		"amount":          {"100"},
		"payer_member_id": {strconv.FormatInt(foreignMembers[0].ID, 10)},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("update = %d, want 204", w.Code)
	}

	updated, err := srv.store.GetBooking(ctx, active.ID, own.ID)
	if err != nil {
		t.Fatalf("get booking: %v", err)
	}
	if updated.PayerMemberID != nil {
		t.Errorf("a payer of another household was stored: %d", *updated.PayerMemberID)
	}
}

// The dialog renders cost nature, budget class and the settlement switch only
// for an expense, so an income never submits them. Reading them anyway would
// reset all three on every keystroke, because an absent field arrives empty.
func TestIncomeUpdateKeepsFieldsItsDialogNeverShows(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	b := newExpenseBooking(t, srv, active.ID)
	b.Direction = store.DirIncome
	b.CostNature = store.CostVariable
	b.BudgetClass = store.ClassSaving
	b.Settle = true
	if err := srv.store.SaveBooking(ctx, b, nil, nil); err != nil {
		t.Fatalf("prepare income: %v", err)
	}

	w := post(t, h, "/bookings/"+strconv.FormatInt(b.ID, 10), url.Values{
		"direction": {string(store.DirIncome)},
		"name":      {"Gehalt"},
		"amount":    {"3000"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("update = %d, want 204", w.Code)
	}

	got, err := srv.store.GetBooking(ctx, active.ID, b.ID)
	if err != nil {
		t.Fatalf("get booking: %v", err)
	}
	if got.CostNature != store.CostVariable {
		t.Errorf("cost nature = %q, want it kept as %q", got.CostNature, store.CostVariable)
	}
	if got.BudgetClass != store.ClassSaving {
		t.Errorf("budget class = %q, want it kept as %q", got.BudgetClass, store.ClassSaving)
	}
	if !got.Settle {
		t.Error("settle was reset although the dialog never showed the switch")
	}
}

func TestBookingUpdateRejectsOutOfRangeAmount(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	b := newExpenseBooking(t, srv, active.ID)
	path := "/bookings/" + strconv.FormatInt(b.ID, 10)

	for _, amount := range []string{"Inf", "NaN", "1e30"} {
		if got := post(t, h, path, url.Values{"name": {"Miete"}, "amount": {amount}}).Code; got != http.StatusBadRequest {
			t.Errorf("amount %q = %d, want 400", amount, got)
		}
	}

	// A partially typed amount keeps the stored value instead of zeroing it.
	if got := post(t, h, path, url.Values{"name": {"Miete"}, "amount": {"-"}}).Code; got != http.StatusNoContent {
		t.Fatalf("partial amount = %d, want 204", got)
	}
	after, _ := srv.store.GetBooking(ctx, active.ID, b.ID)
	if after.AmountCents != 120000 {
		t.Errorf("amount = %d, want 120000", after.AmountCents)
	}
}

func TestBookingCreateSetsDirectionAndCategory(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	if got := post(t, h, "/bookings/new?direction=income", nil).Code; got != http.StatusOK {
		t.Fatalf("create income booking = %d, want 200", got)
	}
	bookings, err := srv.store.ListBookings(ctx, active.ID)
	if err != nil {
		t.Fatalf("list bookings: %v", err)
	}
	if len(bookings) != 1 {
		t.Fatalf("want 1 booking, got %d", len(bookings))
	}
	b := bookings[0]
	if b.Direction != store.DirIncome {
		t.Errorf("direction = %q, want income", b.Direction)
	}
	// A booking without a category cannot exist, so one has to be assigned.
	if b.CategoryID == 0 {
		t.Error("booking was created without a category")
	}
	cat, err := srv.store.GetCategory(ctx, active.ID, b.CategoryID)
	if err != nil {
		t.Fatalf("get category: %v", err)
	}
	if cat.Classification != store.DirIncome {
		t.Errorf("category %q is %q, want an income category", cat.Name, cat.Classification)
	}
}

func TestBookingTagsRoundTrip(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	b := newExpenseBooking(t, srv, active.ID)
	tag, err := srv.store.CreateTag(ctx, active.ID, "Urlaub", "#14b8a6")
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}

	w := post(t, h, "/bookings/"+strconv.FormatInt(b.ID, 10), url.Values{
		"name":   {"Miete"},
		"amount": {"1200"},
		"tag":    {strconv.FormatInt(tag.ID, 10)},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("update = %d, want 204", w.Code)
	}
	links, _ := srv.store.ListBookingTags(ctx, active.ID)
	if len(links[b.ID]) != 1 || links[b.ID][0] != tag.ID {
		t.Fatalf("tags = %v, want [%d]", links[b.ID], tag.ID)
	}

	// Submitting without the checkbox has to detach the tag again.
	post(t, h, "/bookings/"+strconv.FormatInt(b.ID, 10), url.Values{
		"name":   {"Miete"},
		"amount": {"1200"},
	})
	links, _ = srv.store.ListBookingTags(ctx, active.ID)
	if len(links[b.ID]) != 0 {
		t.Errorf("tags = %v, want none", links[b.ID])
	}
}

func TestDashboardRendersForEveryPeriod(t *testing.T) {
	_, h, _ := newTestServer(t)
	for _, key := range []string{"", "1m", "2m", "3m", "6m", "12m", "bogus"} {
		if got := get(t, h, "/dashboard?p="+key).Code; got != http.StatusOK {
			t.Errorf("period %q = %d, want 200", key, got)
		}
	}
}

// A single month says too little about a plan carrying yearly and quarterly
// figures, so the dashboard opens on the quarter.
func TestDashboardOpensOnTheQuarter(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := get(t, h, "/dashboard").Body.String()
	if !strings.Contains(body, `<span class="period-chip period-chip-active">Quartal</span>`) {
		t.Error("the quarter is not the period the dashboard opens on")
	}
}

func TestDashboardPersonViewNarrowsTheFigures(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()
	members, _ := srv.store.ListMembers(ctx, active.ID)

	// A stale link to somebody else's household falls back to the household view.
	if got := get(t, h, "/dashboard?view=999999").Code; got != http.StatusOK {
		t.Errorf("unknown member = %d, want 200", got)
	}
	if got := get(t, h, "/dashboard?view="+strconv.FormatInt(members[0].ID, 10)).Code; got != http.StatusOK {
		t.Errorf("person view = %d, want 200", got)
	}
}

func TestOldPagesRedirect(t *testing.T) {
	_, h, _ := newTestServer(t)
	for path, want := range map[string]string{
		"/expenses":   "/bookings",
		"/income":     "/bookings",
		"/statistics": "/dashboard",
	} {
		w := get(t, h, path)
		if w.Code != http.StatusMovedPermanently {
			t.Errorf("GET %s = %d, want 301", path, w.Code)
		}
		if got := w.Header().Get("Location"); got != want {
			t.Errorf("GET %s redirects to %q, want %q", path, got, want)
		}
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

	after, err := srv.store.GetMember(ctx, active.ID, before.ID)
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

func TestReorderMemberViaHTTP(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()
	before, _ := srv.store.ListMembers(ctx, active.ID)
	if len(before) < 2 {
		t.Fatalf("need at least 2 members, got %d", len(before))
	}

	w := post(t, h, "/members/"+strconv.FormatInt(before[1].ID, 10)+"/move?dir=up", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("move = %d, want 204", w.Code)
	}
	if w.Header().Get("HX-Refresh") != "true" {
		t.Errorf("missing HX-Refresh header")
	}

	after, _ := srv.store.ListMembers(ctx, active.ID)
	if after[0].ID != before[1].ID {
		t.Errorf("member was not moved: %d stays first", after[0].ID)
	}

	if got := post(t, h, "/members/"+strconv.FormatInt(before[1].ID, 10)+"/move?dir=sideways", nil).Code; got != http.StatusBadRequest {
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

// The dialog is what the user edits in, so creating a booking has to hand one
// back rather than a row that would need a second round trip to open.
func TestBookingCreateOpensADialog(t *testing.T) {
	_, h, _ := newTestServer(t)
	w := post(t, h, "/bookings/new?direction=expense", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("create = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<dialog") {
		t.Error("the response carries no dialog")
	}
	// Auto-save must not swap the form, otherwise typing would be interrupted.
	if !strings.Contains(body, `hx-swap="none"`) {
		t.Error("the dialog form would replace itself on save")
	}
	if strings.Contains(body, `type="submit"`) {
		t.Error("the dialog must not carry a save button")
	}
}

func TestBookingEditRendersStoredState(t *testing.T) {
	srv, h, active := newTestServer(t)
	b := newExpenseBooking(t, srv, active.ID)

	w := get(t, h, "/bookings/"+strconv.FormatInt(b.ID, 10)+"/edit")
	if w.Code != http.StatusOK {
		t.Fatalf("edit = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `value="Miete"`) {
		t.Error("the dialog does not show the stored name")
	}
	// Only a fresh draft may be thrown away by closing its dialog.
	if strings.Contains(w.Body.String(), "/discard") {
		t.Error("an existing booking must not offer to discard itself")
	}
}

// A draft nobody typed into is worth nothing, so closing its dialog has to
// leave the list as empty as it was.
func TestDiscardDropsAnUntouchedDraft(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	w := post(t, h, "/bookings/new?direction=expense", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("create = %d, want 200", w.Code)
	}
	bookings, _ := srv.store.ListBookings(ctx, active.ID)
	if len(bookings) != 1 {
		t.Fatalf("want the draft to exist, got %d bookings", len(bookings))
	}
	id := strconv.FormatInt(bookings[0].ID, 10)
	if !strings.Contains(w.Body.String(), "/bookings/"+id+"/discard") {
		t.Error("the draft dialog carries no discard URL")
	}

	if got := post(t, h, "/bookings/"+id+"/discard", nil).Code; got != http.StatusNoContent {
		t.Fatalf("discard = %d, want 204", got)
	}
	if bookings, _ = srv.store.ListBookings(ctx, active.ID); len(bookings) != 0 {
		t.Errorf("the untouched draft survived: %+v", bookings)
	}

	// Discarding twice is what the delete button leaves behind.
	if got := post(t, h, "/bookings/"+id+"/discard", nil).Code; got != http.StatusNoContent {
		t.Errorf("discard of a gone booking = %d, want 204", got)
	}
}

func TestDiscardKeepsAnythingTypedIn(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	post(t, h, "/bookings/new?direction=expense", nil)
	bookings, _ := srv.store.ListBookings(ctx, active.ID)
	id := strconv.FormatInt(bookings[0].ID, 10)

	if got := post(t, h, "/bookings/"+id, url.Values{"amount": {"12"}}).Code; got != http.StatusNoContent {
		t.Fatalf("update = %d, want 204", got)
	}
	if got := post(t, h, "/bookings/"+id+"/discard", nil).Code; got != http.StatusNoContent {
		t.Fatalf("discard = %d, want 204", got)
	}
	if bookings, _ = srv.store.ListBookings(ctx, active.ID); len(bookings) != 1 {
		t.Fatalf("a booking with an amount was discarded")
	}

	// A name alone is an edit too: the amount can still follow later.
	post(t, h, "/bookings/new?direction=expense", nil)
	bookings, _ = srv.store.ListBookings(ctx, active.ID)
	named := strconv.FormatInt(bookings[len(bookings)-1].ID, 10)
	post(t, h, "/bookings/"+named, url.Values{"name": {"Miete"}})
	post(t, h, "/bookings/"+named+"/discard", nil)
	if bookings, _ = srv.store.ListBookings(ctx, active.ID); len(bookings) != 2 {
		t.Errorf("a named booking was discarded: %+v", bookings)
	}
}

func TestOverrideChangesTheAmountForItsRange(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()
	b := newExpenseBooking(t, srv, active.ID)
	id := strconv.FormatInt(b.ID, 10)

	w := post(t, h, "/bookings/"+id+"/overrides", url.Values{
		"starts_on": {"2026-02-01"},
		"ends_on":   {"2026-04-30"},
		"amount":    {"10"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create override = %d, want 200", w.Code)
	}

	overrides, err := srv.store.ListOverrides(ctx, active.ID, b.ID)
	if err != nil {
		t.Fatalf("list overrides: %v", err)
	}
	if len(overrides) != 1 || overrides[0].AmountCents != 1000 {
		t.Fatalf("overrides = %+v", overrides)
	}

	rep, err := srv.buildMonthReport(ctx, active.ID, "2026-03")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if rep.ExpenseCents != 1000 {
		t.Errorf("March = %d, want the override amount 1000", rep.ExpenseCents)
	}
	rep, _ = srv.buildMonthReport(ctx, active.ID, "2026-06")
	if rep.ExpenseCents != 120000 {
		t.Errorf("June = %d, want the base amount 120000", rep.ExpenseCents)
	}

	if got := post(t, h, "/overrides/"+strconv.FormatInt(overrides[0].ID, 10)+"/delete",
		url.Values{"booking_id": {id}}).Code; got != http.StatusOK {
		t.Errorf("delete override = %d, want 200", got)
	}
}
