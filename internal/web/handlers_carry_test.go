package web

import (
	"testing"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

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
