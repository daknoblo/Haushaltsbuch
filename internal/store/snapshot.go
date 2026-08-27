package store

import (
	"context"
	"fmt"
	"regexp"
)

// SnapshotVersion is the layout of a backup file. It is written into every
// snapshot so a future change can tell an old file apart instead of importing
// it into the wrong shape.
const SnapshotVersion = 1

// Snapshot is a household book in full: everything the application stores, in
// one structure that survives a round trip through a file.
type Snapshot struct {
	Version    int                 `json:"version"`
	CreatedAt  string              `json:"created_at"`
	ActiveID   int64               `json:"active_household_id"`
	Households []HouseholdSnapshot `json:"households"`
}

// HouseholdSnapshot is one household with everything that belongs to it.
type HouseholdSnapshot struct {
	Household  Household         `json:"household"`
	Members    []Member          `json:"members"`
	Categories []Category        `json:"categories"`
	Tags       []Tag             `json:"tags"`
	Bookings   []BookingSnapshot `json:"bookings"`
}

// BookingSnapshot is a booking with the rows that hang off it.
type BookingSnapshot struct {
	Booking   Booking           `json:"booking"`
	Splits    []BookingSplit    `json:"splits"`
	TagIDs    []int64           `json:"tag_ids"`
	Overrides []BookingOverride `json:"overrides"`
}

