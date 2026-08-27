package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
)

// FormatCents formats an integer amount of cents using German conventions
// (e.g. 123456 -> "1.234,56").
func FormatCents(c int64) string {
	neg := c < 0
	// Negating math.MinInt64 overflows back to itself, so clamp first.
	if c < -MaxAmountCents {
		c = -MaxAmountCents
	} else if c > MaxAmountCents {
		c = MaxAmountCents
	}
	if neg {
		c = -c
	}
	euros := c / 100
	cents := c % 100
	out := groupThousands(euros) + "," + fmt.Sprintf("%02d", cents)
	if neg {
		out = "-" + out
	}
	return out
}

// FormatEUR formats cents as a Euro amount (e.g. "1.234,56 €").
func FormatEUR(c int64) string {
	return FormatCents(c) + " €"
}

// FormatEURShort drops the cents, for captions where a figure has to stay
// short enough to be read at a glance.
func FormatEURShort(c int64) string {
	if c < -MaxAmountCents {
		c = -MaxAmountCents
	} else if c > MaxAmountCents {
		c = MaxAmountCents
	}
	euros := (c + 50) / 100
	if c < 0 {
		euros = (c - 50) / 100
	}
	if euros < 0 {
		return "-" + groupThousands(-euros) + " €"
	}
	return groupThousands(euros) + " €"
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if len(s) > pre {
			b.WriteString(".")
		}
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteString(".")
		}
	}
	return b.String()
}

// FormatPercent formats a percentage value with one decimal, German style.
func FormatPercent(p float64) string {
	s := strconv.FormatFloat(p, 'f', 1, 64)
	s = strings.Replace(s, ".", ",", 1)
	return s + " %"
}

// CurrentMonth returns the current month as "YYYY-MM" in local time.
func CurrentMonth() string {
	return time.Now().Format("2006-01")
}

// ShiftMonth returns ym shifted by delta months. Invalid input falls back to
// the current month.
func ShiftMonth(ym string, delta int) string {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		t = time.Now()
	}
	return t.AddDate(0, delta, 0).Format("2006-01")
}

// ValidMonth reports whether ym is a valid "YYYY-MM" string.
func ValidMonth(ym string) bool {
	_, err := time.Parse("2006-01", ym)
	return err == nil
}

// NormalizeMonth returns ym if valid, otherwise the current month.
func NormalizeMonth(ym string) string {
	if ValidMonth(ym) {
		return ym
	}
	return CurrentMonth()
}

// MonthLabel returns a human-readable label for a "YYYY-MM" string in the
// language of ctx (e.g. "Juli 2026").
func MonthLabel(ctx context.Context, ym string) string {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return ym
	}
	return fmt.Sprintf("%s %d", i18n.MonthName(i18n.LangFrom(ctx), int(t.Month())), t.Year())
}

// MonthShort returns a short label (e.g. "Jul 26").
func MonthShort(ctx context.Context, ym string) string {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return ym
	}
	return fmt.Sprintf("%s %02d", i18n.MonthAbbr(i18n.LangFrom(ctx), int(t.Month())), t.Year()%100)
}

// FormatDate renders a stored YYYY-MM-DD in German notation.
func FormatDate(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("02.01.2006")
}

// formatDecimal formats cents as a plain decimal (dot separator, no grouping),
// suitable for a number input value (e.g. 123456 -> "1234.56").
func formatDecimal(c int64) string {
	neg := c < 0
	if neg {
		c = -c
	}
	s := fmt.Sprintf("%d.%02d", c/100, c%100)
	if neg {
		s = "-" + s
	}
	return s
}

// MaxAmountCents bounds monetary input at 10 billion Euro. Anything beyond is
// a typo rather than a household figure, and the limit keeps the int64 totals
// computed in package calc far away from overflow.
const MaxAmountCents int64 = 1_000_000_000_000

// ErrAmountRange is returned for values outside ±MaxAmountCents.
var ErrAmountRange = errors.New("web: amount out of range")

// ParseCents parses a user-entered monetary string into cents. It accepts both
// German ("1.234,56") and plain ("1234.56") notations and an optional "€".
func ParseCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "€", "")
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, nil
	}
	switch {
	case strings.Contains(s, ",") && strings.Contains(s, "."):
		// Assume '.' thousands separator and ',' decimal separator.
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, ",", ".")
	case strings.Contains(s, ","):
		s = strings.ReplaceAll(s, ",", ".")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	// strconv.ParseFloat accepts "NaN" and "Inf"; converting either to int64 is
	// undefined and would saturate silently.
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, ErrAmountRange
	}
	cents := math.Round(f * 100)
	if cents > float64(MaxAmountCents) || cents < -float64(MaxAmountCents) {
		return 0, ErrAmountRange
	}
	return int64(cents), nil
}

// ParseFloatLoose parses a possibly German-formatted decimal (accepting ',' as
// the decimal separator) into a float64. NaN and infinities are rejected.
func ParseFloatLoose(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, ErrAmountRange
	}
	return f, nil
}
