package calc

import "math"

// ChartBar is one laid-out bar of the trend chart.
type ChartBar struct {
	Month  string
	Income bool
	Cents  int64
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// ChartGridLine is a horizontal rule with the amount it stands for.
type ChartGridLine struct {
	Y     float64
	Cents int64
}

// ChartTick labels one month below the plot.
type ChartTick struct {
	Month string
	X     float64
}

// TrendChart is an income/expense bar chart laid out in user space. Values stay
// raw so the view formats them in the request language.
type TrendChart struct {
	Width  float64
	Height float64
	Left   float64
	Right  float64
	Top    float64
	Bottom float64
	Bars   []ChartBar
	Grid   []ChartGridLine
	Ticks  []ChartTick
}

// Empty reports whether there is nothing to draw.
func (c TrendChart) Empty() bool { return len(c.Bars) == 0 }

// BuildTrendChart lays out income and expenses per month as paired bars. The
// maths lives here rather than in a charting library, which would need
// 'unsafe-eval' the CSP does not grant.
func BuildTrendChart(reps []MonthReport, width, height float64) TrendChart {
	const (
		gutterLeft   = 62.0
		gutterBottom = 24.0
		padTop       = 10.0
		padRight     = 8.0
		gridLines    = 4
	)
	c := TrendChart{
		Width:  width,
		Height: height,
		Left:   gutterLeft,
		Right:  width - padRight,
		Top:    padTop,
		Bottom: height - gutterBottom,
	}
	if len(reps) == 0 || c.Right <= c.Left || c.Bottom <= c.Top {
		return c
	}

	var peak int64
	for _, r := range reps {
		peak = max(peak, r.IncomeCents, r.ExpenseCents)
	}
	top, step := niceScale(peak, gridLines)
	if top <= 0 {
		return c
	}

	plotH := c.Bottom - c.Top
	scale := plotH / float64(top)
	for v := int64(0); v <= top; v += step {
		c.Grid = append(c.Grid, ChartGridLine{Y: c.Bottom - float64(v)*scale, Cents: v})
	}

	// A slot holds both bars of a month plus the gap to the next one.
	slot := (c.Right - c.Left) / float64(len(reps))
	barW := math.Min(slot*0.34, 26)
	for i, r := range reps {
		center := c.Left + slot*(float64(i)+0.5)
		c.Ticks = append(c.Ticks, ChartTick{Month: r.Month, X: center})
		for _, bar := range []struct {
			income bool
			cents  int64
			x      float64
		}{
			{true, r.IncomeCents, center - barW - 1},
			{false, r.ExpenseCents, center + 1},
		} {
			h := math.Max(float64(bar.cents)*scale, 0)
			c.Bars = append(c.Bars, ChartBar{
				Month:  r.Month,
				Income: bar.income,
				Cents:  bar.cents,
				X:      bar.x,
				Y:      c.Bottom - h,
				Width:  barW,
				Height: h,
			})
		}
	}
	return c
}

// niceScale rounds the axis up to a value that divides into readable steps, so
// the grid reads 1.000 / 2.000 / 3.000 rather than 1.237 / 2.474.
func niceScale(peak int64, lines int) (top, step int64) {
	if peak <= 0 || lines <= 0 {
		return 0, 0
	}
	raw := float64(peak) / float64(lines)
	magnitude := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= magnitude*m {
			raw = magnitude * m
			break
		}
	}
	step = max(int64(raw), 1)
	top = ((peak + step - 1) / step) * step
	return top, step
}
