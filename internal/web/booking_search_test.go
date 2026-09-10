package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestBookingSearchCoversValuesAcrossFields(t *testing.T) {
	payer := int64(1)
	row := BookingRow{
		Booking: store.Booking{
			ID: 42, Name: "Telekom", Note: "Glasfaseranschluss", ExternalID: "vertrag-tk",
			Direction: store.DirExpense, AmountCents: 123456, Frequency: store.FreqQuarterly,
			Interval: 2, DuePoint: store.DueMiddle, CostNature: store.CostVariable,
			BudgetClass: store.ClassWant, SplitMode: store.SplitPercent, Settle: true,
			PayerMemberID: &payer, StartsOn: "2026-02-01", EndsOn: "2027-02-28",
		},
		Category: store.Category{Name: "Kommunikation", Icon: "phone", Color: "#123456"},
		Payer:    store.Member{ID: 1, Name: "Anna"},
		Members:  []store.Member{{ID: 1, Name: "Anna"}, {ID: 2, Name: "Ben"}, {ID: 3, Name: "Clara"}},
		Carriers: []store.Member{{ID: 1, Name: "Anna"}, {ID: 2, Name: "Ben"}},
		Splits:   []store.BookingSplit{{MemberID: 1, Value: 60}, {MemberID: 2, Value: 40}},
		TagIDs:   []int64{5},
		Month:    "2026-09",
		Overrides: []store.BookingOverride{
			{StartsOn: "2026-09-01", EndsOn: "2026-10-31", AmountCents: 50000, Note: "Neukundenrabatt"},
		},
	}
	row.Search = bookingSearch(t.Context(), row, map[int64]store.Tag{
		5: {Name: "Internet"},
		6: {Name: "Unbenutzt"},
	})
	for _, query := range []string{
		"telekom", "KOMMUNIKATION", "glasfaser", "vertrag-tk", "internet",
		"Anna", "Ben", "1234.56", "1234,56", "1.234,56", "500,00", "83.33",
		"2027-02-28", "28.02.2027", "Februar", "vierteljährlich", "quarterly",
		"Monatsmitte", CostNatureLabel(t.Context(), store.CostVariable), "Vergnügen", "Prozentual", "60 %",
		"Neukundenrabatt", "31.10.2026", "kommunikation ben", "  INTERNET   anna  ",
	} {
		if !(BookingsVM{Search: query}).Visible(row) {
			t.Errorf("query %q did not match indexed values", query)
		}
	}
	for _, query := range []string{"Clara", "Unbenutzt", "telekom versicherung", "not-found"} {
		if (BookingsVM{Search: query}).Visible(row) {
			t.Errorf("unrelated query %q matched", query)
		}
	}
	encoded, err := row.Search.SuggestionsJSON()
	if err != nil {
		t.Fatal(err)
	}
	var suggestions []string
	if err := json.Unmarshal([]byte(encoded), &suggestions); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Telekom", "Kommunikation", "Internet", "Anna", "Ben"} {
		if !slices.Contains(suggestions, value) {
			t.Errorf("completion %q missing", value)
		}
	}
}

func TestBookingSearchUsesFixedAmounts(t *testing.T) {
	row := BookingRow{
		Booking: store.Booking{Frequency: store.FreqMonthly, SplitMode: store.SplitFixed},
		Splits:  []store.BookingSplit{{MemberID: 1, Value: 45678}},
		Month:   "2026-09",
	}
	row.Search = bookingSearch(t.Context(), row, nil)
	if !(BookingsVM{Search: "456,78"}).Visible(row) {
		t.Error("fixed member amount is not searchable")
	}
}

func TestBookingSearchCategoryAndNoteThroughHTTP(t *testing.T) {
	srv, h, hh := newTestServer(t)
	b := newExpenseBooking(t, srv, hh.ID)
	b.Name, b.Note = "Wohnung Altbau", "Familienwohnung"
	if err := srv.store.SaveBooking(t.Context(), b, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/bookings", "/bookings/list"} {
		w := get(t, h, path+"?m=2026-09&all=true&q="+url.QueryEscape("Miete Familienwohnung"))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `data-booking-name="Wohnung Altbau" data-booking-active="true">`) {
			t.Errorf("%s: category/note-only match is not visible", path)
		}
		if !strings.Contains(body, "data-booking-search=") || !strings.Contains(body, "data-booking-suggestions=") {
			t.Errorf("%s: client-side index missing", path)
		}
	}
}

func TestBookingToolbarOrderAndCheckbox(t *testing.T) {
	_, h, _ := newTestServer(t)
	body := get(t, h, "/bookings").Body.String()
	sort := strings.Index(body, `class="booking-sort-options"`)
	search := strings.Index(body, `id="booking-search"`)
	checkbox := strings.Index(body, `type="checkbox" id="booking-active-filter" checked`)
	if sort < 0 || search < sort || checkbox < search {
		t.Error("toolbar must order sorting, search, checked month checkbox")
	}
	if strings.Contains(body, `aria-pressed=`) {
		t.Error("month filter is still a toggle button")
	}
}
