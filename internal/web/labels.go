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

// FrequencyLabel returns the localized label for a frequency.
func FrequencyLabel(ctx context.Context, f store.Frequency) string {
	switch f {
	case store.FreqWeekly:
		return T(ctx, "freq.weekly")
	case store.FreqYearly:
		return T(ctx, "freq.yearly")
	default:
		return T(ctx, "freq.monthly")
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
		{string(store.FreqWeekly), T(ctx, "freq.weekly")},
		{string(store.FreqYearly), T(ctx, "freq.yearly")},
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

// RhythmLabel describes how often an expense occurs, for the collapsed row.
func RhythmLabel(ctx context.Context, e store.Expense) string {
	if e.IsOneOff {
		return T(ctx, "freq.oneOff")
	}
	return FrequencyLabel(ctx, e.Frequency)
}

// PageTitle returns the localized title of the active page.
func PageTitle(ctx context.Context, active string) string {
	switch active {
	case "expenses":
		return T(ctx, "nav.expenses")
	case "income":
		return T(ctx, "nav.income")
	case "statistics":
		return T(ctx, "nav.statistics")
	case "settings":
		return T(ctx, "nav.settings")
	default:
		return T(ctx, "nav.overview")
	}
}
