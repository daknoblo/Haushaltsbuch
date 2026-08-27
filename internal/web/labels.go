package web

import (
	"context"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
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

// MatrixBandLabel names one band of the year matrix. The keys are spelled out
// rather than assembled, so the catalog guard can see them.
func MatrixBandLabel(ctx context.Context, key string) string {
	switch key {
	case calc.BandIncome:
		return T(ctx, "matrix.band.income")
	case calc.BandFixed:
		return T(ctx, "matrix.band.fixed")
	default:
		return T(ctx, "matrix.band.variable")
	}
}

// MatrixShareLabel names what a percentage row is a share of. Every one of them
// is a share of income now, so the band no longer enters into it.
func MatrixShareLabel(ctx context.Context) string {
	return T(ctx, "matrix.shareOf.income")
}

// BudgetClassDot is the color a class carries in the 50/30/20 bar, so the same
// class is the same color wherever it shows up.
func BudgetClassDot(c store.BudgetClass) string {
	switch c {
	case store.ClassWant:
		return "bg-amber-500"
	case store.ClassSaving:
		return "bg-sky-500"
	default:
		return "bg-indigo-500"
	}
}

// RuleBucketHint is the sentence that says what belongs in a pail of the rule.
func RuleBucketHint(ctx context.Context, c store.BudgetClass) string {
	switch c {
	case store.ClassWant:
		return T(ctx, "rule.wantHint")
	case store.ClassSaving:
		return T(ctx, "rule.savingHint")
	default:
		return T(ctx, "rule.needHint")
	}
}

// RuleGapLabel is how far a pail is from its target.
func RuleGapLabel(ctx context.Context, rep calc.MonthReport, class store.BudgetClass) string {
	switch gap := rep.ByBudgetClass[class] - rep.TargetCents(class); {
	case gap > 0:
		return Tf(ctx, "rule.over", FormatEUR(gap))
	case gap < 0:
		return Tf(ctx, "rule.under", FormatEUR(-gap))
	default:
		return T(ctx, "rule.exact")
	}
}

// RuleArcTitle names an arc of the ring. The leftover has no class, because it
// is not a fourth pail but income no bucket consumed.
func RuleArcTitle(ctx context.Context, a calc.RuleArc) string {
	if a.Class == "" {
		return T(ctx, "dash.surplus") + ": " + FormatEUR(a.Cents)
	}
	return BudgetClassLabel(ctx, a.Class) + ": " + FormatEUR(a.Cents)
}

// UsageLabel is how many bookings point at a category. The singular needs its
// own wording, and now that the count sits on every row "1 Buchungen" would be
// on the screen more often than not.
func UsageLabel(ctx context.Context, n int) string {
	if n == 1 {
		return T(ctx, "settings.usedByOne")
	}
	return Tf(ctx, "settings.usedBy", n)
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
