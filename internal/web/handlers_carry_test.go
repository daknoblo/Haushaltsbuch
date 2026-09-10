package web

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestCarryForwardDoesNotReviveJanuaryPredecessor(t *testing.T) {
	srv, handler, h := newTestServer(t)
	ctx := t.Context()
	year := carryYear(time.Now())
	yearText := strconv.Itoa(year)
	old := newExpenseBooking(t, srv, h.ID)
	old.AmountCents = 5000
	old.StartsOn, old.EndsOn = strconv.Itoa(year-1)+"-01-01", yearText+"-12-31"
	if err := srv.store.SaveBooking(ctx, old, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.ChangeAmountFrom(ctx, h.ID, old.ID, yearText+"-01-01", 6000); err != nil {
		t.Fatal(err)
	}
	cats, err := srv.store.ListCategories(ctx, h.ID)
	if err != nil {
		t.Fatal(err)
	}
	vm, err := srv.buildCarryVM(ctx, h.ID, cats)
	if err != nil {
		t.Fatal(err)
	}
	if vm.Pending() {
		t.Fatal("price predecessor was offered for annual carry")
	}
	// A stale checkbox must also be harmless when submitted directly.
	w := post(t, handler, "/settings/carry-forward", url.Values{
		"year": {yearText}, "booking": {strconv.FormatInt(old.ID, 10)},
	})
	if w.Code != http.StatusNoContent {
		t.Fatalf("carry HTTP=%d: %s", w.Code, w.Body.String())
	}
	report, err := srv.buildMonthReport(ctx, h.ID, yearText+"-01")
	if err != nil {
		t.Fatal(err)
	}
	if report.ExpenseCents != 6000 {
		t.Errorf("January costs=%d cents, want 6000", report.ExpenseCents)
	}
}

func TestCarryYearFollowsTheCalendar(t *testing.T) {
	t.Parallel()

	cases := []struct {
		when string
		want int
	}{
		{"2026-01-15", 2026},
		{"2026-06-30", 2026},
		{"2026-10-31", 2026},
		{"2026-11-01", 2027},
		{"2026-12-24", 2027},
	}
	for _, c := range cases {
		when, err := time.Parse("2006-01-02", c.when)
		if err != nil {
			t.Fatalf("parse %s: %v", c.when, err)
		}
		if got := carryYear(when); got != c.want {
			t.Errorf("carryYear(%s) = %d, want %d", c.when, got, c.want)
		}
	}
}

func TestOnlyBookingsRunningOutAtYearEndAreOffered(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		b    store.Booking
		want bool
	}{
		{"ends last year", store.Booking{Frequency: store.FreqMonthly, EndsOn: "2026-12-31"}, true},
		{"an older book ending on the first", store.Booking{Frequency: store.FreqMonthly, EndsOn: "2026-12-01"}, true},
		{"already carried", store.Booking{Frequency: store.FreqMonthly, EndsOn: "2027-12-31"}, false},
		{"runs on forever", store.Booking{Frequency: store.FreqMonthly, EndsOn: ""}, false},
		{"a one-off", store.Booking{Frequency: store.FreqOnce, EndsOn: "2026-12-31"}, false},
		{"closed off by a change", store.Booking{Frequency: store.FreqMonthly, EndsOn: "2026-03-31"}, false},
		{"retired by a January change", store.Booking{Frequency: store.FreqMonthly, EndsOn: "2026-12-31", Retired: true}, false},
	}
	for _, c := range cases {
		if got := carriable(c.b, 2027); got != c.want {
			t.Errorf("%s: carriable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidUntilMeansTheEndOfTheMonth(t *testing.T) {
	t.Parallel()

	cases := []struct{ month, want string }{
		{"2026-12", "2026-12-31"},
		{"2026-02", "2026-02-28"},
		{"2028-02", "2028-02-29"},
		{"2026-04", "2026-04-30"},
		{"", ""},
		{"nonsense", ""},
	}
	for _, c := range cases {
		if got := monthEnd(c.month); got != c.want {
			t.Errorf("monthEnd(%q) = %q, want %q", c.month, got, c.want)
		}
	}
}
