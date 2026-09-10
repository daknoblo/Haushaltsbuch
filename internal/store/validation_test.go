package store

import (
	"errors"
	"testing"
)

func TestValidateDateRange(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, from, until string
		valid             bool
	}{
		{"open", "", "", true},
		{"open start", "", "2026-01-31", true},
		{"open end", "2026-01-01", "", true},
		{"same day", "2026-01-01", "2026-01-01", true},
		{"leap day", "2028-02-29", "2028-03-01", true},
		{"reversed", "2026-12-01", "2026-01-31", false},
		{"bad start", "2026-02-29", "", false},
		{"bad end", "", "2026-04-31", false},
		{"month not date", "2026-01", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDateRange(tc.from, tc.until)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v, want ErrInvalid", err)
			}
		})
	}
}

func TestWritesRejectInvalidDateRangesWithoutChanges(t *testing.T) {
	s, ctx, h := seededStore(t)
	cat := firstExpenseCategory(ctx, t, s, h)
	b := newBooking(h, cat, 5000)
	b.EndsOn = "2026-12-31"
	created, err := s.CreateBooking(ctx, b, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.EndsOn = "2025-12-31"
	if _, err := s.CreateBooking(ctx, b, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create error=%v, want ErrInvalid", err)
	}
	invalid := created
	invalid.EndsOn, invalid.Name = b.EndsOn, "must not save"
	if err := s.SaveBooking(ctx, invalid, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("save error=%v, want ErrInvalid", err)
	}
	saved, err := s.GetBooking(ctx, h.ID, created.ID)
	if err != nil || saved.Name != created.Name || saved.EndsOn != created.EndsOn {
		t.Fatalf("invalid save changed booking: %+v, %v", saved, err)
	}
	o := BookingOverride{BookingID: created.ID, StartsOn: "2026-01-01", EndsOn: "2026-02-28", AmountCents: 1000}
	override, err := s.CreateOverride(ctx, h.ID, o)
	if err != nil {
		t.Fatal(err)
	}
	o.EndsOn = "2025-12-31"
	if _, err := s.CreateOverride(ctx, h.ID, o); !errors.Is(err, ErrInvalid) {
		t.Fatalf("override create error=%v, want ErrInvalid", err)
	}
	override.EndsOn = o.EndsOn
	if err := s.UpdateOverride(ctx, h.ID, override); !errors.Is(err, ErrInvalid) {
		t.Fatalf("override update error=%v, want ErrInvalid", err)
	}
	overrides, err := s.ListOverrides(ctx, h.ID, created.ID)
	if err != nil || len(overrides) != 1 || overrides[0].EndsOn != "2026-02-28" {
		t.Fatalf("invalid write changed overrides: %+v, %v", overrides, err)
	}
}

func TestExtendBookingsOnlyCarriesEligibleYearEnds(t *testing.T) {
	s, ctx, h := seededStore(t)
	cat := firstExpenseCategory(ctx, t, s, h)
	for _, tc := range []struct {
		name, end, want string
		frequency       Frequency
	}{
		{"December", "2026-12-31", "2027-12-31", FreqMonthly},
		{"legacy December", "2026-12-01", "2027-12-31", FreqMonthly},
		{"midyear stale checkbox", "2026-03-31", "2026-03-31", FreqMonthly},
		{"one-off", "2026-12-31", "2026-12-31", FreqOnce},
		{"open", "", "", FreqMonthly},
		{"later", "2028-12-31", "2028-12-31", FreqMonthly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newBooking(h, cat, 5000)
			b.EndsOn, b.Frequency = tc.end, tc.frequency
			created, err := s.CreateBooking(ctx, b, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ExtendBookings(ctx, h.ID, []int64{created.ID}, "2027-12-31"); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetBooking(ctx, h.ID, created.ID)
			if err != nil || got.EndsOn != tc.want {
				t.Fatalf("end=%q error=%v, want %q", got.EndsOn, err, tc.want)
			}
		})
	}
}
