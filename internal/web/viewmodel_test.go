package web

import (
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
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
