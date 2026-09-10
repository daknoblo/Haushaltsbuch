package calc

import "github.com/daknoblo/Haushaltsbuch/internal/store"

// MonthsWithIncome selects months with an income entry for the chosen scope.
// Presence, not amount, distinguishes missing income from an explicit zero.
func MonthsWithIncome(d Data, months []string, member int64) []string {
	out := make([]string, 0, len(months))
	for _, month := range months {
		for _, b := range d.Bookings {
			if ActiveIn(b, month) && incomeForScope(b, d.Splits[b.ID], member) {
				out = append(out, month)
				break
			}
		}
	}
	return out
}

func incomeForScope(b store.Booking, splits []store.BookingSplit, member int64) bool {
	if b.Direction != store.DirIncome {
		return false
	}
	if member == Everyone {
		return true
	}
	for _, split := range splits {
		if split.MemberID == member {
			return true
		}
	}
	return false
}
