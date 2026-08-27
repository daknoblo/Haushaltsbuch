package store

import "context"

// Stats is an inventory of the book rather than a report about the money: how
// much is in it, how much of it nothing points at, and how much room it takes.
type Stats struct {
	Households       int
	Members          int
	Categories       int
	UnusedCategories int
	Tags             int
	UnusedTags       int
	Bookings         int
	Recurring        int
	Overrides        int
	Splits           int
	// FirstMonth and LastMonth are the outer edges of what the book plans for,
	// which says at a glance whether it still reaches into the coming year.
	FirstMonth string
	LastMonth  string
	UpdatedAt  string
	SizeBytes  int64
}

// OneOff is how many bookings happen a single time.
func (s Stats) OneOff() int { return s.Bookings - s.Recurring }

// Stats counts what the household holds. Everything but the household count and
// the file size is scoped to the one asked for.
func (s *Store) Stats(ctx context.Context, householdID int64) (Stats, error) {
	var out Stats
	err := s.q.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM households),
			(SELECT COUNT(*) FROM members WHERE household_id = ?1),
			(SELECT COUNT(*) FROM categories WHERE household_id = ?1),
			(SELECT COUNT(*) FROM categories c WHERE c.household_id = ?1
			   AND NOT EXISTS (SELECT 1 FROM bookings b WHERE b.category_id = c.id)),
			(SELECT COUNT(*) FROM tags WHERE household_id = ?1),
			(SELECT COUNT(*) FROM tags t WHERE t.household_id = ?1
			   AND NOT EXISTS (SELECT 1 FROM booking_tags bt WHERE bt.tag_id = t.id)),
			(SELECT COUNT(*) FROM bookings WHERE household_id = ?1),
			(SELECT COUNT(*) FROM bookings WHERE household_id = ?1 AND frequency <> ?2),
			(SELECT COUNT(*) FROM booking_overrides o
			   JOIN bookings b ON b.id = o.booking_id WHERE b.household_id = ?1),
			(SELECT COUNT(*) FROM booking_splits sp
			   JOIN bookings b ON b.id = sp.booking_id WHERE b.household_id = ?1),
			(SELECT COALESCE(MIN(starts_on), '') FROM bookings WHERE household_id = ?1 AND starts_on <> ''),
			(SELECT COALESCE(MAX(ends_on), '') FROM bookings WHERE household_id = ?1 AND ends_on <> ''),
			(SELECT COALESCE(MAX(updated_at), '') FROM bookings WHERE household_id = ?1)`,
		householdID, string(FreqOnce),
	).Scan(
		&out.Households, &out.Members, &out.Categories, &out.UnusedCategories,
		&out.Tags, &out.UnusedTags, &out.Bookings, &out.Recurring,
		&out.Overrides, &out.Splits, &out.FirstMonth, &out.LastMonth, &out.UpdatedAt,
	)
	if err != nil {
		return Stats{}, err
	}

	// The pages the database occupies rather than the file on disk: the store
	// never learns its own path, and the answer is the same number.
	var pages, size int64
	if err := s.q.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return Stats{}, err
	}
	if err := s.q.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return Stats{}, err
	}
	out.SizeBytes = pages * size
	return out, nil
}
