package calc

import "github.com/daknoblo/Haushaltsbuch/internal/store"

// CostReport includes all allocated expenses, independently of reimbursement.
// Invalid or missing allocations are listed instead of silently changing shares.
type CostReport struct {
	Lines           []ShareLine
	InvalidBookings []store.Booking
}

// Costs reports monthly average costs over the selected planning period.
func Costs(d Data, months []string) CostReport { return costs(d, months, true) }

// CostsTotal sums the normalized monthly costs without averaging.
func CostsTotal(d Data, months []string) CostReport { return costs(d, months, false) }

func costs(d Data, months []string, averaged bool) CostReport {
	var rep CostReport
	known := make(map[int64]store.Member, len(d.Members))
	for _, m := range d.Members {
		known[m.ID] = m
	}
	for _, value := range periodBookingValues(d, months, averaged) {
		b := value.Booking
		if b.Direction != store.DirExpense {
			continue
		}
		if len(value.Shares) == 0 || !validShares(value, known) {
			rep.InvalidBookings = append(rep.InvalidBookings, b)
			continue
		}
		var payer store.Member
		if b.PayerMemberID != nil {
			payer = known[*b.PayerMemberID]
		}
		rep.Lines = append(rep.Lines, ShareLine{
			Booking: b, Payer: payer, Cents: value.Cents,
			Shares: value.Shares, Splits: d.Splits[b.ID], Carriers: len(value.Shares),
			WithoutSettlement: !b.Settle || payer.ID == 0 || payerCarriesAlone(value),
		})
	}
	sortShareLines(rep.Lines)
	return rep
}

// LinesFor selects the costs carried by a person, or all costs for Everyone.
func (r CostReport) LinesFor(member int64) []ShareLine { return shareLinesFor(r.Lines, member) }

// CarriedBy splits a scope's costs into shared, personal and fixed portions.
func (r CostReport) CarriedBy(member int64) Carried { return carriedBy(r.Lines, member) }
