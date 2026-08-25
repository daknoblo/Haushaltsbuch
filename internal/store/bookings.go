package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

type scanner interface {
	Scan(dest ...any) error
}

// SplitInput is a member's share of a booking as submitted by the UI.
type SplitInput struct {
	MemberID int64
	Value    float64
}

const bookingColumns = `id, household_id, category_id, section_id, direction, name, note,
	amount_cents, frequency, interval_n, starts_on, ends_on, cost_nature,
	budget_class, split_mode, sort_order, created_at, updated_at`

func scanBooking(sc scanner) (Booking, error) {
	var (
		b         Booking
		sectionID sql.NullInt64
	)
	err := sc.Scan(
		&b.ID, &b.HouseholdID, &b.CategoryID, &sectionID, &b.Direction, &b.Name, &b.Note,
		&b.AmountCents, &b.Frequency, &b.Interval, &b.StartsOn, &b.EndsOn, &b.CostNature,
		&b.BudgetClass, &b.SplitMode, &b.SortOrder, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return Booking{}, err
	}
	if sectionID.Valid {
		v := sectionID.Int64
		b.SectionID = &v
	}
	return b, nil
}

// ListBookings returns all bookings of a household, ordered for display.
func (s *Store) ListBookings(ctx context.Context, householdID int64) ([]Booking, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+bookingColumns+` FROM bookings
		 WHERE household_id = ?
		 ORDER BY sort_order, id`, householdID)
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

// These sub-selects resolve to NULL unless the referenced row belongs to the
// same household, so a forged id cannot create a cross-household reference.
// category_id is NOT NULL, so a forged category is rejected by the constraint.
const (
	sectionRef  = `(SELECT id FROM sections WHERE id = ? AND household_id = ?)`
	categoryRef = `(SELECT id FROM categories WHERE id = ? AND household_id = ?)`
)

// CreateBooking inserts a booking with its splits and tags and returns it.
// Timestamps and the sort order are assigned automatically.
func (s *Store) CreateBooking(ctx context.Context, b Booking, splits []SplitInput, tagIDs []int64) (Booking, error) {
	var created Booking
	err := s.withTx(ctx, func(tx *Store) error {
		ts := now()
		res, err := tx.q.ExecContext(ctx,
			`INSERT INTO bookings
				(household_id, category_id, section_id, direction, name, note, amount_cents,
				 frequency, interval_n, starts_on, ends_on, cost_nature, budget_class,
				 split_mode, sort_order, created_at, updated_at)
			 VALUES (?, `+categoryRef+`, `+sectionRef+`, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
				(SELECT COALESCE(MAX(sort_order)+1, 0) FROM bookings WHERE household_id = ?), ?, ?)`,
			b.HouseholdID,
			b.CategoryID, b.HouseholdID,
			nullInt(b.SectionID), b.HouseholdID,
			string(b.Direction), b.Name, b.Note, b.AmountCents,
			string(b.Frequency), b.Interval, b.StartsOn, b.EndsOn,
			string(b.CostNature), string(b.BudgetClass), string(b.SplitMode),
			b.HouseholdID, ts, ts,
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
				category_id = `+categoryRef+`, section_id = `+sectionRef+`,
				direction = ?, name = ?, note = ?, amount_cents = ?, frequency = ?,
				interval_n = ?, starts_on = ?, ends_on = ?, cost_nature = ?,
				budget_class = ?, split_mode = ?, updated_at = ?
			 WHERE id = ? AND household_id = ?`,
			b.CategoryID, b.HouseholdID,
			nullInt(b.SectionID), b.HouseholdID,
			string(b.Direction), b.Name, b.Note, b.AmountCents, string(b.Frequency),
			b.Interval, b.StartsOn, b.EndsOn, string(b.CostNature),
			string(b.BudgetClass), string(b.SplitMode), now(), b.ID, b.HouseholdID,
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

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}