// Export reads the whole database into a snapshot.
func (s *Store) Export(ctx context.Context) (Snapshot, error) {
	snap := Snapshot{Version: SnapshotVersion, CreatedAt: now()}

	active, err := s.ActiveHouseholdID(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	snap.ActiveID = active

	households, err := s.ListHouseholds(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	for _, h := range households {
		hs := HouseholdSnapshot{Household: h}
		if hs.Members, err = s.ListMembers(ctx, h.ID); err != nil {
			return Snapshot{}, err
		}
		if hs.Categories, err = s.ListCategories(ctx, h.ID); err != nil {
			return Snapshot{}, err
		}
		if hs.Tags, err = s.ListTags(ctx, h.ID); err != nil {
			return Snapshot{}, err
		}

		bookings, err := s.ListBookings(ctx, h.ID)
		if err != nil {
			return Snapshot{}, err
		}
		splits, err := s.ListSplitsForHousehold(ctx, h.ID)
		if err != nil {
			return Snapshot{}, err
		}
		tags, err := s.ListBookingTags(ctx, h.ID)
		if err != nil {
			return Snapshot{}, err
		}
		overrides, err := s.ListOverridesForHousehold(ctx, h.ID)
		if err != nil {
			return Snapshot{}, err
		}
		for _, b := range bookings {
			hs.Bookings = append(hs.Bookings, BookingSnapshot{
				Booking:   b,
				Splits:    splits[b.ID],
				TagIDs:    tags[b.ID],
				Overrides: overrides[b.ID],
			})
		}
		snap.Households = append(snap.Households, hs)
	}
	return snap, nil
}

// Import replaces the entire database with a snapshot. Rows keep the ids they
// were exported with, which is what lets every reference between them stay
// intact without a remapping pass.
func (s *Store) Import(ctx context.Context, snap Snapshot) error {
	if snap.Version != SnapshotVersion {
		return fmt.Errorf("%w: version %d", ErrBadSnapshot, snap.Version)
	}
	if len(snap.Households) == 0 {
		return fmt.Errorf("%w: no household in it", ErrBadSnapshot)
	}

	return s.withTx(ctx, func(tx *Store) error {
		if err := tx.wipe(ctx); err != nil {
			return err
		}
		for _, hs := range snap.Households {
			if err := tx.importHousehold(ctx, hs); err != nil {
				return err
			}
		}
		active := snap.ActiveID
		if !hasHousehold(snap, active) {
			active = snap.Households[0].Household.ID
		}
		return tx.SetActiveHousehold(ctx, active)
	})
}

func hasHousehold(snap Snapshot, id int64) bool {
	for _, hs := range snap.Households {
		if hs.Household.ID == id {
			return true
		}
	}
	return false
}

func (s *Store) importHousehold(ctx context.Context, hs HouseholdSnapshot) error {
	h := hs.Household
	if _, err := s.q.ExecContext(ctx,
		`INSERT INTO households (id, name, sort_order, created_at) VALUES (?, ?, ?, ?)`,
		h.ID, cleanText(h.Name, "Haushalt"), h.SortOrder, orNow(h.CreatedAt)); err != nil {
		return err
	}

	for _, m := range hs.Members {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO members (id, household_id, name, color, sort_order) VALUES (?, ?, ?, ?, ?)`,
			m.ID, h.ID, cleanText(m.Name, "Person"), cleanColor(m.Color), m.SortOrder); err != nil {
			return err
		}
	}
	for _, c := range hs.Categories {
		class := c.Classification
		if !class.Valid() {
			class = DirExpense
		}
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO categories (id, household_id, name, classification, color, icon, sort_order)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			c.ID, h.ID, cleanText(c.Name, "Kategorie"), string(class),
			cleanColor(c.Color), c.Icon, c.SortOrder); err != nil {
			return err
		}
	}
	for _, t := range hs.Tags {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO tags (id, household_id, name, color) VALUES (?, ?, ?, ?)`,
			t.ID, h.ID, cleanText(t.Name, "Tag"), cleanColor(t.Color)); err != nil {
			return err
		}
	}
	for _, bs := range hs.Bookings {
		if err := s.importBooking(ctx, h.ID, bs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) importBooking(ctx context.Context, householdID int64, bs BookingSnapshot) error {
	b := sanitizeBooking(bs.Booking)
	if _, err := s.q.ExecContext(ctx,
		`INSERT INTO bookings
			(id, household_id, category_id, payer_member_id, direction, name, note,
			 amount_cents, frequency, interval_n, due_point, starts_on, ends_on,
			 cost_nature, budget_class, split_mode, settle, created_at, updated_at)
		 VALUES (?, ?, `+categoryRef+`, `+memberRef+`, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, householdID,
		b.CategoryID, householdID,
		nullInt(b.PayerMemberID), householdID,
		string(b.Direction), b.Name, b.Note, b.AmountCents,
		string(b.Frequency), b.Interval, string(b.DuePoint), b.StartsOn, b.EndsOn,
		string(b.CostNature), string(b.BudgetClass), string(b.SplitMode), b.Settle,
		orNow(b.CreatedAt), orNow(b.UpdatedAt),
	); err != nil {
		return err
	}

	for _, sp := range bs.Splits {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO booking_splits (booking_id, member_id, value)
			 SELECT ?, id, ? FROM members WHERE id = ? AND household_id = ?`,
			b.ID, sp.Value, sp.MemberID, householdID); err != nil {
			return err
		}
	}
	for _, id := range bs.TagIDs {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO booking_tags (booking_id, tag_id)
			 SELECT ?, id FROM tags WHERE id = ? AND household_id = ?`,
			b.ID, id, householdID); err != nil {
			return err
		}
	}
	for _, o := range bs.Overrides {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO booking_overrides (id, booking_id, starts_on, ends_on, amount_cents, note)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			o.ID, b.ID, o.StartsOn, o.EndsOn, o.AmountCents, o.Note); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeBooking replaces anything a hand-edited file could have put in an
// enum, because an unknown frequency would silently change what a figure means.
func sanitizeBooking(b Booking) Booking {
	if !b.Direction.Valid() {
		b.Direction = DirExpense
	}
	if !b.Frequency.Valid() {
		b.Frequency = FreqMonthly
	}
	if !b.DuePoint.Valid() {
		b.DuePoint = DueStart
	}
	if !b.CostNature.Valid() {
		b.CostNature = CostFix
	}
	if !b.BudgetClass.Valid() {
		b.BudgetClass = ClassNeed
	}
	if !b.SplitMode.Valid() {
		b.SplitMode = SplitEqual
	}
	if b.Interval < 1 {
		b.Interval = 1
	}
	return b
}

// Reset empties the database and seeds it the way a fresh install starts.
func (s *Store) Reset(ctx context.Context) error {
	return s.withTx(ctx, func(tx *Store) error {
		if err := tx.wipe(ctx); err != nil {
			return err
		}
		return tx.EnsureSeed(ctx)
	})
}

// ResetBookings drops every booking of one household, keeping the people,
// categories and tags it was set up with.
func (s *Store) ResetBookings(ctx context.Context, householdID int64) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM bookings WHERE household_id = ?`, householdID)
	return err
}

// wipe empties every table. Deleting the households cascades into everything
// that hangs off them; app_state has no owner and goes separately.
func (s *Store) wipe(ctx context.Context) error {
	for _, stmt := range []string{
		`DELETE FROM households`,
		`DELETE FROM app_state`,
	} {
		if _, err := s.q.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// cleanText keeps a name from arriving empty out of a hand-edited file.
func cleanText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// cleanColor drops anything that is not a plain "#rrggbb", so a hand-edited
// file cannot smuggle a value into a style attribute.
func cleanColor(c string) string {
	if hexColor.MatchString(c) {
		return c
	}
	return MemberColor(0)
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func orNow(ts string) string {
	if ts == "" {
		return now()
	}
	return ts
}
