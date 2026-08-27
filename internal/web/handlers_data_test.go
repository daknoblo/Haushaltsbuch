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
