package web

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var deMonths = []string{
	"Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

var deMonthsShort = []string{
	"Jan", "Feb", "Mär", "Apr", "Mai", "Jun",
	"Jul", "Aug", "Sep", "Okt", "Nov", "Dez",
}

// FormatCents formats an integer amount of cents using German conventions
// (e.g. 123456 -> "1.234,56").
func FormatCents(c int64) string {
	neg := c < 0
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

// FormatEURf formats a float Euro value (already in Euro, not cents).
func FormatEURf(euro float64) string {
	return FormatCents(int64(euro*100 + sign(euro)*0.5))
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
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

// MonthLabel returns a human-readable German label for a "YYYY-MM" string
// (e.g. "Juli 2026").
func MonthLabel(ym string) string {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return ym
	}
	return fmt.Sprintf("%s %d", deMonths[int(t.Month())-1], t.Year())
}

// MonthShort returns a short label (e.g. "Jul 26").
func MonthShort(ym string) string {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return ym
	}
	return fmt.Sprintf("%s %02d", deMonthsShort[int(t.Month())-1], t.Year()%100)
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
	return int64(math.Round(f * 100)), nil
}

// ParseFloatLoose parses a possibly German-formatted decimal (accepting ',' as
// the decimal separator) into a float64.
func ParseFloatLoose(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}
