package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

const token = "geheim"

func newTestAPI(t *testing.T) (*store.Store, http.Handler, store.Household) {
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
	return st, New(st, slog.New(slog.DiscardHandler), token).Handler(), hs[0]
}

// do issues a request with the given token, or none when it is empty.
func do(t *testing.T, h http.Handler, method, path, tok string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	r := httptest.NewRequest(method, path, &buf)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return out
}

func TestAuthMatrix(t *testing.T) {
	_, h, _ := newTestAPI(t)

	cases := []struct {
		name   string
		method string
		token  string
		want   int
	}{
		{"no token on a read", http.MethodGet, "", http.StatusUnauthorized},
		{"wrong token on a read", http.MethodGet, "falsch", http.StatusUnauthorized},
		{"right token on a read", http.MethodGet, token, http.StatusOK},
		{"no token on a write", http.MethodPost, "", http.StatusUnauthorized},
		{"wrong token on a write", http.MethodPost, "falsch", http.StatusUnauthorized},
	}
	for _, c := range cases {
		path := "/api/v1/households"
		if c.method == http.MethodPost {
			path = "/api/v1/bookings"
		}
		if got := do(t, h, c.method, path, c.token, nil).Code; got != c.want {
			t.Errorf("%s = %d, want %d", c.name, got, c.want)
		}
	}
}

// Without a token the API is not merely locked but absent, so a deployment that
// never meant to have one cannot be probed for it.
func TestAPIStaysOffWithoutAToken(t *testing.T) {
	st, _, _ := newTestAPI(t)
	h := New(st, slog.New(slog.DiscardHandler), "").Handler()

	w := do(t, h, http.MethodGet, "/api/v1/households", "irgendwas", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("disabled API = %d, want 503", w.Code)
	}
}

func TestCreateBooking(t *testing.T) {
	st, h, hh := newTestAPI(t)
	ctx := t.Context()
	members, _ := st.ListMembers(ctx, hh.ID)

	w := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name":         "Miete",
		"category":     "Miete",
		"amount":       981.50,
		"frequency":    "monthly",
		"active_from":  "2026-01-01",
		"cost_nature":  "fix",
		"budget_class": "need",
		"payer":        members[0].Name,
		"shares": []map[string]any{
			{"name": members[0].Name},
			{"name": members[1].Name},
		},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s), want 201", w.Code, w.Body.String())
	}

	got := decodeBody[bookingOut](t, w)
	if got.AmountCents != 98150 {
		t.Errorf("amount = %d, want 98150", got.AmountCents)
	}
	if got.Category != "Miete" {
		t.Errorf("category = %q, want it resolved by name", got.Category)
	}
	if got.PayerID != members[0].ID {
		t.Errorf("payer = %d, want %d", got.PayerID, members[0].ID)
	}
	if len(got.Shares) != 2 {
		t.Errorf("shares = %+v, want both members", got.Shares)
	}
}

// An external id is what keeps a job that runs twice from filing everything
// twice, so the second call has to update the first booking.
func TestExternalIDUpserts(t *testing.T) {
	st, h, hh := newTestAPI(t)
	ctx := t.Context()

	body := map[string]any{
		"external_id": "gehalt-2026-08",
		"name":        "Gehalt",
		"category":    "Gehalt",
		"direction":   "income",
		"amount":      3500,
		"frequency":   "once",
		"date":        "2026-08-28",
	}
	first := do(t, h, http.MethodPost, "/api/v1/bookings", token, body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first call = %d (%s), want 201", first.Code, first.Body.String())
	}

	body["amount"] = 3600
	second := do(t, h, http.MethodPost, "/api/v1/bookings", token, body)
	if second.Code != http.StatusOK {
		t.Fatalf("second call = %d (%s), want 200", second.Code, second.Body.String())
	}

	a := decodeBody[bookingOut](t, first)
	b := decodeBody[bookingOut](t, second)
	if a.ID != b.ID {
		t.Errorf("second call created booking %d instead of updating %d", b.ID, a.ID)
	}
	if b.AmountCents != 360000 {
		t.Errorf("amount = %d, want the updated 360000", b.AmountCents)
	}

	bookings, _ := st.ListBookings(ctx, hh.ID)
	if len(bookings) != 1 {
		t.Errorf("bookings = %d, want the one that was upserted", len(bookings))
	}
}

