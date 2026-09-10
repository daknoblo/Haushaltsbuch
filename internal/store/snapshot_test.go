package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// A backup is only worth having if what comes back is what went in.
func TestSnapshotRoundTripKeepsEverything(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	h, err := s.ActiveHouseholdID(ctx)
	if err != nil {
		t.Fatalf("active household: %v", err)
	}
	members, _ := s.ListMembers(ctx, h)
	cats, _ := s.ListCategories(ctx, h)
	tag, err := s.CreateTag(ctx, h, "Urlaub", "#2563eb")
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}
	booking, err := s.CreateBooking(ctx, Booking{
		HouseholdID: h, CategoryID: cats[0].ID, PayerMemberID: &members[0].ID,
		Direction: DirExpense, Name: "Miete", AmountCents: 98100,
		Frequency: FreqMonthly, Interval: 1, DuePoint: DueStart,
		StartsOn: "2026-01-01", CostNature: CostFix, BudgetClass: ClassNeed,
		SplitMode: SplitPercent, Settle: true,
	}, []SplitInput{{MemberID: members[0].ID, Value: 60}, {MemberID: members[1].ID, Value: 40}},
		[]int64{tag.ID})
	if err != nil {
		t.Fatalf("create booking: %v", err)
	}
	if _, err := s.CreateOverride(ctx, h, BookingOverride{
		BookingID: booking.ID, StartsOn: "2026-03-01", EndsOn: "2026-08-31", AmountCents: 60000,
	}); err != nil {
		t.Fatalf("create override: %v", err)
	}

	snap, err := s.Export(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := s.Import(ctx, snap); err != nil {
		t.Fatalf("import: %v", err)
	}

	back, err := s.Export(ctx)
	if err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if len(back.Households) != len(snap.Households) {
		t.Fatalf("households = %d, want %d", len(back.Households), len(snap.Households))
	}
	if back.ActiveID != snap.ActiveID {
		t.Errorf("active household = %d, want %d", back.ActiveID, snap.ActiveID)
	}

	got := back.Households[0]
	if len(got.Bookings) != 1 {
		t.Fatalf("bookings = %d, want 1", len(got.Bookings))
	}
	bs := got.Bookings[0]
	if bs.Booking.Name != "Miete" || bs.Booking.AmountCents != 98100 {
		t.Errorf("booking came back as %+v", bs.Booking)
	}
	if bs.Booking.PayerMemberID == nil || *bs.Booking.PayerMemberID != members[0].ID {
		t.Error("the payer did not survive the round trip")
	}
	if len(bs.Splits) != 2 || bs.Splits[0].Value != 60 {
		t.Errorf("splits came back as %+v", bs.Splits)
	}
	if len(bs.TagIDs) != 1 || bs.TagIDs[0] != tag.ID {
		t.Errorf("tags came back as %v", bs.TagIDs)
	}
	if len(bs.Overrides) != 1 || bs.Overrides[0].AmountCents != 60000 {
		t.Errorf("overrides came back as %+v", bs.Overrides)
	}
}

