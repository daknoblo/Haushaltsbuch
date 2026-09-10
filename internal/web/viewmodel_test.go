package web

import (
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// The arrow says which way a figure moved, the color whether that was welcome.
// They disagree on exactly the rows where more money is the good news.
func TestTrendToneFollowsTheVerdictNotTheDirection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		trend calc.MatrixTrend
		gain  bool
		want  string
	}{
		{"rent went up", calc.TrendUp, false, "trend-bad"},
		{"rent came down", calc.TrendDown, false, "trend-good"},
		{"salary went up", calc.TrendUp, true, "trend-good"},
		{"salary came down", calc.TrendDown, true, "trend-bad"},
	}
	for _, c := range cases {
		if got := TrendTone(c.trend, c.gain); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSplitLabelsExplainUnequalAllocations(t *testing.T) {
	line := calc.ShareLine{
		Booking:  store.Booking{SplitMode: store.SplitPercent},
		Carriers: 2,
		Splits: []store.BookingSplit{
			{MemberID: 1, Value: 60},
			{MemberID: 2, Value: 40},
		},
	}
	if got := SplitLabel(t.Context(), line); got != T(t.Context(), "split.percent") {
		t.Errorf("percentage split label = %q", got)
	}
	if got := ShareAllocation(line, 1); got != "60 %" {
		t.Errorf("first allocation = %q", got)
	}
	if got := ShareAllocation(line, 2); got != "40 %" {
		t.Errorf("second allocation = %q", got)
	}
	line.Splits[0].Value = 33.33
	if got := ShareAllocation(line, 1); got != "33,33 %" {
		t.Errorf("percentage precision lost: %q", got)
	}
	line.Booking.SplitMode = store.SplitFixed
	if got := SplitLabel(t.Context(), line); got != T(t.Context(), "split.fixed") {
		t.Errorf("fixed split label = %q", got)
	}
	if got := ShareAllocation(line, 1); got != "" {
		t.Errorf("fixed amount incorrectly shown as percentage: %q", got)
	}
	line.Booking.SplitMode = store.SplitEqual
	if got := SplitLabel(t.Context(), line); got != "÷ 2" {
		t.Errorf("equal split label = %q", got)
	}
}

func TestMatrixDisplaysActiveZerosInPageAndPDF(t *testing.T) {
	if got := MatrixCell(0, true); got != "0 €" {
		t.Errorf("active zero = %q", got)
	}
	if got := MatrixCell(0, false); got != "" {
		t.Errorf("inactive month = %q", got)
	}
	row := calc.MatrixRow{Cents: []int64{0, 0}, Active: []bool{false, true}, ActiveMonths: 1}
	cells := pdfCells(row)
	if cells[0] != "" || cells[1] != "0 €" {
		t.Errorf("PDF does not distinguish activity: %v", cells)
	}
}
