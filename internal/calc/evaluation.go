package calc

import (
	"math"
	"sort"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

type bookingValue struct {
	Booking    store.Booking
	Cents      int64
	Shares     map[int64]int64
	Unassigned int64
	Complete   bool
}

func (v bookingValue) scoped(member int64) (int64, bool) {
	if member == Everyone {
		return v.Cents, true
	}
	cents, ok := v.Shares[member]
	return cents, ok
}

type evaluatedData struct {
	months   map[string][]bookingValue
	totals   map[string][]bookingValue
	averages map[string][]bookingValue
}

// Prepare evaluates the requested periods once for a read-only request
// snapshot. Reports can then reuse the same cent-exact values across scopes
// and charts. Neither the returned data nor its source may be modified.
func Prepare(d Data, periods ...[]string) Data {
	d.evaluated = &evaluatedData{
		months:   make(map[string][]bookingValue),
		totals:   make(map[string][]bookingValue),
		averages: make(map[string][]bookingValue),
	}
	for _, months := range periods {
		key := strings.Join(months, ",")
		if _, ok := d.evaluated.averages[key]; ok {
			continue
		}
		for _, month := range months {
			if _, ok := d.evaluated.months[month]; !ok {
				d.evaluated.months[month] = bookingValues(d, month)
			}
		}
		totals := periodBookingValues(d, months, false)
		d.evaluated.totals[key] = totals
		d.evaluated.averages[key] = averageBookingValues(totals, int64(len(months)))
	}
	return d
}

func bookingValues(d Data, month string) []bookingValue {
	if d.evaluated != nil {
		if values, ok := d.evaluated.months[month]; ok {
			return values
		}
	}
	values := make([]bookingValue, 0, len(d.Bookings))
	for _, b := range d.Bookings {
		if ActiveIn(b, month) {
			values = append(values, evaluateBooking(b, d.Splits[b.ID], d.Overrides[b.ID], month))
		}
	}
	return values
}

func evaluateBooking(b store.Booking, splits []store.BookingSplit, overrides []store.BookingOverride, month string) bookingValue {
	v := bookingValue{
		Booking:  b,
		Cents:    MonthlyCents(b, overrides, month),
		Complete: completeSplit(b, splits, AmountFor(b, overrides, month)),
	}
	raw := make(map[int64]float64, len(splits))
	for _, s := range splits {
		switch b.SplitMode {
		case store.SplitPercent:
			if percent := ClampPercent(s.Value); percent > 0 {
				raw[s.MemberID] += float64(v.Cents) * percent / 100
			}
		case store.SplitFixed:
			if cents := clampAmount(s.Value); cents != 0 {
				raw[s.MemberID] += cents * monthlyFactor(b)
			}
		default:
			raw[s.MemberID] += float64(v.Cents) / float64(len(splits))
		}
	}
	var assigned float64
	for _, cents := range raw {
		assigned += cents
	}
	target := round(assigned)
	if v.Complete && len(raw) > 0 {
		target = v.Cents
	}
	v.Shares = balancedCents(raw, target)
	v.Unassigned = v.Cents - sumCents(v.Shares)
	return v
}

// Validate before monthly normalization, so even a missing cent in a yearly
// fixed split remains visible rather than disappearing into rounding.
func completeSplit(b store.Booking, splits []store.BookingSplit, amount int64) bool {
	var sum float64
	switch b.SplitMode {
	case store.SplitPercent:
		for _, s := range splits {
			sum += ClampPercent(s.Value)
		}
		return math.Abs(sum-100) < 1e-9
	case store.SplitFixed:
		for _, s := range splits {
			sum += clampAmount(s.Value)
		}
		return sum == float64(amount)
	default:
		return len(splits) > 0
	}
}

func periodBookingValues(d Data, months []string, averaged bool) []bookingValue {
	if d.evaluated != nil {
		cache := d.evaluated.totals
		if averaged {
			cache = d.evaluated.averages
		}
		if values, ok := cache[strings.Join(months, ",")]; ok {
			return values
		}
	}
	byID := make(map[int64]int)
	totals := make([]bookingValue, 0, len(d.Bookings))
	for _, month := range months {
		for _, v := range bookingValues(d, month) {
			i, ok := byID[v.Booking.ID]
			if !ok {
				i = len(totals)
				byID[v.Booking.ID] = i
				totals = append(totals, bookingValue{
					Booking: v.Booking, Shares: make(map[int64]int64), Complete: true,
				})
			}
			total := &totals[i]
			total.Cents += v.Cents
			total.Unassigned += v.Unassigned
			total.Complete = total.Complete && v.Complete
			for id, cents := range v.Shares {
				total.Shares[id] += cents
			}
		}
	}
	if averaged {
		return averageBookingValues(totals, int64(len(months)))
	}
	return totals
}

// Average at booking level, distributing indivisible cents within each
// direction. Every scope and breakdown then sums those same rounded leaves.
func averageBookingValues(totals []bookingValue, n int64) []bookingValue {
	if n == 0 {
		return nil
	}
	amounts := make(map[int64]int64, len(totals))
	for _, direction := range []store.Direction{store.DirIncome, store.DirExpense} {
		raw := make(map[int64]float64)
		var total int64
		for _, v := range totals {
			if v.Booking.Direction == direction {
				raw[v.Booking.ID] = float64(v.Cents) / float64(n)
				total += v.Cents
			}
		}
		for id, cents := range balancedCents(raw, total/n) {
			amounts[id] = cents
		}
	}
	out := make([]bookingValue, 0, len(totals))
	for _, total := range totals {
		v := total
		v.Cents = amounts[v.Booking.ID]
		raw := make(map[int64]float64, len(v.Shares))
		for id, cents := range v.Shares {
			raw[id] = float64(cents) / float64(n)
		}
		target := sumCents(v.Shares) / n
		if v.Complete {
			target = v.Cents
		}
		v.Shares = balancedCents(raw, target)
		v.Unassigned = v.Cents - sumCents(v.Shares)
		out = append(out, v)
	}
	return out
}

func sumCents(values map[int64]int64) int64 {
	var total int64
	for _, cents := range values {
		total += cents
	}
	return total
}

// balancedCents uses largest remainders and stable IDs to conserve cents.
// The target must be a rounding of the sum of the supplied values.
func balancedCents(values map[int64]float64, total int64) map[int64]int64 {
	out := make(map[int64]int64, len(values))
	ids := make([]int64, 0, len(values))
	var assigned int64
	for id, v := range values {
		out[id] = int64(math.Floor(v))
		assigned += out[id]
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := values[ids[i]]-float64(out[ids[i]]), values[ids[j]]-float64(out[ids[j]])
		if a != b {
			return a > b
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		if assigned >= total {
			break
		}
		out[id]++
		assigned++
	}
	return out
}
