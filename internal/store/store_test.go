package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// The 0004 rebuild drops and recreates the bookings table. With foreign key
// enforcement on, that DROP would cascade every split and tag away, so this
// walks the real upgrade path instead of starting from the final schema.
func TestMigrationKeepsSplitsAndTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	stmts := make([]string, 0, 16)
	stmts = append(stmts,
		`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`,
	)
	for _, name := range []string{"0001_init.sql", "0002_income_month_index.sql", "0003_bookings.sql"} {
		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		stmts = append(stmts, string(content),
			`INSERT INTO schema_migrations (version, applied_at) VALUES ('`+name+`', '')`)
	}
	stmts = append(stmts,
		`INSERT INTO households (id, name, sort_order, created_at) VALUES (1, 'H', 0, '')`,
		`INSERT INTO members (id, household_id, name, color, sort_order) VALUES (1, 1, 'Anna', '', 0)`,
		`INSERT INTO members (id, household_id, name, color, sort_order) VALUES (2, 1, 'Ben', '', 1)`,
		`INSERT INTO sections (id, household_id, name, sort_order) VALUES (1, 1, 'Wohnen', 0)`,
		`INSERT INTO categories (id, household_id, name, classification, color, sort_order)
		 VALUES (1, 1, 'Miete', 'expense', '#111111', 0)`,
		`INSERT INTO tags (id, household_id, name, color) VALUES (1, 1, 'fix', '#222222')`,
		`INSERT INTO bookings (id, household_id, category_id, section_id, direction, name,
			amount_cents, frequency, interval_n, starts_on, ends_on, cost_nature,
			budget_class, split_mode, sort_order, created_at, updated_at)
		 VALUES (1, 1, 1, 1, 'expense', 'Miete', 120000, 'monthly', 1, '2026-01-01', '',
			'fix', 'need', 'percent', 0, '', '')`,
		`INSERT INTO booking_splits (booking_id, member_id, value) VALUES (1, 1, 60), (1, 2, 40)`,
		`INSERT INTO booking_tags (booking_id, tag_id) VALUES (1, 1)`,
	)
	for _, s := range stmts {
		if _, err := legacy.Exec(s); err != nil {
			t.Fatalf("seed legacy db: %v", err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := t.Context()
	splits, err := st.ListSplitsForHousehold(ctx, 1)
	if err != nil {
		t.Fatalf("splits: %v", err)
	}
	if len(splits[1]) != 2 {
		t.Errorf("the rebuild lost splits: %+v", splits[1])
	}
	tags, err := st.ListBookingTags(ctx, 1)
	if err != nil {
		t.Fatalf("tags: %v", err)
	}
	if len(tags[1]) != 1 {
		t.Errorf("the rebuild lost tags: %+v", tags[1])
	}

	b, err := st.GetBooking(ctx, 1, 1)
	if err != nil {
		t.Fatalf("booking: %v", err)
	}
	if b.AmountCents != 120000 || b.Name != "Miete" {
		t.Errorf("booking = %+v", b)
	}
	// A payer is a new fact, so the first member of the household stands in.
	if b.PayerMemberID == nil || *b.PayerMemberID != 1 {
		t.Errorf("payer = %v, want member 1", b.PayerMemberID)
	}
	if b.DuePoint != DueStart {
		t.Errorf("due point = %q, want start", b.DuePoint)
	}
}

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

func TestReorderMembers(t *testing.T) {
	st, ctx, hh := seededStore(t)
	for _, name := range []string{"Zweite", "Dritte"} {
		if _, err := st.CreateMember(ctx, hh.ID, name, "#000000"); err != nil {
			t.Fatalf("create member: %v", err)
		}
	}
	before, _ := st.ListMembers(ctx, hh.ID)
	if len(before) < 3 {
		t.Fatalf("need at least 3 members, got %d", len(before))
	}

	if err := st.MoveMember(ctx, hh.ID, before[2].ID, -1); err != nil {
		t.Fatalf("move up: %v", err)
	}
	after, _ := st.ListMembers(ctx, hh.ID)
	if after[1].ID != before[2].ID || after[2].ID != before[1].ID {
		t.Fatalf("order after move up = %v", memberIDs(after))
	}

	// Moving the first entry further up is a no-op rather than an error.
	if err := st.MoveMember(ctx, hh.ID, after[0].ID, -1); err != nil {
		t.Fatalf("move first up: %v", err)
	}
	again, _ := st.ListMembers(ctx, hh.ID)
	if again[0].ID != after[0].ID {
		t.Fatalf("first entry moved unexpectedly: %v", memberIDs(again))
	}
}

func memberIDs(ms []Member) []int64 {
	out := make([]int64, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestUpdatesReportMissingRows(t *testing.T) {
	st, ctx, hh := seededStore(t)
	if err := st.UpdateMember(ctx, hh.ID, 999999, "X", "#000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing member = %v, want ErrNotFound", err)
	}
	if err := st.DeleteCategory(ctx, hh.ID, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing category = %v, want ErrNotFound", err)
	}
	if err := st.DeleteOverride(ctx, hh.ID, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing override = %v, want ErrNotFound", err)
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
		DuePoint:    DueStart,
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
	otherMembers, err := st.ListMembers(ctx, other.ID)
	if err != nil {
		t.Fatalf("other members: %v", err)
	}
	otherTag, err := st.CreateTag(ctx, other.ID, "Fremd", "#000000")
	if err != nil {
		t.Fatalf("other tag: %v", err)
	}

	b := newBooking(hh, cat, 1000)
	b.PayerMemberID = &otherMembers[0].ID

	created, err := st.CreateBooking(ctx, b,
		[]SplitInput{{MemberID: otherMembers[0].ID, Value: 100}}, []int64{otherTag.ID})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if created.PayerMemberID != nil {
		t.Errorf("payer of a foreign household leaked in: %v", *created.PayerMemberID)
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

func TestOverridesRoundTrip(t *testing.T) {
	st, ctx, hh := seededStore(t)
	cat := firstExpenseCategory(ctx, t, st, hh)
	b, err := st.CreateBooking(ctx, newBooking(hh, cat, 4999), nil, nil)
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}

	o, err := st.CreateOverride(ctx, hh.ID, BookingOverride{
		BookingID: b.ID, StartsOn: "2026-01-01", EndsOn: "2026-06-30", AmountCents: 1000,
	})
	if err != nil {
		t.Fatalf("create override: %v", err)
	}
	if o.AmountCents != 1000 {
		t.Errorf("override = %+v", o)
	}

	o.AmountCents = 1500
	if err := st.UpdateOverride(ctx, hh.ID, o); err != nil {
		t.Fatalf("update override: %v", err)
	}
	list, err := st.ListOverrides(ctx, hh.ID, b.ID)
	if err != nil {
		t.Fatalf("list overrides: %v", err)
	}
	if len(list) != 1 || list[0].AmountCents != 1500 {
		t.Fatalf("overrides = %+v", list)
	}

	// A foreign household must not reach the override.
	other, _ := st.CreateHouseholdSeeded(ctx, "Fremd")
	if err := st.DeleteOverride(ctx, other.ID, o.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-household delete = %v, want ErrNotFound", err)
	}
	if err := st.DeleteOverride(ctx, hh.ID, o.ID); err != nil {
		t.Fatalf("delete override: %v", err)
	}

	// Deleting the booking has to take its overrides with it.
	o2, _ := st.CreateOverride(ctx, hh.ID, BookingOverride{BookingID: b.ID, AmountCents: 1})
	if err := st.DeleteBooking(ctx, hh.ID, b.ID); err != nil {
		t.Fatalf("delete booking: %v", err)
	}
	if err := st.DeleteOverride(ctx, hh.ID, o2.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("override outlived its booking: %v", err)
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

	unused, err := st.CreateCategory(ctx, hh.ID, Category{Name: "Frei", Classification: DirExpense, Color: "#123456"})
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
