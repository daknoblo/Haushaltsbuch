package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seededStore(t *testing.T) (*Store, context.Context, Household) {
	t.Helper()
	ctx := t.Context()
	st := newTestStore(t)
	if err := st.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hs, err := st.ListHouseholds(ctx)
	if err != nil {
		t.Fatalf("list households: %v", err)
	}
	return st, ctx, hs[0]
}

func TestEnsureSeed(t *testing.T) {
	ctx := t.Context()
	st := newTestStore(t)
	if err := st.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Second call must be idempotent.
	if err := st.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed again: %v", err)
	}

	hs, err := st.ListHouseholds(ctx)
	if err != nil {
		t.Fatalf("list households: %v", err)
	}
	if len(hs) != 1 {
		t.Fatalf("want 1 household, got %d", len(hs))
	}

	active, err := st.ActiveHouseholdID(ctx)
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active != hs[0].ID {
		t.Fatalf("active household = %d, want %d", active, hs[0].ID)
	}

	members, err := st.ListMembers(ctx, hs[0].ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("want 2 members, got %d", len(members))
	}
}

func TestReorderSections(t *testing.T) {
	st, ctx, hh := seededStore(t)
	before, _ := st.ListSections(ctx, hh.ID)
	if len(before) < 3 {
		t.Fatalf("need at least 3 sections, got %d", len(before))
	}

	if err := st.MoveSection(ctx, hh.ID, before[2].ID, -1); err != nil {
		t.Fatalf("move up: %v", err)
	}
	after, _ := st.ListSections(ctx, hh.ID)
	if after[1].ID != before[2].ID || after[2].ID != before[1].ID {
		t.Fatalf("order after move up = %v", ids(after))
	}

	// Moving the first entry further up is a no-op rather than an error.
	if err := st.MoveSection(ctx, hh.ID, after[0].ID, -1); err != nil {
		t.Fatalf("move first up: %v", err)
	}
	again, _ := st.ListSections(ctx, hh.ID)
	if again[0].ID != after[0].ID {
		t.Fatalf("first entry moved unexpectedly: %v", ids(again))
	}
}

func ids(secs []Section) []int64 {
	out := make([]int64, len(secs))
	for i, s := range secs {
		out[i] = s.ID
	}
	return out
}

func TestUpdatesReportMissingRows(t *testing.T) {
	st, ctx, hh := seededStore(t)
	if err := st.RenameSection(ctx, hh.ID, 999999, "X"); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename missing section = %v, want ErrNotFound", err)
	}
	if err := st.UpdateMember(ctx, hh.ID, 999999, "X", "#000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing member = %v, want ErrNotFound", err)
	}
	if err := st.DeleteCategory(ctx, hh.ID, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing category = %v, want ErrNotFound", err)
	}
}

func TestFrequencyMonthlyFactor(t *testing.T) {
	cases := map[Frequency]float64{
		FreqOnce:      1.0,
		FreqMonthly:   1.0,
		FreqWeekly:    52.0 / 12.0,
		FreqQuarterly: 1.0 / 3.0,
		FreqYearly:    1.0 / 12.0,
	}
	for f, want := range cases {
		if got := f.MonthlyFactor(); got != want {
			t.Errorf("%s factor = %v, want %v", f, got, want)
		}
	}
	if FreqOnce.Recurring() {
		t.Error("a one-off booking must not count as recurring")
	}
	if !FreqYearly.Recurring() {
		t.Error("a yearly booking must count as recurring")
	}
}

// firstExpenseCategory returns a seeded category bookings can be filed under.
func firstExpenseCategory(ctx context.Context, t *testing.T, st *Store, hh Household) Category {
	t.Helper()
	cats, err := st.ListCategories(ctx, hh.ID)
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	for _, c := range cats {
		if c.Classification == DirExpense {
			return c
		}
	}
	t.Fatal("no expense category seeded")
	return Category{}
}

// newBooking builds a booking whose direction follows its category.
func newBooking(hh Household, cat Category, cents int64) Booking {
	return Booking{
		HouseholdID: hh.ID,
		CategoryID:  cat.ID,
		Direction:   cat.Classification,
		Name:        "Test",
		AmountCents: cents,
		Frequency:   FreqMonthly,
		Interval:    1,
		StartsOn:    "2026-01-01",
		CostNature:  CostFix,
		BudgetClass: ClassNeed,
		SplitMode:   SplitPercent,
	}
}

