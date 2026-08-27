package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type scanner interface {
	Scan(dest ...any) error
}

// SplitInput is a member's share of a booking as submitted by the UI.
type SplitInput struct {
	MemberID int64
	Value    float64
}

const bookingColumns = `id, household_id, category_id, payer_member_id, direction, name, note,
	amount_cents, frequency, interval_n, due_point, starts_on, ends_on, cost_nature,
	budget_class, split_mode, settle, external_id, created_at, updated_at`

func scanBooking(sc scanner) (Booking, error) {
	var (
		b     Booking
		payer sql.NullInt64
	)
	err := sc.Scan(
		&b.ID, &b.HouseholdID, &b.CategoryID, &payer, &b.Direction, &b.Name, &b.Note,
		&b.AmountCents, &b.Frequency, &b.Interval, &b.DuePoint, &b.StartsOn, &b.EndsOn,
		&b.CostNature, &b.BudgetClass, &b.SplitMode, &b.Settle, &b.ExternalID,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return Booking{}, err
	}
	if payer.Valid {
		v := payer.Int64
		b.PayerMemberID = &v
	}
	return b, nil
}

// ListBookings returns all bookings of a household, largest first. Bookings are
// grouped by category in the UI, which leaves no use for a manual order.
func (s *Store) ListBookings(ctx context.Context, householdID int64) ([]Booking, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+bookingColumns+` FROM bookings
		 WHERE household_id = ?
		 ORDER BY amount_cents DESC, name COLLATE NOCASE, id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBooking returns a single booking of a household.
func (s *Store) GetBooking(ctx context.Context, householdID, id int64) (Booking, error) {
	b, err := scanBooking(s.q.QueryRowContext(ctx,
		`SELECT `+bookingColumns+` FROM bookings WHERE id = ? AND household_id = ?`,
		id, householdID))
	if errors.Is(err, sql.ErrNoRows) {
		return Booking{}, ErrNotFound
	}
	return b, err
}

// GetBookingByExternalID finds the booking a caller filed under its own id.
// An empty id never matches, so a hand-entered booking cannot be claimed.
func (s *Store) GetBookingByExternalID(ctx context.Context, householdID int64, externalID string) (Booking, error) {
	if externalID == "" {
		return Booking{}, ErrNotFound
	}
	b, err := scanBooking(s.q.QueryRowContext(ctx,
		`SELECT `+bookingColumns+` FROM bookings WHERE household_id = ? AND external_id = ?`,
		householdID, externalID))
	if errors.Is(err, sql.ErrNoRows) {
		return Booking{}, ErrNotFound
	}
	return b, err
}

// These sub-selects resolve to NULL unless the referenced row belongs to the
// same household, so a forged id cannot create a cross-household reference.
// category_id is NOT NULL, so a forged category is rejected by the constraint.
const (
	categoryRef = `(SELECT id FROM categories WHERE id = ? AND household_id = ?)`
	memberRef   = `(SELECT id FROM members WHERE id = ? AND household_id = ?)`
)

// CreateBooking inserts a booking with its splits and tags and returns it.
func (s *Store) CreateBooking(ctx context.Context, b Booking, splits []SplitInput, tagIDs []int64) (Booking, error) {
	var created Booking
	err := s.withTx(ctx, func(tx *Store) error {
		ts := now()
		res, err := tx.q.ExecContext(ctx,
			`INSERT INTO bookings
				(household_id, category_id, payer_member_id, direction, name, note,
				 amount_cents, frequency, interval_n, due_point, starts_on, ends_on,
				 cost_nature, budget_class, split_mode, settle, external_id, created_at, updated_at)
			 VALUES (?, `+categoryRef+`, `+memberRef+`, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			b.HouseholdID,
			b.CategoryID, b.HouseholdID,
			nullInt(b.PayerMemberID), b.HouseholdID,
			string(b.Direction), b.Name, b.Note, b.AmountCents,
			string(b.Frequency), b.Interval, string(b.DuePoint), b.StartsOn, b.EndsOn,
			string(b.CostNature), string(b.BudgetClass), string(b.SplitMode), b.Settle,
			b.ExternalID, ts, ts,
		)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := tx.replaceSplits(ctx, id, b.HouseholdID, splits); err != nil {
			return err
		}
		if err := tx.replaceTags(ctx, id, b.HouseholdID, tagIDs); err != nil {
			return err
		}
		created, err = tx.GetBooking(ctx, b.HouseholdID, id)
		return err
	})
	if err != nil {
		return Booking{}, err
	}
	return created, nil
}

