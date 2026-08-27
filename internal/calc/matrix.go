package calc

import (
	"sort"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Bands of the year matrix. A booking lands in one of the first three by its
// direction and cost nature; every expense additionally counts towards the
// 50/30/20 band, which is a second view of the same money rather than a fourth
// pot.
const (
	BandIncome   = "income"
	BandFixed    = "fixed"
	BandVariable = "variable"
	BandClass    = "class"
)

// MatrixRow is one line of the year matrix: a category, one of its bookings, or
// a summary. Cents and Share hold one entry per month of the range.
type MatrixRow struct {
	Key int64
	// Label names a category or booking; LabelKey names a fixed line and is
	// translated by the caller. Exactly one of them is set.
	Label       string
	LabelKey    string
	Color       string
	Icon        string
	Cents       []int64
	Share       []float64
	TotalCents  int64
	MeanCents   int64
	MedianCents int64
	ShareTotal  float64
	Children    []MatrixRow
}

// MatrixBand groups rows under one heading and carries their sum.
type MatrixBand struct {
	Key   string
	Rows  []MatrixRow
	Total MatrixRow
}

// Matrix is the year at a glance: every category as a row, every month as a
// column, plus the lines that only make sense across the whole table.
type Matrix struct {
	Months  []string
	Bands   []MatrixBand
	Expense MatrixRow
	Surplus MatrixRow
	// Target is what the 50/30/20 rule sets aside for saving, so the class band
	// can be read against something.
	Target MatrixRow
}

// Empty reports whether the matrix has nothing to show.
func (m Matrix) Empty() bool {
	return len(m.Months) == 0 || (m.Expense.TotalCents == 0 && m.incomeEmpty())
}

func (m Matrix) incomeEmpty() bool {
	for _, b := range m.Bands {
		if b.Key == BandIncome {
			return b.Total.TotalCents == 0
		}
	}
	return true
}

// Band returns the band with the given key.
func (m Matrix) Band(key string) MatrixBand {
	for _, b := range m.Bands {
		if b.Key == key {
			return b
		}
	}
	return MatrixBand{Key: key}
}

// BuildMatrix lays out one household's year. With a member other than Everyone
// every figure is that member's own share, matching the rest of the reports.
func BuildMatrix(d Data, months []string, member int64) Matrix {
	n := len(months)
	if n == 0 {
		return Matrix{}
	}

	cats := make(map[int64]store.Category, len(d.Categories))
	for _, c := range d.Categories {
		cats[c.ID] = c
	}

	type rowKey struct {
		band string
		cat  int64
	}
	byCategory := make(map[rowKey][]int64)
	byBooking := make(map[rowKey]map[int64][]int64)
	bookings := make(map[int64]store.Booking, len(d.Bookings))
	byBand := make(map[string][]int64)
	byClass := make(map[store.BudgetClass][]int64)

	for i, month := range months {
		for _, b := range d.Bookings {
			if !ActiveIn(b, month) {
				continue
			}
			amount := float64(AmountFor(b, d.Overrides[b.ID], month)) * monthlyFactor(b)
			if member != Everyone {
				shares, _ := allocate(amount, b, d.Splits[b.ID])
				share, ok := shares[member]
				if !ok {
					continue
				}
				amount = share
			}
			cents := round(amount)
			if cents == 0 {
				continue
			}

			band := BandIncome
			if b.Direction == store.DirExpense {
				band = BandVariable
				if b.CostNature == store.CostFix {
					band = BandFixed
				}
				addAt(byClass, b.BudgetClass, i, n, cents)
			}

			k := rowKey{band, b.CategoryID}
			addAt(byCategory, k, i, n, cents)
			addAt(byBand, band, i, n, cents)
			if byBooking[k] == nil {
				byBooking[k] = make(map[int64][]int64)
			}
			addAt(byBooking[k], b.ID, i, n, cents)
			bookings[b.ID] = b
		}
	}

	m := Matrix{Months: months}
	for _, band := range []string{BandIncome, BandFixed, BandVariable} {
		mb := MatrixBand{Key: band, Total: summarize(MatrixRow{LabelKey: "matrix.total." + band}, byBand[band], n)}
		for key, cents := range byCategory {
			if key.band != band {
				continue
			}
			c := cats[key.cat]
			row := summarize(MatrixRow{Key: key.cat, Label: c.Name, Color: c.Color, Icon: c.Icon}, cents, n)
			row.Share, row.ShareTotal = shareOf(row, mb.Total)
			for id, child := range byBooking[key] {
				b := bookings[id]
				kid := summarize(MatrixRow{Key: id, Label: b.Name}, child, n)
				row.Children = append(row.Children, kid)
			}
			sortRows(row.Children)
			mb.Rows = append(mb.Rows, row)
		}
		sortRows(mb.Rows)
		m.Bands = append(m.Bands, mb)
	}

	income := m.Band(BandIncome).Total
	expense := addRows(m.Band(BandFixed).Total, m.Band(BandVariable).Total, n)
	m.Expense = summarize(MatrixRow{LabelKey: "matrix.total.expense"}, expense, n)
	m.Surplus = summarize(MatrixRow{LabelKey: "matrix.surplus"}, diffRows(income.Cents, expense, n), n)
	m.Target = summarize(MatrixRow{LabelKey: "matrix.savingTarget"}, scale(income.Cents, store.ClassSaving.TargetPercent()), n)

	// The two expense bands say how the spending splits; the classes say what
	// share of the income it eats, which is what the rule is about.
	for i := range m.Bands {
		if m.Bands[i].Key == BandIncome {
			continue
		}
		m.Bands[i].Total.Share, m.Bands[i].Total.ShareTotal = shareOf(m.Bands[i].Total, m.Expense)
	}
	m.Bands = append(m.Bands, classBand(byClass, income, n))
	return m
}

// classBand is the 50/30/20 view: the same expenses again, grouped by what the
// household decided they were for and measured against what came in.
func classBand(byClass map[store.BudgetClass][]int64, income MatrixRow, n int) MatrixBand {
	mb := MatrixBand{Key: BandClass}
	total := make([]int64, n)
	for _, c := range []store.BudgetClass{store.ClassNeed, store.ClassWant, store.ClassSaving} {
		row := summarize(MatrixRow{LabelKey: "class." + string(c)}, byClass[c], n)
		row.Share, row.ShareTotal = shareOf(row, income)
		mb.Rows = append(mb.Rows, row)
		for i, v := range byClass[c] {
			total[i] += v
		}
	}
	mb.Total = summarize(MatrixRow{LabelKey: "matrix.total.class"}, total, n)
	mb.Total.Share, mb.Total.ShareTotal = shareOf(mb.Total, income)
	return mb
}

// summarize fills in the figures that only exist across the whole row. The mean
// divides by every month of the range, empty ones included, matching
// PeriodReport.
func summarize(row MatrixRow, cents []int64, n int) MatrixRow {
	row.Cents = make([]int64, n)
	copy(row.Cents, cents)
	for _, v := range row.Cents {
		row.TotalCents += v
	}
	row.MeanCents = row.TotalCents / int64(n)
	row.MedianCents = median(row.Cents)
	return row
}

// shareOf is a row's share of its band, per month and over the whole range.
func shareOf(row, base MatrixRow) ([]float64, float64) {
	out := make([]float64, len(row.Cents))
	for i, v := range row.Cents {
		if i < len(base.Cents) && base.Cents[i] != 0 {
			out[i] = float64(v) / float64(base.Cents[i]) * 100
		}
	}
	var total float64
	if base.TotalCents != 0 {
		total = float64(row.TotalCents) / float64(base.TotalCents) * 100
	}
	return out, total
}

func addAt[K comparable](m map[K][]int64, key K, i, n int, v int64) {
	if m[key] == nil {
		m[key] = make([]int64, n)
	}
	m[key][i] += v
}

func addRows(a, b MatrixRow, n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		if i < len(a.Cents) {
			out[i] += a.Cents[i]
		}
		if i < len(b.Cents) {
			out[i] += b.Cents[i]
		}
	}
	return out
}

func diffRows(a, b []int64, n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		if i < len(a) {
			out[i] += a[i]
		}
		if i < len(b) {
			out[i] -= b[i]
		}
	}
	return out
}

func scale(cents []int64, percent int) []int64 {
	out := make([]int64, len(cents))
	for i, v := range cents {
		out[i] = v * int64(percent) / 100
	}
	return out
}

// median answers what a typical month looks like, which a mean cannot when a
// single month is far off the rest.
func median(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]int64(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

func sortRows(rows []MatrixRow) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].TotalCents > rows[j].TotalCents })
}
