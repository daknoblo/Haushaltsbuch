package store

// Frequency describes how often a recurring expense occurs.
type Frequency string

const (
	FreqWeekly  Frequency = "weekly"
	FreqMonthly Frequency = "monthly"
	FreqYearly  Frequency = "yearly"
)

// Valid reports whether f is a known frequency.
func (f Frequency) Valid() bool {
	switch f {
	case FreqWeekly, FreqMonthly, FreqYearly:
		return true
	default:
		return false
	}
}

// MonthlyFactor returns the factor to normalise an amount of this frequency to
// a monthly-equivalent value.
func (f Frequency) MonthlyFactor() float64 {
	switch f {
	case FreqWeekly:
		return 52.0 / 12.0
	case FreqYearly:
		return 1.0 / 12.0
	default: // monthly
		return 1.0
	}
}

// CostNature classifies an expense as fixed or variable.
type CostNature string

const (
	CostFix      CostNature = "fix"
	CostVariable CostNature = "variable"
)

// Valid reports whether c is a known cost nature.
func (c CostNature) Valid() bool {
	return c == CostFix || c == CostVariable
}

// BudgetClass is the 50/30/20 classification of an expense.
type BudgetClass string

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

// SplitMode describes how an expense is split between members.
type SplitMode string

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

// Section groups expenses for a clearer overview.
type Section struct {
	ID          int64
	HouseholdID int64
	Name        string
	SortOrder   int
}

// Category is a free, managed label for an expense (e.g. "Miete").
type Category struct {
	ID          int64
	HouseholdID int64
	Name        string
}

// Expense is a recurring or one-off cost.
type Expense struct {
	ID          int64
	HouseholdID int64
	SectionID   *int64
	CategoryID  *int64
	Name        string
	AmountCents int64
	Frequency   Frequency
	CostNature  CostNature
	BudgetClass BudgetClass
	IsOneOff    bool
	OccurredOn  string // YYYY-MM-DD (one-off only)
	ActiveFrom  string // YYYY-MM (recurring)
	ActiveUntil string // YYYY-MM (recurring, optional)
	SplitMode   SplitMode
	SortOrder   int
	CreatedAt   string
	UpdatedAt   string
}

// ExpenseSplit records a member's participation/share in an expense. The
// meaning of Value depends on the expense's SplitMode: it is ignored for
// equal, a percentage (0-100) for percent, and cents for fixed.
type ExpenseSplit struct {
	ID        int64
	ExpenseID int64
	MemberID  int64
	Value     float64
}

// Income is a manually entered income line for a member in a given month.
type Income struct {
	ID          int64
	HouseholdID int64
	MemberID    int64
	YearMonth   string // YYYY-MM
	Name        string
	AmountCents int64
	SortOrder   int
}