// SaveBooking persists all mutable fields of b together with its splits and
// tags in a single transaction, scoped to b.HouseholdID.
func (s *Store) SaveBooking(ctx context.Context, b Booking, splits []SplitInput, tagIDs []int64) error {
	return s.withTx(ctx, func(tx *Store) error {
		err := affected(tx.q.ExecContext(ctx,
			`UPDATE bookings SET
				category_id = `+categoryRef+`, payer_member_id = `+memberRef+`,
				direction = ?, name = ?, note = ?, amount_cents = ?, frequency = ?,
				interval_n = ?, due_point = ?, starts_on = ?, ends_on = ?,
				cost_nature = ?, budget_class = ?, split_mode = ?, settle = ?,
				external_id = ?, updated_at = ?
			 WHERE id = ? AND household_id = ?`,
			b.CategoryID, b.HouseholdID,
			nullInt(b.PayerMemberID), b.HouseholdID,
			string(b.Direction), b.Name, b.Note, b.AmountCents, string(b.Frequency),
			b.Interval, string(b.DuePoint), b.StartsOn, b.EndsOn, string(b.CostNature),
			string(b.BudgetClass), string(b.SplitMode), b.Settle, b.ExternalID,
			now(), b.ID, b.HouseholdID,
		))
		if err != nil {
			return err
		}
		if err := tx.replaceSplits(ctx, b.ID, b.HouseholdID, splits); err != nil {
			return err
		}
		return tx.replaceTags(ctx, b.ID, b.HouseholdID, tagIDs)
	})
}

// DeleteBooking removes a booking of a household with its splits and tags.
func (s *Store) DeleteBooking(ctx context.Context, householdID, id int64) error {
	return affected(s.q.ExecContext(ctx,
		`DELETE FROM bookings WHERE id = ? AND household_id = ?`, id, householdID))
}

