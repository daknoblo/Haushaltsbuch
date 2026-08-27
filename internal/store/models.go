package store

// Direction says whether a booking feeds the budget or draws from it.
type Direction string

// Directions of a booking.
const (
	DirIncome  Direction = "income"
	DirExpense Direction = "expense"
)

// Valid reports whether d is a known direction.
func (d Direction) Valid() bool {
	return d == DirIncome || d == DirExpense
}

// Frequency describes how often a booking occurs.
type Frequency string

// Frequencies of a booking.
const (
	FreqOnce      Frequency = "once"
	FreqWeekly    Frequency = "weekly"
	FreqMonthly   Frequency = "monthly"
	FreqQuarterly Frequency = "quarterly"
	FreqYearly    Frequency = "yearly"
)

// Valid reports whether f is a known frequency.
func (f Frequency) Valid() bool {
	switch f {
	case FreqOnce, FreqWeekly, FreqMonthly, FreqQuarterly, FreqYearly:
		return true
	default:
		return false
	}
}

// Recurring reports whether f repeats rather than happening a single time.
func (f Frequency) Recurring() bool {
	return f != FreqOnce
}

// MonthlyFactor returns the factor to normalise an amount of this frequency to
// a monthly-equivalent value. A recurring amount is spread evenly across the
// months it covers, so a yearly premium contributes a twelfth every month.
func (f Frequency) MonthlyFactor() float64 {
	switch f {
	case FreqWeekly:
		return 52.0 / 12.0
	case FreqQuarterly:
		return 1.0 / 3.0
	case FreqYearly:
		return 1.0 / 12.0
	default: // once, monthly
		return 1.0
	}
}

// CostNature classifies a booking as fixed or variable.
type CostNature string

// Cost natures of a booking.
const (
	CostFix      CostNature = "fix"
	CostVariable CostNature = "variable"
)

// Valid reports whether c is a known cost nature.
func (c CostNature) Valid() bool {
	return c == CostFix || c == CostVariable
}

// BudgetClass is the 50/30/20 classification of a booking.
type BudgetClass string

// Budget classes of the 50/30/20 rule.
const (
	ClassNeed   BudgetClass = "need"
	ClassWant   BudgetClass = "want"
	ClassSaving BudgetClass = "saving"
)

// Valid reports whether b is a known budget class.
func (b BudgetClass) Valid() bool {
	switch b {
	case ClassNeed, ClassWant, ClassSaving:
		return true
	default:
		return false
	}
}

// TargetPercent is the share of net income the 50/30/20 rule allots to this
// class. The rule is named after these three numbers, so they live here rather
// than being written out wherever a target is shown.
func (b BudgetClass) TargetPercent() int {
	switch b {
	case ClassNeed:
		return 50
	case ClassWant:
		return 30
	default:
		return 20
	}
}

// SplitMode describes how a booking is split between members.
type SplitMode string

// Split modes of a booking.
const (
	SplitEqual   SplitMode = "equal"
	SplitPercent SplitMode = "percent"
	SplitFixed   SplitMode = "fixed"
)

// Valid reports whether m is a known split mode.
func (m SplitMode) Valid() bool {
	switch m {
	case SplitEqual, SplitPercent, SplitFixed:
		return true
	default:
		return false
	}
}

// DuePoint says where inside the month a recurring booking falls. It is a
// coarse hint rather than a date, because a plan only needs to know whether the
// money is gone at the start of the month or still there until the end.
type DuePoint string

// Due points of a recurring booking.
const (
	DueStart  DuePoint = "start"
	DueMiddle DuePoint = "mid"
	DueEnd    DuePoint = "end"
)

// Valid reports whether p is a known due point.
func (p DuePoint) Valid() bool {
	switch p {
	case DueStart, DueMiddle, DueEnd:
		return true
	default:
		return false
	}
}

// Household is a single budget book. Exactly one household is active at a time.
type Household struct {
	ID        int64
	Name      string
	SortOrder int
	CreatedAt string
}

// Member is a person who can be charged for expenses within a household.
type Member struct {
	ID          int64
	HouseholdID int64
	Name        string
	Color       string
	SortOrder   int
}

// Category is the mandatory label of a booking (e.g. "Miete"). Its
// classification keeps income categories out of expense pickers.
type Category struct {
	ID             int64
	HouseholdID    int64
	Name           string
	Classification Direction
	Color          string
	// Icon is a key into the built-in symbol set, empty for the fallback.
	Icon      string
	SortOrder int
}

// Tag is a free, cross-cutting label. A booking can carry any number of them.
type Tag struct {
	ID          int64
	HouseholdID int64
	Name        string
	Color       string
}

// Booking is a single planned figure: either money coming in or going out,
// happening once or repeating. It replaces the former split between expenses
// and incomes so that a figure is maintained in exactly one place.
type Booking struct {
	ID          int64
	HouseholdID int64
	CategoryID  int64
	// PayerMemberID is who fronts the money, which is what makes a settlement
	// between the members computable at all.
	PayerMemberID *int64
	Direction     Direction
	Name          string
	Note          string
	AmountCents   int64
	Frequency     Frequency
	Interval      int
	DuePoint      DuePoint
	StartsOn      string // YYYY-MM-DD, the date itself when Frequency is once
	EndsOn        string // YYYY-MM-DD, empty means open ended
	CostNature    CostNature
	BudgetClass   BudgetClass
	SplitMode     SplitMode
	// Settle keeps the booking out of the settlement when false, for costs that
	// are shared on paper but never squared between the members.
	Settle    bool
	CreatedAt string
	UpdatedAt string
}

// BookingOverride replaces a recurring booking's amount for a while, e.g. an
// introductory price for the first six months.
type BookingOverride struct {
	ID          int64
	BookingID   int64
	StartsOn    string // YYYY-MM-DD, empty means open start
	EndsOn      string // YYYY-MM-DD, empty means open end
	AmountCents int64
	Note        string
}

// BookingSplit records a member's share of a booking. The meaning of Value
// depends on the booking's SplitMode: it is ignored for equal, a percentage
// (0-100) for percent, and cents for fixed.
type BookingSplit struct {
	BookingID int64
	MemberID  int64
	Value     float64
}