func TestBookingWithSplitsAndTags(t *testing.T) {
	st, ctx, hh := seededStore(t)
	cat := firstExpenseCategory(ctx, t, st, hh)
	members, err := st.ListMembers(ctx, hh.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	tag, err := st.CreateTag(ctx, hh.ID, "Urlaub", "#14b8a6")
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}

	b, err := st.CreateBooking(ctx, newBooking(hh, cat, 120000),
		[]SplitInput{{MemberID: members[0].ID, Value: 60}, {MemberID: members[1].ID, Value: 40}},
		[]int64{tag.ID})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if b.CategoryID != cat.ID || b.Interval != 1 {
		t.Fatalf("stored booking = %+v", b)
	}

	splits, err := st.ListSplitsForHousehold(ctx, hh.ID)
	if err != nil {
		t.Fatalf("splits: %v", err)
	}
	if len(splits[b.ID]) != 2 {
		t.Fatalf("want 2 splits, got %d", len(splits[b.ID]))
	}
	tags, err := st.ListBookingTags(ctx, hh.ID)
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	if len(tags[b.ID]) != 1 || tags[b.ID][0] != tag.ID {
		t.Fatalf("tags = %v, want [%d]", tags[b.ID], tag.ID)
	}

	// Saving with one member and no tag has to drop the others.
	b.AmountCents = 99000
	if err := st.SaveBooking(ctx, b, []SplitInput{{MemberID: members[0].ID, Value: 100}}, nil); err != nil {
		t.Fatalf("save booking: %v", err)
	}
	splits, _ = st.ListSplitsForHousehold(ctx, hh.ID)
	if len(splits[b.ID]) != 1 {
		t.Fatalf("want 1 split after save, got %d", len(splits[b.ID]))
	}
	tags, _ = st.ListBookingTags(ctx, hh.ID)
	if len(tags[b.ID]) != 0 {
		t.Fatalf("want no tags after save, got %v", tags[b.ID])
	}
}

func TestBookingRejectsForeignReferences(t *testing.T) {
	st, ctx, hh := seededStore(t)
	cat := firstExpenseCategory(ctx, t, st, hh)

	other, err := st.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}
	otherSections, err := st.ListSections(ctx, other.ID)
	if err != nil {
		t.Fatalf("other sections: %v", err)
	}
	otherMembers, err := st.ListMembers(ctx, other.ID)
	if err != nil {
		t.Fatalf("other members: %v", err)
	}
	otherTag, err := st.CreateTag(ctx, other.ID, "Fremd", "#000000")
	if err != nil {
		t.Fatalf("other tag: %v", err)
	}

	b := newBooking(hh, cat, 1000)
	b.SectionID = &otherSections[0].ID

	created, err := st.CreateBooking(ctx, b,
		[]SplitInput{{MemberID: otherMembers[0].ID, Value: 100}}, []int64{otherTag.ID})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if created.SectionID != nil {
		t.Errorf("section of a foreign household leaked in: %v", *created.SectionID)
	}
	splits, _ := st.ListSplitsForHousehold(ctx, hh.ID)
	if len(splits[created.ID]) != 0 {
		t.Errorf("member of a foreign household leaked in: %+v", splits[created.ID])
	}
	tags, _ := st.ListBookingTags(ctx, hh.ID)
	if len(tags[created.ID]) != 0 {
		t.Errorf("tag of a foreign household leaked in: %v", tags[created.ID])
	}
}

func TestBookingMutationsAreHouseholdScoped(t *testing.T) {
	st, ctx, hh := seededStore(t)
	cat := firstExpenseCategory(ctx, t, st, hh)
	other, err := st.CreateHouseholdSeeded(ctx, "Fremd")
	if err != nil {
		t.Fatalf("create other household: %v", err)
	}

	b, err := st.CreateBooking(ctx, newBooking(hh, cat, 1000), nil, nil)
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if _, err := st.GetBooking(ctx, other.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("read across households = %v, want ErrNotFound", err)
	}
	if err := st.DeleteBooking(ctx, other.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete across households = %v, want ErrNotFound", err)
	}
	if _, err := st.GetBooking(ctx, hh.ID, b.ID); err != nil {
		t.Errorf("booking was deleted anyway: %v", err)
	}
}

func TestCategoryInUseCannotBeDeleted(t *testing.T) {
	st, ctx, hh := seededStore(t)
	cat := firstExpenseCategory(ctx, t, st, hh)

	if _, err := st.CreateBooking(ctx, newBooking(hh, cat, 1000), nil, nil); err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if err := st.DeleteCategory(ctx, hh.ID, cat.ID); !errors.Is(err, ErrCategoryInUse) {
		t.Fatalf("delete used category = %v, want ErrCategoryInUse", err)
	}

	usage, err := st.CountCategoryUsage(ctx, hh.ID)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if usage[cat.ID] != 1 {
		t.Errorf("usage = %d, want 1", usage[cat.ID])
	}

	unused, err := st.CreateCategory(ctx, hh.ID, "Frei", DirExpense, "#123456")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if err := st.DeleteCategory(ctx, hh.ID, unused.ID); err != nil {
		t.Errorf("delete unused category: %v", err)
	}
}

func TestSeedProvidesBothCategoryKinds(t *testing.T) {
	st, ctx, hh := seededStore(t)
	cats, err := st.ListCategories(ctx, hh.ID)
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	var income, expense int
	for _, c := range cats {
		switch c.Classification {
		case DirIncome:
			income++
		case DirExpense:
			expense++
		}
		if c.Color == "" {
			t.Errorf("category %q has no color", c.Name)
		}
	}
	if income == 0 || expense == 0 {
		t.Fatalf("seed needs both kinds, got %d income / %d expense", income, expense)
	}
}
