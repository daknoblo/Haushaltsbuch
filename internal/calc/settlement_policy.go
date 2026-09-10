package calc

import "github.com/daknoblo/Haushaltsbuch/internal/store"

// PayerCarriesAlone reports whether the payer carries the complete expense.
// Invalid or incomplete splits are not suppressed by this automatic rule.
func PayerCarriesAlone(b store.Booking, splits []store.BookingSplit, overrides []store.BookingOverride, month string) bool {
	return payerCarriesAlone(evaluateBooking(b, splits, overrides, month))
}

func payerCarriesAlone(value bookingValue) bool {
	b := value.Booking
	if b.Direction != store.DirExpense || b.PayerMemberID == nil || !value.Complete || len(value.Shares) != 1 {
		return false
	}
	_, carries := value.Shares[*b.PayerMemberID]
	return carries
}

// SettlementEnabled keeps the saved preference separate from the automatic
// exclusion, so adding another carrier restores only a previously enabled
// settlement, never one the user deliberately disabled.
func SettlementEnabled(b store.Booking, splits []store.BookingSplit, overrides []store.BookingOverride, month string) bool {
	return b.Direction == store.DirExpense && b.Settle && !PayerCarriesAlone(b, splits, overrides, month)
}