// The caller's own id is enough to find a booking again, so a script never has
// to remember what the app assigned.
func TestBookingReachableByExternalID(t *testing.T) {
	_, h, _ := newTestAPI(t)

	do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"external_id": "strom",
		"name":        "Strom",
		"category":    "Strom",
		"amount":      100,
		"active_from": "2026-01-01",
	})

	w := do(t, h, http.MethodGet, "/api/v1/bookings/ext:strom", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get by external id = %d (%s), want 200", w.Code, w.Body.String())
	}
	if got := decodeBody[bookingOut](t, w); got.Name != "Strom" {
		t.Errorf("name = %q, want Strom", got.Name)
	}

	if got := do(t, h, http.MethodDelete, "/api/v1/bookings/ext:strom", token, nil).Code; got != http.StatusOK {
		t.Errorf("delete by external id = %d, want 200", got)
	}
	if got := do(t, h, http.MethodGet, "/api/v1/bookings/ext:strom", token, nil).Code; got != http.StatusNotFound {
		t.Errorf("after the delete = %d, want 404", got)
	}
}

// A partial update must not quietly drop what it does not mention.
func TestUpdateKeepsWhatItDoesNotMention(t *testing.T) {
	st, h, hh := newTestAPI(t)
	ctx := t.Context()
	members, _ := st.ListMembers(ctx, hh.ID)

	created := decodeBody[bookingOut](t, do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"external_id": "miete",
		"name":        "Miete",
		"category":    "Miete",
		"amount":      1000,
		"active_from": "2026-01-01",
		"payer":       members[0].Name,
		"shares":      []map[string]any{{"name": members[0].Name}, {"name": members[1].Name}},
	}))

	w := do(t, h, http.MethodPut, "/api/v1/bookings/ext:miete", token, map[string]any{"amount": 1100})
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d (%s), want 200", w.Code, w.Body.String())
	}

	got := decodeBody[bookingOut](t, w)
	if got.AmountCents != 110000 {
		t.Errorf("amount = %d, want 110000", got.AmountCents)
	}
	if got.PayerID != created.PayerID {
		t.Errorf("payer = %d, want it kept as %d", got.PayerID, created.PayerID)
	}
	if len(got.Shares) != 2 {
		t.Errorf("shares = %+v, want the two it was created with", got.Shares)
	}
}

func TestValidationRejectsWhatItCannotUse(t *testing.T) {
	_, h, _ := newTestAPI(t)

	cases := map[string]map[string]any{
		"no name":          {"category": "Miete", "amount": 10},
		"no category":      {"name": "X", "amount": 10},
		"unknown category": {"name": "X", "category": "gibtesnicht", "amount": 10},
		"category of the other direction": {
			"name": "X", "category": "Gehalt", "direction": "expense", "amount": 10},
		"unknown frequency":    {"name": "X", "category": "Miete", "frequency": "täglich"},
		"negative amount":      {"name": "X", "category": "Miete", "amount": -5},
		"one-off without date": {"name": "X", "category": "Miete", "frequency": "once", "amount": 10},
		"unknown person":       {"name": "X", "category": "Miete", "amount": 10, "payer": "Niemand Bekanntes"},
	}
	for name, body := range cases {
		w := do(t, h, http.MethodPost, "/api/v1/bookings", token, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d (%s), want 400", name, w.Code, w.Body.String())
		}
		if decodeBody[errorBody](t, w).Error == "" {
			t.Errorf("%s came back without a reason", name)
		}
	}
}

// A misspelled field is a caller's bug, and silence about it is how a script
// ends up writing something other than what it meant.
func TestUnknownFieldIsRejected(t *testing.T) {
	_, h, _ := newTestAPI(t)

	w := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "X", "category": "Miete", "amount_cent": 100,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", w.Code)
	}
}

func TestListBookingsFiltersByMonth(t *testing.T) {
	_, h, _ := newTestAPI(t)

	do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Bonus", "category": "Gehalt", "direction": "income",
		"amount": 500, "frequency": "once", "date": "2026-06-15",
	})

	inJune := decodeBody[struct {
		Bookings []bookingOut `json:"bookings"`
	}](t, do(t, h, http.MethodGet, "/api/v1/bookings?month=2026-06", token, nil))
	if len(inJune.Bookings) != 1 {
		t.Errorf("June holds %d bookings, want 1", len(inJune.Bookings))
	}

	inJuly := decodeBody[struct {
		Bookings []bookingOut `json:"bookings"`
	}](t, do(t, h, http.MethodGet, "/api/v1/bookings?month=2026-07", token, nil))
	if len(inJuly.Bookings) != 0 {
		t.Errorf("July holds %d bookings, want none", len(inJuly.Bookings))
	}

	if got := do(t, h, http.MethodGet, "/api/v1/bookings?month=Juni", token, nil).Code; got != http.StatusBadRequest {
		t.Errorf("a month that is not YYYY-MM = %d, want 400", got)
	}
}

