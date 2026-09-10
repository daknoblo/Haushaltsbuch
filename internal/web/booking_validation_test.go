package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestBookingAutosaveValidatesDateRange(t *testing.T) {
	srv, h, hh := newTestServer(t)
	b := newExpenseBooking(t, srv, hh.ID)
	b.StartsOn, b.EndsOn = "2026-02-01", "2026-04-30"
	if err := srv.store.SaveBooking(t.Context(), b, nil, nil); err != nil {
		t.Fatal(err)
	}
	path := "/bookings/" + strconv.FormatInt(b.ID, 10)
	for _, tc := range []struct {
		name, from, until, wantFrom, wantUntil string
		status                                 int
	}{
		{"reversed", "2026-05", "2026-04", b.StartsOn, b.EndsOn, http.StatusBadRequest},
		{"invalid month", "2026-13", "2026-04", b.StartsOn, b.EndsOn, http.StatusBadRequest},
		{"partial month", "2026-0", "2026-04", b.StartsOn, b.EndsOn, http.StatusNoContent},
		{"same month", "2026-04", "2026-04", "2026-04-01", "2026-04-30", http.StatusNoContent},
		{"open start", "", "2026-04", "", "2026-04-30", http.StatusNoContent},
		{"open end", "2026-02", "", "2026-02-01", "", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := srv.store.SaveBooking(t.Context(), b, nil, nil); err != nil {
				t.Fatal(err)
			}
			w := post(t, h, path, url.Values{
				"name": {"Miete"}, "amount": {"-"}, "recurring": {"on"}, "frequency": {"monthly"},
				"active_from": {tc.from}, "active_until": {tc.until},
			})
			if w.Code != tc.status {
				t.Fatalf("response = %d: %s, want %d", w.Code, w.Body.String(), tc.status)
			}
			if tc.status == http.StatusBadRequest &&
				(strings.TrimSpace(w.Body.String()) == "" || strings.Contains(w.Body.String(), "error.invalidDateRange") ||
					w.Header().Get("HX-Trigger") != "") {
				t.Errorf("invalid autosave needs explicit error, no changed event: %+v %q", w.Header(), w.Body.String())
			}
			stored, err := srv.store.GetBooking(t.Context(), hh.ID, b.ID)
			if err != nil || stored.StartsOn != tc.wantFrom || stored.EndsOn != tc.wantUntil || stored.AmountCents != b.AmountCents {
				t.Fatalf("stored = %+v, %v; want %s..%s, original amount", stored, err, tc.wantFrom, tc.wantUntil)
			}
		})
	}
}

func TestBookingDatesKeepIncompleteOrOmittedFields(t *testing.T) {
	for _, tc := range []struct {
		name      string
		frequency store.Frequency
		form      url.Values
	}{
		{"omitted recurring bounds", store.FreqMonthly, url.Values{}},
		{"partial end month", store.FreqMonthly, url.Values{"active_until": {"2026-"}}},
		{"partial one-off date", store.FreqOnce, url.Values{"occurred_on": {"2026-02-"}}},
		{"omitted one-off date", store.FreqOnce, url.Values{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := store.Booking{Frequency: tc.frequency, StartsOn: "2026-02-01"}
			r := &http.Request{Form: tc.form}
			if err := bookingDatesFromForm(r, &b); err != nil || b.StartsOn != "2026-02-01" || b.EndsOn != "" {
				t.Errorf("date handling = %+v, %v", b, err)
			}
		})
	}
}

