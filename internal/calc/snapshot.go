package calc

import "github.com/daknoblo/Haushaltsbuch/internal/store"

// FromSnapshot exposes the planning input without persisting derived rows.
// The snapshot and returned data are read-only for the lifetime of a request.
func FromSnapshot(s store.HouseholdSnapshot) Data {
	d := Data{Members: s.Members, Categories: s.Categories, Tags: s.Tags,
		Splits: make(map[int64][]store.BookingSplit), TagLinks: make(map[int64][]int64),
		Overrides: make(map[int64][]store.BookingOverride)}
	for _, b := range s.Bookings {
		d.Bookings = append(d.Bookings, b.Booking)
		d.Splits[b.Booking.ID], d.TagLinks[b.Booking.ID], d.Overrides[b.Booking.ID] = b.Splits, b.TagIDs, b.Overrides
	}
	return d
}