// Importing replaces rather than merges, or a restore would pile a second copy
// on top of what is already there.
func TestImportReplacesWhatWasThere(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snap, err := s.Export(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := s.CreateHouseholdSeeded(ctx, "Dazwischen"); err != nil {
		t.Fatalf("create household: %v", err)
	}
	if err := s.Import(ctx, snap); err != nil {
		t.Fatalf("import: %v", err)
	}

	households, err := s.ListHouseholds(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(households) != len(snap.Households) {
		t.Errorf("households = %d, want %d", len(households), len(snap.Households))
	}
	for _, h := range households {
		if h.Name == "Dazwischen" {
			t.Error("a household created after the backup survived the restore")
		}
	}
}

func TestImportRejectsAnUnknownVersion(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snap, _ := s.Export(ctx)
	snap.Version = SnapshotVersion + 1
	if err := s.Import(ctx, snap); err == nil {
		t.Error("a snapshot from another version was imported")
	}
}

// A file that fails halfway must leave the book as it was, not half wiped.
func TestFailedImportLeavesTheBookAlone(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	before, _ := s.Export(ctx)
	broken, _ := s.Export(ctx)
	// A booking pointing at a category that the file never defines.
	broken.Households[0].Bookings = append(broken.Households[0].Bookings, BookingSnapshot{
		Booking: Booking{ID: 9999, CategoryID: 4242, Direction: DirExpense, Name: "kaputt"},
	})
	if err := s.Import(ctx, broken); err == nil {
		t.Fatal("a snapshot with a dangling category was imported")
	}

	after, err := s.Export(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(after.Households) != len(before.Households) {
		t.Errorf("households = %d, want the original %d", len(after.Households), len(before.Households))
	}
}

func TestResetStartsOver(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	h, _ := s.ActiveHouseholdID(ctx)
	if _, err := s.CreateHouseholdSeeded(ctx, "Zweiter"); err != nil {
		t.Fatalf("create household: %v", err)
	}
	if _, err := s.CreateTag(ctx, h, "Weg", "#2563eb"); err != nil {
		t.Fatalf("create tag: %v", err)
	}

	if err := s.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	households, _ := s.ListHouseholds(ctx)
	if len(households) != 1 {
		t.Fatalf("households = %d, want the single seeded one", len(households))
	}
	active, err := s.ActiveHouseholdID(ctx)
	if err != nil || active == 0 {
		t.Errorf("no household is active after a reset: %d, %v", active, err)
	}
	members, _ := s.ListMembers(ctx, active)
	if len(members) == 0 {
		t.Error("a reset household has nobody in it")
	}
	cats, _ := s.ListCategories(ctx, active)
	if len(cats) == 0 {
		t.Error("a reset household has no categories")
	}
	tags, _ := s.ListTags(ctx, active)
	if len(tags) != 0 {
		t.Errorf("tags = %d, want none after a reset", len(tags))
	}
}

// Clearing the bookings keeps what the book was set up with, so the next year
// starts from the same people and categories.
func TestResetBookingsKeepsTheSetup(t *testing.T) {
	ctx := t.Context()
	s := newTestStore(t)
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatalf("seed: %v", err)
	}

	h, _ := s.ActiveHouseholdID(ctx)
	cats, _ := s.ListCategories(ctx, h)
	members, _ := s.ListMembers(ctx, h)
	if _, err := s.CreateBooking(ctx, Booking{
		HouseholdID: h, CategoryID: cats[0].ID, PayerMemberID: &members[0].ID,
		Direction: DirExpense, Name: "Miete", AmountCents: 1000,
		Frequency: FreqMonthly, Interval: 1, DuePoint: DueStart,
		CostNature: CostFix, BudgetClass: ClassNeed, SplitMode: SplitEqual,
	}, nil, nil); err != nil {
		t.Fatalf("create booking: %v", err)
	}

	if err := s.ResetBookings(ctx, h); err != nil {
		t.Fatalf("reset bookings: %v", err)
	}

	bookings, _ := s.ListBookings(ctx, h)
	if len(bookings) != 0 {
		t.Errorf("bookings = %d, want none", len(bookings))
	}
	if left, _ := s.ListMembers(ctx, h); len(left) != len(members) {
		t.Errorf("members = %d, want the original %d", len(left), len(members))
	}
	if left, _ := s.ListCategories(ctx, h); len(left) != len(cats) {
		t.Errorf("categories = %d, want the original %d", len(left), len(cats))
	}
}

func TestSnapshotRetiredMarkerAndLegacyRestore(t *testing.T) {
	s, ctx, h := seededStore(t)
	cat := firstExpenseCategory(ctx, t, s, h)
	b := newBooking(h, cat, 5000)
	b.StartsOn, b.EndsOn = "2025-01-01", "2026-12-31"
	b.Retired = true
	old, err := s.CreateBooking(ctx, b, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if old.Retired {
		t.Fatal("ordinary creation accepted a read-only retired marker")
	}
	next, err := s.ChangeAmountFrom(ctx, h.ID, old.ID, "2026-01-01", 6000)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := s.Import(ctx, decoded); err != nil {
		t.Fatal(err)
	}
	for id, retired := range map[int64]bool{old.ID: true, next.ID: false} {
		got, err := s.GetBooking(ctx, h.ID, id)
		if err != nil || got.Retired != retired {
			t.Fatalf("restored marker id=%d: %+v, %v", id, got, err)
		}
	}

	// Legacy backups have no marker. Even identical names and contiguous dates
	// do not provide enough information to reconstruct predecessor relationships.
	legacy := bytes.ReplaceAll(raw, []byte(`"Retired":true,`), nil)
	legacy = bytes.ReplaceAll(legacy, []byte(`"Retired":false,`), nil)
	if bytes.Contains(legacy, []byte(`"Retired"`)) {
		t.Fatal("legacy fixture still contains Retired")
	}
	var oldFormat Snapshot
	if err := json.Unmarshal(legacy, &oldFormat); err != nil {
		t.Fatal(err)
	}
	if err := s.Import(ctx, oldFormat); err != nil {
		t.Fatalf("legacy restore: %v", err)
	}
	for _, id := range []int64{old.ID, next.ID} {
		got, err := s.GetBooking(ctx, h.ID, id)
		if err != nil || got.Retired {
			t.Fatalf("legacy marker id=%d: %+v, %v", id, got, err)
		}
	}
}

func TestImportInvalidRangeRollsBack(t *testing.T) {
	s, ctx, h := seededStore(t)
	cat := firstExpenseCategory(ctx, t, s, h)
	b, err := s.CreateBooking(ctx, newBooking(h, cat, 5000), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, override := range []bool{false, true} {
		snap, err := s.Export(ctx)
		if err != nil {
			t.Fatal(err)
		}
		bs := &snap.Households[0].Bookings[0]
		if override {
			bs.Overrides = []BookingOverride{{
				ID: 1, BookingID: b.ID, StartsOn: "2026-12-01", EndsOn: "2026-01-31",
			}}
		} else {
			bs.Booking.StartsOn, bs.Booking.EndsOn = "2026-12-01", "2026-01-31"
		}
		if err := s.Import(ctx, snap); !errors.Is(err, ErrBadSnapshot) {
			t.Fatalf("override=%v: error=%v, want ErrBadSnapshot", override, err)
		}
		got, err := s.GetBooking(ctx, h.ID, b.ID)
		if err != nil || got.StartsOn != b.StartsOn || got.EndsOn != b.EndsOn {
			t.Fatalf("failed restore changed booking: %+v, %v", got, err)
		}
	}
}

func TestExportIsConsistentDuringConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := t.Context()
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatal(err)
	}
	households, err := s.ListHouseholds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h := households[0]
	cat := firstExpenseCategory(ctx, t, s, h)
	b := newBooking(h, cat, 5000)
	b.Name = h.Name
	b, err = s.CreateBooking(ctx, b, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	finished := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		for revision := 1; ; revision++ {
			select {
			case <-stop:
				finished <- nil
				return
			default:
			}
			name := fmt.Sprintf("revision %d", revision)
			err := writer.withTx(ctx, func(tx *Store) error {
				if err := tx.RenameHousehold(ctx, h.ID, name); err != nil {
					return err
				}
				b.Name = name
				return tx.SaveBooking(ctx, b, nil, nil)
			})
			if err != nil {
				finished <- err
				return
			}
		}
	}()
	defer func() {
		close(stop)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	for range 100 {
		snap, err := s.Export(ctx)
		if err != nil {
			t.Fatal(err)
		}
		hs := snap.Households[0]
		if len(hs.Bookings) != 1 || hs.Household.Name != hs.Bookings[0].Booking.Name {
			t.Fatalf("mixed revisions in snapshot: %+v", hs)
		}
	}
}