// ExtendBookings moves the end of the given recurring bookings, which is how a
// book that runs to December is taken into the next year. It never shortens a
// period and never touches a one-off, so a stale checkbox can do no harm;
// anything that does not qualify is left alone rather than reported, because a
// review of a dozen bookings should not fail over one of them.
func (s *Store) ExtendBookings(ctx context.Context, householdID int64, ids []int64, until string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := time.Parse("2006-01-02", until); err != nil {
		return fmt.Errorf("%w: %q is not a date", ErrInvalid, until)
	}

	return s.withTx(ctx, func(tx *Store) error {
		for _, id := range ids {
			if _, err := tx.q.ExecContext(ctx,
				`UPDATE bookings SET ends_on = ?, updated_at = ?
				 WHERE id = ? AND household_id = ? AND frequency <> ? AND ends_on <> '' AND ends_on < ?`,
				until, now(), id, householdID, string(FreqOnce), until,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// ChangeAmountFrom records a lasting change of a recurring amount: the booking
// is closed off the day before, and a copy of it carries the new amount onward.
// Two bookings rather than one is what makes the change itself visible — when
// it happened and what it was before. Both halves are written together, because
// an ended booking without its successor is money silently gone from the plan.
func (s *Store) ChangeAmountFrom(ctx context.Context, householdID, id int64, from string, amountCents int64) (Booking, error) {
	var created Booking
	err := s.withTx(ctx, func(tx *Store) error {
		old, err := tx.GetBooking(ctx, householdID, id)
		if err != nil {
			return err
		}
		if !old.Frequency.Recurring() {
			return fmt.Errorf("%w: a one-off amount has nothing to change from", ErrInvalid)
		}

		day, err := time.Parse("2006-01-02", from)
		if err != nil {
			return fmt.Errorf("%w: %q is not a date", ErrInvalid, from)
		}
		if old.StartsOn != "" && from <= old.StartsOn {
			return fmt.Errorf("%w: the change starts before the booking does", ErrInvalid)
		}
		if old.EndsOn != "" && from > old.EndsOn {
			return fmt.Errorf("%w: the change starts after the booking ends", ErrInvalid)
		}

		splits, err := tx.ListSplits(ctx, householdID, id)
		if err != nil {
			return err
		}
		tagIDs, err := tx.ListTagIDs(ctx, householdID, id)
		if err != nil {
			return err
		}

		next := old
		next.ID = 0
		next.AmountCents = amountCents
		next.StartsOn = from
		// An external id is unique per household, so the successor cannot carry
		// the one its predecessor was filed under.
		next.ExternalID = ""

		old.EndsOn = day.AddDate(0, 0, -1).Format("2006-01-02")
		if err := tx.SaveBooking(ctx, old, splitInputs(splits), tagIDs); err != nil {
			return err
		}
		created, err = tx.CreateBooking(ctx, next, splitInputs(splits), tagIDs)
		return err
	})
	if err != nil {
		return Booking{}, err
	}
	return created, nil
}

// splitInputs turns stored splits back into what a write expects.
func splitInputs(splits []BookingSplit) []SplitInput {
	out := make([]SplitInput, 0, len(splits))
	for _, sp := range splits {
		out = append(out, SplitInput{MemberID: sp.MemberID, Value: sp.Value})
	}
	return out
}

func (s *Store) replaceSplits(ctx context.Context, bookingID, householdID int64, splits []SplitInput) error {
	args := make([]any, 0, len(splits)+1)
	args = append(args, bookingID)
	marks := make([]string, 0, len(splits))
	for _, sp := range splits {
		marks = append(marks, "?")
		args = append(args, sp.MemberID)
	}

	del := `DELETE FROM booking_splits WHERE booking_id = ?`
	if len(marks) > 0 {
		del += ` AND member_id NOT IN (` + strings.Join(marks, ", ") + `)`
	}
	if _, err := s.q.ExecContext(ctx, del, args...); err != nil {
		return err
	}

	for _, sp := range splits {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO booking_splits (booking_id, member_id, value)
			 SELECT ?, id, ? FROM members WHERE id = ? AND household_id = ?
			 ON CONFLICT(booking_id, member_id) DO UPDATE SET value = excluded.value`,
			bookingID, sp.Value, sp.MemberID, householdID,
		); err != nil {
			return err
		}
	}
	return nil
}

// ListSplits returns the splits of a single booking of a household.
func (s *Store) ListSplits(ctx context.Context, householdID, bookingID int64) ([]BookingSplit, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT sp.booking_id, sp.member_id, sp.value
		 FROM booking_splits sp
		 JOIN bookings b ON b.id = sp.booking_id
		 WHERE b.id = ? AND b.household_id = ?
		 ORDER BY sp.member_id`, bookingID, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BookingSplit
	for rows.Next() {
		var sp BookingSplit
		if err := rows.Scan(&sp.BookingID, &sp.MemberID, &sp.Value); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// ListSplitsForHousehold returns all booking splits of a household keyed by
// booking id.
func (s *Store) ListSplitsForHousehold(ctx context.Context, householdID int64) (map[int64][]BookingSplit, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT sp.booking_id, sp.member_id, sp.value
		 FROM booking_splits sp
		 JOIN bookings b ON b.id = sp.booking_id
		 WHERE b.household_id = ?
		 ORDER BY sp.booking_id, sp.member_id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64][]BookingSplit)
	for rows.Next() {
		var sp BookingSplit
		if err := rows.Scan(&sp.BookingID, &sp.MemberID, &sp.Value); err != nil {
			return nil, err
		}
		out[sp.BookingID] = append(out[sp.BookingID], sp)
	}
	return out, rows.Err()
}

// ---- amount overrides ----

const overrideColumns = `o.id, o.booking_id, o.starts_on, o.ends_on, o.amount_cents, o.note`

func scanOverride(sc scanner) (BookingOverride, error) {
	var o BookingOverride
	err := sc.Scan(&o.ID, &o.BookingID, &o.StartsOn, &o.EndsOn, &o.AmountCents, &o.Note)
	return o, err
}

// ListOverrides returns the amount overrides of a single booking, oldest first.
func (s *Store) ListOverrides(ctx context.Context, householdID, bookingID int64) ([]BookingOverride, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+overrideColumns+`
		 FROM booking_overrides o
		 JOIN bookings b ON b.id = o.booking_id
		 WHERE b.id = ? AND b.household_id = ?
		 ORDER BY o.starts_on, o.id`, bookingID, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BookingOverride
	for rows.Next() {
		o, err := scanOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListOverridesForHousehold returns all overrides of a household keyed by
// booking id, so a whole report is built from one query.
func (s *Store) ListOverridesForHousehold(ctx context.Context, householdID int64) (map[int64][]BookingOverride, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+overrideColumns+`
		 FROM booking_overrides o
		 JOIN bookings b ON b.id = o.booking_id
		 WHERE b.household_id = ?
		 ORDER BY o.booking_id, o.starts_on, o.id`, householdID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64][]BookingOverride)
	for rows.Next() {
		o, err := scanOverride(rows)
		if err != nil {
			return nil, err
		}
		out[o.BookingID] = append(out[o.BookingID], o)
	}
	return out, rows.Err()
}

// CreateOverride adds an amount override to a booking of a household.
func (s *Store) CreateOverride(ctx context.Context, householdID int64, o BookingOverride) (BookingOverride, error) {
	var out BookingOverride
	err := s.withTx(ctx, func(tx *Store) error {
		res, err := tx.q.ExecContext(ctx,
			`INSERT INTO booking_overrides (booking_id, starts_on, ends_on, amount_cents, note)
			 SELECT id, ?, ?, ?, ? FROM bookings WHERE id = ? AND household_id = ?`,
			o.StartsOn, o.EndsOn, o.AmountCents, o.Note, o.BookingID, householdID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		out, err = scanOverride(tx.q.QueryRowContext(ctx,
			`SELECT `+overrideColumns+` FROM booking_overrides o WHERE o.id = ?`, id))
		return err
	})
	if err != nil {
		return BookingOverride{}, err
	}
	return out, nil
}

// UpdateOverride changes an override of a household's booking.
func (s *Store) UpdateOverride(ctx context.Context, householdID int64, o BookingOverride) error {
	return affected(s.q.ExecContext(ctx,
		`UPDATE booking_overrides SET starts_on = ?, ends_on = ?, amount_cents = ?, note = ?
		 WHERE id = ? AND booking_id IN (SELECT id FROM bookings WHERE household_id = ?)`,
		o.StartsOn, o.EndsOn, o.AmountCents, o.Note, o.ID, householdID))
}

// DeleteOverride removes an override of a household's booking.
func (s *Store) DeleteOverride(ctx context.Context, householdID, id int64) error {
	return affected(s.q.ExecContext(ctx,
		`DELETE FROM booking_overrides
		 WHERE id = ? AND booking_id IN (SELECT id FROM bookings WHERE household_id = ?)`,
		id, householdID))
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
