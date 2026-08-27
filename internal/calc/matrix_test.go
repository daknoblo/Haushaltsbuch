package calc

import (
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// budgetBook is the household book this feature was built from: a salary paid
// in the first three months, rent and insurance every month, and a savings
// plan classified as such.
func budgetBook() Data {
	monthly := func(id, cat int64, name string, cents int64, nature store.CostNature, class store.BudgetClass) store.Booking {
		return store.Booking{
			ID: id, CategoryID: cat, Direction: store.DirExpense, Name: name,
			AmountCents: cents, Frequency: store.FreqMonthly, Interval: 1,
			StartsOn: "2026-01-01", SplitMode: store.SplitEqual,
			CostNature: nature, BudgetClass: class,
		}
	}
	salary := func(id int64, month string, cents int64) store.Booking {
		return store.Booking{
			ID: id, CategoryID: 10, Direction: store.DirIncome, Name: "Gehalt",
			AmountCents: cents, Frequency: store.FreqOnce, StartsOn: month,
			SplitMode: store.SplitEqual,
		}
	}
	return Data{
		Members: members(),
		Categories: []store.Category{
			{ID: 10, Name: "Gehalt", Classification: store.DirIncome},
			{ID: 20, Name: "Wohnen", Classification: store.DirExpense},
			{ID: 21, Name: "Sparen", Classification: store.DirExpense},
			{ID: 22, Name: "Lebenshaltung", Classification: store.DirExpense},
		},
		Bookings: []store.Booking{
			salary(1, "2026-01-15", 370100),
			salary(2, "2026-02-15", 454100),
			salary(3, "2026-03-15", 360500),
			monthly(4, 20, "Miete", 98100, store.CostFix, store.ClassNeed),
			monthly(5, 20, "GEZ", 1800, store.CostFix, store.ClassNeed),
			monthly(6, 21, "Sparplan", 100000, store.CostFix, store.ClassSaving),
			monthly(7, 22, "Lebensmittel", 25000, store.CostVariable, store.ClassNeed),
		},
	}
}

func TestMatrixSumsTheYearPerBand(t *testing.T) {
	m := BuildMatrix(budgetBook(), calendarYear(), Everyone)

	if got, want := m.Band(BandIncome).Total.TotalCents, int64(1184700); got != want {
		t.Errorf("income = %d, want %d", got, want)
	}
	// Miete, GEZ and the savings plan run all twelve months.
	if got, want := m.Band(BandFixed).Total.TotalCents, int64((98100+1800+100000)*12); got != want {
		t.Errorf("fixed = %d, want %d", got, want)
	}
	if got, want := m.Band(BandVariable).Total.TotalCents, int64(25000*12); got != want {
		t.Errorf("variable = %d, want %d", got, want)
	}
	if got, want := m.Expense.TotalCents, m.Band(BandFixed).Total.TotalCents+m.Band(BandVariable).Total.TotalCents; got != want {
		t.Errorf("expenses = %d, want the two bands together %d", got, want)
	}
}

// The mean divides by the whole year, the median says what a typical month
// looks like. Nine months without a salary make those two disagree, which is
// exactly why both are shown.
func TestMatrixMeanAndMedianDisagreeOnAnUnevenYear(t *testing.T) {
	m := BuildMatrix(budgetBook(), calendarYear(), Everyone)
	income := m.Band(BandIncome).Total

	if income.MeanCents != 98725 {
		t.Errorf("mean = %d, want 98725", income.MeanCents)
	}
	if income.MedianCents != 0 {
		t.Errorf("median = %d, want 0 for nine empty months", income.MedianCents)
	}
}

func TestMatrixSavingTargetIsAFifthOfIncome(t *testing.T) {
	m := BuildMatrix(budgetBook(), calendarYear(), Everyone)
	if got, want := m.Target.TotalCents, int64(236940); got != want {
		t.Errorf("saving target = %d, want %d (20 %% of 1.184.700)", got, want)
	}
}

func TestMatrixSurplusIsIncomeLessExpenses(t *testing.T) {
	m := BuildMatrix(budgetBook(), calendarYear(), Everyone)
	income := m.Band(BandIncome).Total
	for i := range m.Months {
		want := income.Cents[i] - m.Expense.Cents[i]
		if m.Surplus.Cents[i] != want {
			t.Errorf("surplus of %s = %d, want %d", m.Months[i], m.Surplus.Cents[i], want)
		}
	}
}

// A category is a row, its bookings are the lines underneath, and the two have
// to agree or the table lies about where the money went.
func TestMatrixCategoryRowsAddUpFromTheirBookings(t *testing.T) {
	m := BuildMatrix(budgetBook(), calendarYear(), Everyone)

	var wohnen MatrixRow
	for _, r := range m.Band(BandFixed).Rows {
		if r.Label == "Wohnen" {
			wohnen = r
		}
	}
	if len(wohnen.Children) != 2 {
		t.Fatalf("Wohnen has %d bookings, want Miete and GEZ", len(wohnen.Children))
	}
	var sum int64
	for _, c := range wohnen.Children {
		sum += c.TotalCents
	}
	if sum != wohnen.TotalCents {
		t.Errorf("bookings add up to %d, category says %d", sum, wohnen.TotalCents)
	}
	if wohnen.Children[0].Label != "Miete" {
		t.Errorf("first booking is %q, want the largest one", wohnen.Children[0].Label)
	}
}

// The class band is the same expenses seen a second way, so it has to come to
// the same total.
func TestMatrixClassBandCoversEveryExpense(t *testing.T) {
	m := BuildMatrix(budgetBook(), calendarYear(), Everyone)
	if got, want := m.Band(BandClass).Total.TotalCents, m.Expense.TotalCents; got != want {
		t.Errorf("classes = %d, expenses = %d", got, want)
	}
	if got := m.Band(BandClass).Rows[2].TotalCents; got != 100000*12 {
		t.Errorf("saving = %d, want the savings plan", got)
	}
}

// In a person view every row is that person's share, so a booking split down
// the middle halves the whole table.
func TestMatrixPersonViewShowsOnlyTheOwnShare(t *testing.T) {
	d := budgetBook()
	d.Splits = map[int64][]store.BookingSplit{
		4: {{BookingID: 4, MemberID: 1}, {BookingID: 4, MemberID: 2}},
	}
	whole := BuildMatrix(d, calendarYear(), Everyone)
	mine := BuildMatrix(d, calendarYear(), 1)

	var wholeRent, myRent int64
	for _, r := range whole.Band(BandFixed).Rows {
		for _, c := range r.Children {
			if c.Label == "Miete" {
				wholeRent = c.TotalCents
			}
		}
	}
	for _, r := range mine.Band(BandFixed).Rows {
		for _, c := range r.Children {
			if c.Label == "Miete" {
				myRent = c.TotalCents
			}
		}
	}
	if wholeRent == 0 || myRent*2 != wholeRent {
		t.Errorf("own share = %d, household = %d, want half", myRent, wholeRent)
	}
}

func TestMatrixWithoutMonthsIsEmpty(t *testing.T) {
	if m := BuildMatrix(budgetBook(), nil, Everyone); !m.Empty() {
		t.Error("a range without months produced a matrix")
	}
}
