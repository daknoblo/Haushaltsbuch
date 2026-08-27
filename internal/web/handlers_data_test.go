package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postFile issues a same-origin multipart POST, which is how a backup arrives.
func postFile(t *testing.T, h http.Handler, path, field, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, path, &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The backup is only worth offering if the file it hands out can be handed
// back, so this walks the whole way round through HTTP.
func TestBackupRoundTripsThroughHTTP(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()
	newExpenseBooking(t, srv, active.ID)

	dl := get(t, h, "/settings/backup.json")
	if dl.Code != http.StatusOK {
		t.Fatalf("download = %d, want 200", dl.Code)
	}
	if ct := dl.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q, want JSON", ct)
	}
	backup := dl.Body.Bytes()

	if got := post(t, h, "/settings/reset", nil).Code; got != http.StatusNoContent {
		t.Fatalf("reset = %d, want 204", got)
	}
	if bookings, _ := srv.store.ListBookings(ctx, active.ID); len(bookings) != 0 {
		t.Fatalf("the reset left %d bookings behind", len(bookings))
	}

	if got := postFile(t, h, "/settings/restore", "snapshot", "backup.json", backup).Code; got != http.StatusNoContent {
		t.Fatalf("restore = %d, want 204", got)
	}
	bookings, err := srv.store.ListBookings(ctx, active.ID)
	if err != nil {
		t.Fatalf("list bookings: %v", err)
	}
	if len(bookings) != 1 || bookings[0].Name != "Miete" {
		t.Errorf("the booking did not come back: %+v", bookings)
	}
}

func TestRestoreRejectsWhatItCannotRead(t *testing.T) {
	_, h, _ := newTestServer(t)

	cases := map[string][]byte{
		"no JSON at all":     []byte("kein json"),
		"an unknown version": []byte(`{"version":99,"households":[{"household":{"ID":1,"Name":"x"}}]}`),
		"no household in it": []byte(`{"version":1,"households":[]}`),
	}
	for name, content := range cases {
		if got := postFile(t, h, "/settings/restore", "snapshot", "b.json", content).Code; got != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", name, got)
		}
	}
	if got := post(t, h, "/settings/restore", nil).Code; got != http.StatusBadRequest {
		t.Errorf("a request without a file = %d, want 400", got)
	}
}

// Clearing the bookings is the gentler of the two, so it has to leave the setup
// standing.
func TestResetBookingsKeepsTheSetupOverHTTP(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()
	newExpenseBooking(t, srv, active.ID)
	before, _ := srv.store.ListCategories(ctx, active.ID)

	if got := post(t, h, "/settings/reset-bookings", nil).Code; got != http.StatusNoContent {
		t.Fatalf("reset = %d, want 204", got)
	}

	if bookings, _ := srv.store.ListBookings(ctx, active.ID); len(bookings) != 0 {
		t.Errorf("bookings = %d, want none", len(bookings))
	}
	after, _ := srv.store.ListCategories(ctx, active.ID)
	if len(after) != len(before) {
		t.Errorf("categories = %d, want the original %d", len(after), len(before))
	}
}

// Wiping the book is the one request worth checking twice against a forged
// cross-site post.
func TestResetRejectsACrossSitePost(t *testing.T) {
	srv, h, active := newTestServer(t)
	ctx := t.Context()

	for _, path := range []string{"/settings/reset", "/settings/reset-bookings", "/settings/restore"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s from another site = %d, want 403", path, w.Code)
		}
	}

	households, err := srv.store.ListHouseholds(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(households) == 0 {
		t.Fatal("a cross-site request emptied the book")
	}
	if _, err := srv.store.GetHousehold(ctx, active.ID); err != nil {
		t.Errorf("the active household is gone: %v", err)
	}
}

// The file names the version it was written for, so a future layout change can
// tell an old file apart instead of importing it into the wrong shape.
func TestBackupNamesItsVersion(t *testing.T) {
	_, h, _ := newTestServer(t)

	var snap struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(get(t, h, "/settings/backup.json").Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.Version == 0 {
		t.Error("the backup carries no version")
	}
}

// The API answers a bearer token, not a cookie, so the same-origin guard would
// only turn away callers it was never meant to protect against. The HTML routes
// keep it, and this holds the two apart.
func TestAPIIsExemptFromTheSameOriginGuard(t *testing.T) {
	_, h, _ := newTestServer(t)

	api := httptest.NewRequest(http.MethodPost, "/api/v1/bookings", strings.NewReader(`{"name":"X"}`))
	api.Header.Set("Content-Type", "application/json")
	api.Header.Set("Authorization", "Bearer "+testAPIToken)
	api.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, api)
	if w.Code == http.StatusForbidden {
		t.Error("the same-origin guard rejected an API call carrying a valid token")
	}

	page := httptest.NewRequest(http.MethodPost, "/settings/reset", strings.NewReader(""))
	page.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	page.Header.Set("Sec-Fetch-Site", "cross-site")
	pw := httptest.NewRecorder()
	h.ServeHTTP(pw, page)
	if pw.Code != http.StatusForbidden {
		t.Errorf("an HTML route from another site = %d, want 403", pw.Code)
	}
}

// A token is what stands between the API and the network, so an unauthenticated
// call must not reach a handler even from the same origin.
func TestAPIRefusesWithoutAToken(t *testing.T) {
	srv, h, _ := newTestServer(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/households", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated API call = %d, want 401", w.Code)
	}

	if households, err := srv.store.ListHouseholds(t.Context()); err != nil || len(households) == 0 {
		t.Fatalf("the book was disturbed: %v", err)
	}
}
