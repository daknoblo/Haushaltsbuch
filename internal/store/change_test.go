package store

import "testing"

// The case this exists for: 50 € of electricity from January, put up to 60 € in
// April. The old price has to stay in the book with the months it applied to,
// or the year says the household always paid 60.
func TestChangeAmountSplitsTheBookingInTwo(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h, _ := s.ActiveHouseholdID(ctx)
	cats, _ := s.ListCategories(ctx, h)
	members, _ := s.ListMembers(ctx, h)

	old, err := s.CreateBooking(ctx, Booking{
		HouseholdID: h, CategoryID: cats[0].ID, PayerMemberID: &members[0].ID,
		Direction: DirExpense, Name: "Strom", AmountCents: 5000,
		Frequency: FreqMonthly, Interval: 1, DuePoint: DueStart,
		StartsOn: "2026-01-01", EndsOn: "2026-12-31",
		CostNature: CostFix, BudgetClass: ClassNeed, SplitMode: SplitEqual, Settle: true,
	}, []SplitInput{{MemberID: members[0].ID}, {MemberID: members[1].ID}}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	next, err := s.ChangeAmountFrom(ctx, h, old.ID, "2026-04-01", 6000)
	if err != nil {
		t.Fatalf("change: %v", err)
	}

	closed, err := s.GetBooking(ctx, h, old.ID)
	if err != nil {
		t.Fatalf("get old: %v", err)
	}
	if closed.EndsOn != "2026-03-31" {
		t.Errorf("the old booking ends %q, want 2026-03-31", closed.EndsOn)
	}
	if closed.AmountCents != 5000 {
		t.Errorf("the old amount became %d, want it kept at 5000", closed.AmountCents)
	}

	if next.StartsOn != "2026-04-01" {
		t.Errorf("the successor starts %q, want 2026-04-01", next.StartsOn)
	}
	if next.EndsOn != "2026-12-31" {
		t.Errorf("the successor ends %q, want the original 2026-12-31", next.EndsOn)
	}
	if next.AmountCents != 6000 {
		t.Errorf("the successor carries %d, want 6000", next.AmountCents)
	}
	if next.Name != "Strom" || next.CategoryID != old.CategoryID {
		t.Errorf("the successor is not the same booking: %+v", next)
	}
	if next.PayerMemberID == nil || *next.PayerMemberID != members[0].ID {
		t.Error("the successor lost its payer")
	}

	splits, _ := s.ListSplits(ctx, h, next.ID)
	if len(splits) != 2 {
		t.Errorf("the successor carries %d shares, want the original 2", len(splits))
	}
}

// An external id is unique per household, so the successor cannot inherit the
// one its predecessor was filed under.
func TestChangeAmountLeavesTheExternalIDBehind(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h, _ := s.ActiveHouseholdID(ctx)
	cats, _ := s.ListCategories(ctx, h)

	old, err := s.CreateBooking(ctx, Booking{
		HouseholdID: h, CategoryID: cats[0].ID, Direction: DirExpense,
		Name: "Strom", AmountCents: 5000, Frequency: FreqMonthly, Interval: 1,
		DuePoint: DueStart, StartsOn: "2026-01-01", CostNature: CostFix,
		BudgetClass: ClassNeed, SplitMode: SplitEqual, ExternalID: "strom",
	}, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	next, err := s.ChangeAmountFrom(ctx, h, old.ID, "2026-04-01", 6000)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if next.ExternalID != "" {
		t.Errorf("the successor took the external id %q with it", next.ExternalID)
	}
	if kept, _ := s.GetBookingByExternalID(ctx, h, "strom"); kept.ID != old.ID {
		t.Error("the external id no longer points at the booking it was given to")
	}
}

func TestChangeAmountRefusesWhatMakesNoSense(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h, _ := s.ActiveHouseholdID(ctx)
	cats, _ := s.ListCategories(ctx, h)

	base := Booking{
		HouseholdID: h, CategoryID: cats[0].ID, Direction: DirExpense,
		Name: "Strom", AmountCents: 5000, Frequency: FreqMonthly, Interval: 1,
		DuePoint: DueStart, StartsOn: "2026-03-01", EndsOn: "2026-09-30",
		CostNature: CostFix, BudgetClass: ClassNeed, SplitMode: SplitEqual,
	}
	recurring, _ := s.CreateBooking(ctx, base, nil, nil)

	once := base
	once.Frequency = FreqOnce
	oneOff, _ := s.CreateBooking(ctx, once, nil, nil)

	cases := map[string]struct {
		id   int64
		from string
	}{
		"before the booking starts": {recurring.ID, "2026-02-01"},
		"on the day it starts":      {recurring.ID, "2026-03-01"},
		"after it ends":             {recurring.ID, "2026-11-01"},
		"not a date":                {recurring.ID, "April"},
		"a one-off has no period":   {oneOff.ID, "2026-05-01"},
	}
	for name, c := range cases {
		if _, err := s.ChangeAmountFrom(ctx, h, c.id, c.from, 6000); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	// Nothing of the above may have taken effect.
	after, _ := s.GetBooking(ctx, h, recurring.ID)
	if after.EndsOn != "2026-09-30" || after.AmountCents != 5000 {
		t.Errorf("a rejected change still altered the booking: %+v", after)
	}
	bookings, _ := s.ListBookings(ctx, h)
	if len(bookings) != 2 {
		t.Errorf("bookings = %d, want the two that were created", len(bookings))
	}
}