func TestOverrideWritesRejectInvalidDateRanges(t *testing.T) {
	srv, h, hh := newTestServer(t)
	b := newExpenseBooking(t, srv, hh.ID)
	o, err := srv.store.CreateOverride(t.Context(), hh.ID, store.BookingOverride{
		BookingID: b.ID, StartsOn: "2026-02-01", EndsOn: "2026-04-30", AmountCents: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, dates := range [][2]string{
		{"2026-05-01", "2026-04-30"}, {"2026-02-30", "2026-04-30"}, {"2026-02-01", "not-a-date"},
	} {
		for _, path := range []string{
			"/bookings/" + strconv.FormatInt(b.ID, 10) + "/overrides",
			"/overrides/" + strconv.FormatInt(o.ID, 10),
		} {
			w := post(t, h, path, url.Values{
				"starts_on": {dates[0]}, "ends_on": {dates[1]}, "amount": {"20"},
			})
			if w.Code != http.StatusBadRequest || strings.TrimSpace(w.Body.String()) == "" ||
				strings.Contains(w.Body.String(), "error.invalidDateRange") {
				t.Errorf("%s %v = %d: %s", path, dates, w.Code, w.Body.String())
			}
			stored, err := srv.store.ListOverrides(t.Context(), hh.ID, b.ID)
			if err != nil || len(stored) != 1 {
				t.Fatalf("overrides = %+v, %v", stored, err)
			}
			if stored[0].StartsOn != o.StartsOn || stored[0].EndsOn != o.EndsOn || stored[0].AmountCents != o.AmountCents {
				t.Errorf("invalid write changed override: %+v", stored[0])
			}
		}
	}
}

func TestOverrideDateRangeAllowsEqualAndOpenBounds(t *testing.T) {
	for _, dates := range [][2]string{
		{"2026-02-01", "2026-02-01"}, {"", "2026-02-01"}, {"2026-02-01", ""}, {"", ""},
	} {
		r := &http.Request{Form: url.Values{"starts_on": {dates[0]}, "ends_on": {dates[1]}, "amount": {"10"}}}
		got, err := overrideFromForm(r)
		if err != nil || got.StartsOn != dates[0] || got.EndsOn != dates[1] {
			t.Errorf("%v = %+v, %v", dates, got, err)
		}
	}
}

func TestBookingAutosaveRejectsMismatchedDirection(t *testing.T) {
	srv, h, hh := newTestServer(t)
	b := newExpenseBooking(t, srv, hh.ID)
	path := "/bookings/" + strconv.FormatInt(b.ID, 10)
	w := post(t, h, path, url.Values{
		"name": {"Miete"}, "amount": {"1200"}, "direction": {"income"},
		"recurring": {"on"}, "frequency": {"monthly"}, "category": {"Miete"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("mismatched category = %d: %s", w.Code, w.Body.String())
	}
	stored, err := srv.store.GetBooking(t.Context(), hh.ID, b.ID)
	if err != nil || stored.Direction != b.Direction || stored.CategoryID != b.CategoryID {
		t.Fatalf("invalid write changed booking: %+v, %v", stored, err)
	}
	w = post(t, h, path, url.Values{
		"name": {"Gehalt"}, "amount": {"1200"}, "direction": {"income"},
		"recurring": {"on"}, "frequency": {"monthly"}, "category": {"Gehalt"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("matching category = %d: %s", w.Code, w.Body.String())
	}
}

func TestBookingAutosavePreservesRetiredMarker(t *testing.T) {
	srv, h, hh := newTestServer(t)
	b := newExpenseBooking(t, srv, hh.ID)
	b.StartsOn, b.EndsOn = "2026-01-01", "2026-12-31"
	if err := srv.store.SaveBooking(t.Context(), b, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.ChangeAmountFrom(t.Context(), hh.ID, b.ID, "2026-05-01", 130000); err != nil {
		t.Fatal(err)
	}
	w := post(t, h, "/bookings/"+strconv.FormatInt(b.ID, 10), url.Values{
		"name": {"Miete"}, "note": {"Historical price"}, "amount": {"1200"},
		"recurring": {"on"}, "frequency": {"monthly"},
		"active_from": {"2026-01"}, "active_until": {"2026-04"},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("autosave = %d: %s", w.Code, w.Body.String())
	}
	stored, err := srv.store.GetBooking(t.Context(), hh.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Retired || stored.Note != "Historical price" {
		t.Fatalf("autosave did not preserve retirement marker: %+v", stored)
	}
}