func TestReportAnswersWithTheFigures(t *testing.T) {
	_, h, _ := newTestAPI(t)

	do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Gehalt", "category": "Gehalt", "direction": "income",
		"amount": 3000, "frequency": "monthly", "active_from": "2026-01-01",
	})
	do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Miete", "category": "Miete", "amount": 1000,
		"frequency": "monthly", "active_from": "2026-01-01",
		"cost_nature": "fix", "budget_class": "need",
	})

	w := do(t, h, http.MethodGet, "/api/v1/report?month=2026-08", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("report = %d (%s), want 200", w.Code, w.Body.String())
	}

	got := decodeBody[reportOut](t, w)
	if got.IncomeCents != 300000 || got.ExpenseCents != 100000 {
		t.Errorf("income %d, expenses %d, want 300000 and 100000", got.IncomeCents, got.ExpenseCents)
	}
	if got.BalanceCents != 200000 {
		t.Errorf("balance = %d, want 200000", got.BalanceCents)
	}
	if got.FixedCents != 100000 {
		t.Errorf("fixed = %d, want 100000", got.FixedCents)
	}
}

func TestReferenceDataListsWhatABookingNeeds(t *testing.T) {
	_, h, _ := newTestAPI(t)

	for _, path := range []string{"/api/v1/households", "/api/v1/categories", "/api/v1/members", "/api/v1/tags"} {
		if got := do(t, h, http.MethodGet, path, token, nil).Code; got != http.StatusOK {
			t.Errorf("%s = %d, want 200", path, got)
		}
	}

	cats := decodeBody[struct {
		Categories []categoryOut `json:"categories"`
	}](t, do(t, h, http.MethodGet, "/api/v1/categories", token, nil))
	if len(cats.Categories) == 0 {
		t.Fatal("no categories, so nothing could be booked")
	}
	if cats.Categories[0].Name == "" || cats.Categories[0].Classification == "" {
		t.Errorf("a category came back incomplete: %+v", cats.Categories[0])
	}
}

func TestUnknownHouseholdIsRejected(t *testing.T) {
	_, h, _ := newTestAPI(t)

	if got := do(t, h, http.MethodGet, "/api/v1/categories?household=999999", token, nil).Code; got != http.StatusNotFound {
		t.Errorf("unknown household = %d, want 404", got)
	}
	if got := do(t, h, http.MethodGet, "/api/v1/categories?household=abc", token, nil).Code; got != http.StatusBadRequest {
		t.Errorf("a household that is not a number = %d, want 400", got)
	}
}

func TestCreateCategory(t *testing.T) {
	_, h, _ := newTestAPI(t)

	w := do(t, h, http.MethodPost, "/api/v1/categories", token, map[string]any{
		"name":  "Restaurant",
		"color": "#F59E0B",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body)
	}
	got := decodeBody[map[string]any](t, w)
	if got["name"] != "Restaurant" {
		t.Errorf("name = %v", got["name"])
	}
	if got["classification"] != "expense" {
		t.Errorf("classification = %v, want expense by default", got["classification"])
	}
	if got["color"] != "#f59e0b" {
		t.Errorf("color = %v, want the hex folded to lower case", got["color"])
	}
}

// A job that sets up its own categories has to survive a second run, so the
// name is the key: the same name gives back the same category, never a twin.
func TestCreatingTheSameCategoryTwiceKeepsOne(t *testing.T) {
	st, h, hh := newTestAPI(t)

	first := decodeBody[map[string]any](t, do(t, h, http.MethodPost, "/api/v1/categories", token,
		map[string]any{"name": "Restaurant"}))

	w := do(t, h, http.MethodPost, "/api/v1/categories", token, map[string]any{"name": "restaurant"})
	if w.Code != http.StatusOK {
		t.Fatalf("second create status = %d, want 200: %s", w.Code, w.Body)
	}
	if again := decodeBody[map[string]any](t, w); again["id"] != first["id"] {
		t.Errorf("id = %v, want the first one %v", again["id"], first["id"])
	}

	cats, err := st.ListCategories(t.Context(), hh.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var n int
	for _, c := range cats {
		if strings.EqualFold(c.Name, "Restaurant") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d categories named Restaurant, want 1", n)
	}
}

func TestCreateCategoryRefusesNonsense(t *testing.T) {
	_, h, _ := newTestAPI(t)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"no name", map[string]any{"color": "#ffffff"}},
		{"blank name", map[string]any{"name": "   "}},
		{"unknown classification", map[string]any{"name": "X", "classification": "vielleicht"}},
		{"color that is not hex", map[string]any{"name": "X", "color": "rot"}},
	}
	for _, c := range cases {
		if w := do(t, h, http.MethodPost, "/api/v1/categories", token, c.body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", c.name, w.Code)
		}
	}
}
