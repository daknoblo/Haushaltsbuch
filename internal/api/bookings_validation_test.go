package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestBookingResponsesApplyOverrides(t *testing.T) {
	st, h, hh := newTestAPI(t)
	created := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Override", "category": "Miete", "amount_cents": 5000, "external_id": "override",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	b := decodeBody[bookingOut](t, created)
	if b.MonthlyCents != 5000 {
		t.Fatalf("create monthly amount = %d, want 5000", b.MonthlyCents)
	}
	for _, amount := range []int64{1000, 900} {
		if _, err := st.CreateOverride(t.Context(), hh.ID, store.BookingOverride{
			BookingID: b.ID, AmountCents: amount,
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/bookings/" + strconv.FormatInt(b.ID, 10)
	for _, tc := range []struct {
		name, method, path string
		body               map[string]any
	}{
		{"get", http.MethodGet, path, nil},
		{"get external id", http.MethodGet, "/api/v1/bookings/ext:override", nil},
		{"update", http.MethodPut, path, map[string]any{"note": "updated"}},
		{"upsert", http.MethodPost, "/api/v1/bookings", map[string]any{"external_id": "override", "note": "upserted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := do(t, h, tc.method, tc.path, token, tc.body)
			if w.Code != http.StatusOK {
				t.Fatalf("response = %d: %s", w.Code, w.Body.String())
			}
			got := decodeBody[bookingOut](t, w)
			if got.MonthlyCents != 900 || got.AmountCents != 5000 {
				t.Errorf("amounts = monthly %d, base %d; want 900, 5000", got.MonthlyCents, got.AmountCents)
			}
		})
	}
	for _, query := range []string{"", "?month=" + time.Now().Format("2006-01")} {
		w := do(t, h, http.MethodGet, "/api/v1/bookings"+query, token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("list = %d: %s", w.Code, w.Body.String())
		}
		got := decodeBody[struct {
			Bookings []bookingOut `json:"bookings"`
		}](t, w)
		if len(got.Bookings) != 1 || got.Bookings[0].MonthlyCents != 900 {
			t.Errorf("list %q = %+v", query, got.Bookings)
		}
	}
	report := do(t, h, http.MethodGet, "/api/v1/report", token, nil)
	if report.Code != http.StatusOK {
		t.Fatalf("report = %d: %s", report.Code, report.Body.String())
	}
	got := decodeBody[struct {
		Expense int64 `json:"expense_cents"`
	}](t, report)
	if got.Expense != 900 {
		t.Errorf("report expense = %d, want 900", got.Expense)
	}
}

func TestBookingListUsesRequestedOverrideMonthAndTags(t *testing.T) {
	st, h, hh := newTestAPI(t)
	created := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Override", "category": "Miete", "amount_cents": 5000,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	b := decodeBody[bookingOut](t, created)
	_, err := st.CreateOverride(t.Context(), hh.ID, store.BookingOverride{
		BookingID: b.ID, StartsOn: "2026-02-01", EndsOn: "2026-04-30", AmountCents: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := st.CreateTag(t.Context(), hh.ID, "Override tag", "#2563eb")
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, h, http.MethodPut, "/api/v1/bookings/"+strconv.FormatInt(b.ID, 10), token,
		map[string]any{"tags": []string{tag.Name}})
	if w.Code != http.StatusOK {
		t.Fatalf("set tags = %d: %s", w.Code, w.Body.String())
	}
	for month, want := range map[string]int64{"2026-01": 5000, "2026-02": 1000, "2026-04": 1000, "2026-05": 5000} {
		w := do(t, h, http.MethodGet, "/api/v1/bookings?month="+month, token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("list = %d: %s", w.Code, w.Body.String())
		}
		got := decodeBody[struct {
			Bookings []bookingOut `json:"bookings"`
		}](t, w)
		if len(got.Bookings) != 1 {
			t.Fatalf("list = %+v", got.Bookings)
		}
		if got.Bookings[0].MonthlyCents != want {
			t.Errorf("%s = %d, want %d", month, got.Bookings[0].MonthlyCents, want)
		}
		if len(got.Bookings[0].Tags) != 1 || got.Bookings[0].Tags[0] != tag.ID {
			t.Errorf("tags = %v, want %d", got.Bookings[0].Tags, tag.ID)
		}
	}
}

func TestBookingWritesValidateResultingCategory(t *testing.T) {
	st, h, hh := newTestAPI(t)
	for _, method := range []string{http.MethodPut, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			ext := "direction-" + method
			created := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
				"name": "Miete", "category": "Miete", "external_id": ext, "amount_cents": 5000,
			})
			if created.Code != http.StatusCreated {
				t.Fatalf("create = %d: %s", created.Code, created.Body.String())
			}
			b := decodeBody[bookingOut](t, created)
			path := "/api/v1/bookings"
			if method == http.MethodPut {
				path += "/" + strconv.FormatInt(b.ID, 10)
			}
			body := map[string]any{"external_id": ext, "direction": "income"}
			w := do(t, h, method, path, token, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("direction-only write = %d: %s", w.Code, w.Body.String())
			}
			stored, err := st.GetBooking(t.Context(), hh.ID, b.ID)
			if err != nil || stored.Direction != store.DirExpense || stored.CategoryID != b.CategoryID {
				t.Fatalf("rejected write changed booking: %+v, %v", stored, err)
			}
			body["category"] = "Gehalt"
			w = do(t, h, method, path, token, body)
			if w.Code != http.StatusOK {
				t.Fatalf("valid paired change = %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestBookingWritesValidateResultingDates(t *testing.T) {
	st, h, hh := newTestAPI(t)
	invalid := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Invalid", "category": "Miete", "active_from": "2026-05-01", "active_until": "2026-04-30",
	})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("reversed create = %d: %s", invalid.Code, invalid.Body.String())
	}
	bookings, err := st.ListBookings(t.Context(), hh.ID)
	if err != nil || len(bookings) != 0 {
		t.Fatalf("rejected create persisted: %+v, %v", bookings, err)
	}
	created := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Valid", "category": "Miete", "external_id": "range",
		"active_from": "2026-02-01", "active_until": "2026-04-30",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	b := decodeBody[bookingOut](t, created)
	path := "/api/v1/bookings/" + strconv.FormatInt(b.ID, 10)
	for _, body := range []map[string]any{
		{"active_from": "2026-05-01"}, {"active_until": "2026-01-31"},
		{"active_from": "2026-02-30"}, {"active_until": "2026-13-01"},
	} {
		for _, method := range []string{http.MethodPut, http.MethodPost} {
			target := path
			if method == http.MethodPost {
				target = "/api/v1/bookings"
				body["external_id"] = "range"
			}
			w := do(t, h, method, target, token, body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s %v = %d: %s", method, body, w.Code, w.Body.String())
			}
			stored, err := st.GetBooking(t.Context(), hh.ID, b.ID)
			if err != nil || stored.StartsOn != b.StartsOn || stored.EndsOn != b.EndsOn {
				t.Fatalf("rejected write changed dates: %+v, %v", stored, err)
			}
		}
	}
	for _, body := range []map[string]any{
		{"active_from": "2026-04-30"}, {"active_from": ""}, {"active_until": ""},
	} {
		if w := do(t, h, http.MethodPut, path, token, body); w.Code != http.StatusOK {
			t.Errorf("valid range %v = %d: %s", body, w.Code, w.Body.String())
		}
	}
}

func TestBookingRetiredMarkerIsReadOnly(t *testing.T) {
	st, h, hh := newTestAPI(t)
	created := do(t, h, http.MethodPost, "/api/v1/bookings", token, map[string]any{
		"name": "Miete", "category": "Miete", "active_from": "2026-01-01", "active_until": "2026-12-31",
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	b := decodeBody[bookingOut](t, created)
	if _, err := st.ChangeAmountFrom(t.Context(), hh.ID, b.ID, "2026-05-01", 6000); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/bookings/" + strconv.FormatInt(b.ID, 10)
	w := do(t, h, http.MethodPut, path, token, map[string]any{"note": "history"})
	if w.Code != http.StatusOK || !decodeBody[bookingOut](t, w).Retired {
		t.Fatalf("update lost retirement marker: %d %s", w.Code, w.Body.String())
	}
	w = do(t, h, http.MethodPut, path, token, map[string]any{"retired": false})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("writable retirement marker: %d %s", w.Code, w.Body.String())
	}
}
