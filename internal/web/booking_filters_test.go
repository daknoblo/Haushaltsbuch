package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestBookingFilters(t *testing.T) {
	rows := []BookingRow{
		{Month: "2026-09", Booking: store.Booking{Name: "Miete", Frequency: store.FreqMonthly}},
		{Month: "2026-09", Booking: store.Booking{Name: "Miete alt", Frequency: store.FreqMonthly, EndsOn: "2026-08-31"}},
		{Month: "2026-09", Booking: store.Booking{Name: "Gehalt August", Frequency: store.FreqOnce, StartsOn: "2026-08-28"}},
		{Month: "2026-09", Booking: store.Booking{Name: "Gehalt September", Frequency: store.FreqOnce, StartsOn: "2026-09-28"}},
		{Month: "2026-09", Booking: store.Booking{Name: "Miete neu", Frequency: store.FreqMonthly, StartsOn: "2026-10-01"}},
		{Month: "2026-09", Booking: store.Booking{Name: "Öl", Frequency: store.FreqMonthly}},
	}
	for _, tc := range []struct {
		query string
		all   bool
		want  string
	}{
		{"", false, "Miete,Gehalt September,Öl"},
		{"x", false, "Miete,Gehalt September,Öl"},
		{"Ö", false, "Miete,Gehalt September,Öl"},
		{" MI ", false, "Miete"},
		{"mi", true, "Miete,Miete alt,Miete neu"},
		{"GEHALT", true, "Gehalt August,Gehalt September"},
		{"öl", false, "Öl"},
		{"zz", true, ""},
		{"", true, "Miete,Miete alt,Gehalt August,Gehalt September,Miete neu,Öl"},
	} {
		vm := BookingsVM{Bookings: rows, Search: tc.query, ShowAll: tc.all}
		var names []string
		for _, row := range rows {
			if vm.Visible(row) {
				names = append(names, row.Booking.Name)
			}
		}
		if got := strings.Join(names, ","); got != tc.want {
			t.Errorf("query %q, all %t: %q, want %q", tc.query, tc.all, got, tc.want)
		}
		if vm.NoMatches() != (tc.want == "") {
			t.Errorf("incorrect empty state for %q", tc.query)
		}
	}
}

func TestBookingFilterURLsAndRendering(t *testing.T) {
	srv, h, hh := newTestServer(t)
	ids := plausibilityBook(t, srv, hh)
	for _, path := range []string{"/bookings", "/bookings/list"} {
		w := get(t, h, path+"?m=2026-05&q=Bonus&all=true&s=name")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, w.Code)
		}
		body := w.Body.String()
		if strings.Count(body, `data-booking-name=`) != len(ids) {
			t.Errorf("%s: client-side filtering needs all bookings in the fragment", path)
		}
		if strings.Count(body, `id="booking-list"`) != 1 {
			t.Errorf("%s: duplicate or missing list ID", path)
		}
		if !strings.Contains(body, `data-booking-name="Bonus" data-booking-active="false">`) {
			t.Errorf("%s: matching historical booking not visible", path)
		}
		if !strings.Contains(body, `data-booking-name="Miete" data-booking-active="true" hidden`) {
			t.Errorf("%s: non-matching current booking not hidden", path)
		}
	}
	vm := BookingsVM{Month: "2026-05", Sort: SortName, Search: "Miete & Öl", ShowAll: true}
	links := make([]string, 0, len(sortOrder)+2)
	links = append(links, vm.ListURL())
	for _, option := range vm.SortOptions(t.Context()) {
		links = append(links, option.URL)
	}
	nav := Nav{Path: "/bookings", BookingFilters: "&s=" + vm.Sort + vm.FilterQuery()}
	links = append(links, nav.MonthURL("2026-06"))
	for _, link := range links {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("q") != vm.Search || u.Query().Get("all") != "true" {
			t.Errorf("filters lost in %s", link)
		}
	}
	body := get(t, h, "/bookings?m=2026-05").Body.String()
	if !strings.Contains(body, `aria-pressed="true"`) ||
		!strings.Contains(body, `data-booking-name="Bonus" data-booking-active="false" hidden`) {
		t.Error("active-month filter not enabled by default")
	}
}
