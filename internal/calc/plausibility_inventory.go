package calc

import "github.com/daknoblo/Haushaltsbuch/internal/store"

// CheckInventory keeps plan counts separate from imported and accepted rows.
// Both arguments must come from the same transactional snapshot.
func (r *PlausibilityResult) CheckInventory(s store.HouseholdSnapshot, stats store.Stats) {
	f := Finding{Code: "inventory"}
	r.Compare(f, int64(len(s.Bookings)), int64(stats.Bookings))
	r.Compare(f, int64(len(s.Bank.Transactions)), int64(stats.BankTransactions))
	r.Compare(f, int64(len(s.Members)), int64(stats.Members))
	r.Compare(f, int64(len(s.Categories)), int64(stats.Categories))
	r.Compare(f, int64(len(s.Tags)), int64(stats.Tags))
	categories, tags := map[int64]bool{}, map[int64]bool{}
	recurring, splits, overrides, accepted := 0, 0, 0, 0
	for _, row := range s.Bookings {
		categories[row.Booking.CategoryID] = true
		for _, id := range row.TagIDs {
			tags[id] = true
		}
		if row.Booking.Frequency != store.FreqOnce {
			recurring++
		}
		splits += len(row.Splits)
		overrides += len(row.Overrides)
	}
	for _, row := range s.Bank.Transactions {
		categories[row.CategoryID] = true
		for _, id := range row.TagIDs {
			tags[id] = true
		}
		if row.Decision == "accepted" {
			accepted++
		}
	}
	unusedCategories, unusedTags := 0, 0
	for _, category := range s.Categories {
		if !categories[category.ID] {
			unusedCategories++
		}
	}
	for _, tag := range s.Tags {
		if !tags[tag.ID] {
			unusedTags++
		}
	}
	for _, pair := range [][2]int{{recurring, stats.Recurring}, {splits, stats.Splits}, {overrides, stats.Overrides}, {accepted, stats.AcceptedPayments}, {unusedCategories, stats.UnusedCategories}, {unusedTags, stats.UnusedTags}} {
		r.Compare(f, int64(pair[0]), int64(pair[1]))
	}
}
