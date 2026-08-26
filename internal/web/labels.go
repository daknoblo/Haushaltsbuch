package web

import (
	"context"

	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// T translates a key using the language stored in the request context. templ
// hands ctx to every component, so views call this directly.
func T(ctx context.Context, key string) string {
	return i18n.C(ctx, i18n.Key(key))
}

// Tf translates a format key and fills in the arguments.
func Tf(ctx context.Context, key string, args ...any) string {
	return i18n.Cf(ctx, i18n.Key(key), args...)
}

// LangCode returns the BCP 47 tag of the request language for the lang
// attribute of the document.
func LangCode(ctx context.Context) string {
	return string(i18n.LangFrom(ctx))
}

// FrequencyLabel returns the localized label for a frequency.
func FrequencyLabel(ctx context.Context, f store.Frequency) string {
	switch f {
	case store.FreqOnce:
		return T(ctx, "freq.oneOff")
	case store.FreqWeekly:
		return T(ctx, "freq.weekly")
	case store.FreqQuarterly:
		return T(ctx, "freq.quarterly")
	case store.FreqYearly:
		return T(ctx, "freq.yearly")
	default:
		return T(ctx, "freq.monthly")
	}
}

// DirectionLabel returns the localized label for a booking direction.
func DirectionLabel(ctx context.Context, d store.Direction) string {
	if d == store.DirIncome {
		return T(ctx, "dir.income")
	}
	return T(ctx, "dir.expense")
}

// DirectionOptions returns the selectable booking directions.
func DirectionOptions(ctx context.Context) []Option {
	return []Option{
		{string(store.DirExpense), T(ctx, "dir.expense")},
		{string(store.DirIncome), T(ctx, "dir.income")},
	}
}

// CostNatureLabel returns the localized label for a cost nature.
func CostNatureLabel(ctx context.Context, c store.CostNature) string {
	if c == store.CostVariable {
		return T(ctx, "cost.variable")
	}
	return T(ctx, "cost.fix")
}

// BudgetClassLabel returns the localized label for a budget class.
func BudgetClassLabel(ctx context.Context, b store.BudgetClass) string {
	switch b {
	case store.ClassWant:
		return T(ctx, "class.want")
	case store.ClassSaving:
		return T(ctx, "class.saving")
	default:
		return T(ctx, "class.need")
	}
}

// SplitModeLabel returns the localized label for a split mode.
func SplitModeLabel(ctx context.Context, m store.SplitMode) string {
	switch m {
	case store.SplitPercent:
		return T(ctx, "split.percent")
	case store.SplitFixed:
		return T(ctx, "split.fixed")
	default:
		return T(ctx, "split.equal")
	}
}

// FrequencyOptions returns the selectable frequencies.
func FrequencyOptions(ctx context.Context) []Option {
	return []Option{
		{string(store.FreqMonthly), T(ctx, "freq.monthly")},
		{string(store.FreqOnce), T(ctx, "freq.oneOff")},
		{string(store.FreqWeekly), T(ctx, "freq.weekly")},
		{string(store.FreqQuarterly), T(ctx, "freq.quarterly")},
		{string(store.FreqYearly), T(ctx, "freq.yearly")},
	}
}

// RecurringOptions returns the frequencies of a repeating booking. "Once" is
// missing on purpose: the dialog offers a switch for that instead.
func RecurringOptions(ctx context.Context) []Option {
	return []Option{
		{string(store.FreqMonthly), T(ctx, "freq.monthly")},
		{string(store.FreqWeekly), T(ctx, "freq.weekly")},
		{string(store.FreqQuarterly), T(ctx, "freq.quarterly")},
		{string(store.FreqYearly), T(ctx, "freq.yearly")},
	}
}

// DuePointOptions returns where inside the month a booking falls.
func DuePointOptions(ctx context.Context) []Option {
	return []Option{
		{string(store.DueStart), T(ctx, "bookings.dueStart")},
		{string(store.DueMiddle), T(ctx, "bookings.dueMid")},
		{string(store.DueEnd), T(ctx, "bookings.dueEnd")},
	}
}

// DuePointLabel names where inside the month a booking falls.
func DuePointLabel(ctx context.Context, p store.DuePoint) string {
	switch p {
	case store.DueMiddle:
		return T(ctx, "bookings.dueMid")
	case store.DueEnd:
		return T(ctx, "bookings.dueEnd")
	default:
		return T(ctx, "bookings.dueStart")
	}
}

// CostNatureOptions returns the selectable cost natures.
func CostNatureOptions(ctx context.Context) []Option {
	return []Option{
		{string(store.CostFix), T(ctx, "cost.fix")},
		{string(store.CostVariable), T(ctx, "cost.variable")},
	}
}

// BudgetClassOptions returns the selectable budget classes.
func BudgetClassOptions(ctx context.Context) []Option {
	return []Option{
		{string(store.ClassNeed), T(ctx, "class.need")},
		{string(store.ClassWant), T(ctx, "class.want")},
		{string(store.ClassSaving), T(ctx, "class.saving")},
	}
}

// SplitModeOptions returns the selectable split modes.
func SplitModeOptions(ctx context.Context) []Option {
	return []Option{
		{string(store.SplitEqual), T(ctx, "split.equal")},
		{string(store.SplitPercent), T(ctx, "split.percent")},
		{string(store.SplitFixed), T(ctx, "split.fixed")},
	}
}

// RhythmLabel describes how often a booking occurs, for the collapsed row.
func RhythmLabel(ctx context.Context, b store.Booking) string {
	if !b.Frequency.Recurring() {
		return T(ctx, "freq.oneOff")
	}
	if b.Interval > 1 {
		return Tf(ctx, "freq.everyN", b.Interval, FrequencyLabel(ctx, b.Frequency))
	}
	return FrequencyLabel(ctx, b.Frequency)
}

// MemberRoleTitle says what a name on a booking row means.
func MemberRoleTitle(ctx context.Context, m store.Member, pays bool) string {
	if pays {
		return Tf(ctx, "bookings.paysTitle", m.Name)
	}
	return Tf(ctx, "bookings.carriesTitle", m.Name)
}

// PageTitle returns the localized title of the active page.
func PageTitle(ctx context.Context, active string) string {
	switch active {
	case "bookings":
		return T(ctx, "nav.bookings")
	case "dashboard":
		return T(ctx, "nav.dashboard")
	case "settings":
		return T(ctx, "nav.settings")
	default:
		return T(ctx, "nav.overview")
	}
}
