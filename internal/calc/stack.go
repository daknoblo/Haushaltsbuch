package calc

import (
	"math"
	"sort"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Groupings of the stacked chart.
const (
	GroupCategory = "category"
	GroupClass    = "class"
)

// CleanGrouping falls back to the category view for anything unknown.
func CleanGrouping(key string) string {
	if key == GroupClass {
		return GroupClass
	}
	return GroupCategory
}

// StackSegment is one block of a month's column.
type StackSegment struct {
	Key      int64
	Label    string
	LabelKey string
	Color    string
	Cents    int64
	X        float64
	Y        float64
	Width    float64
	Height   float64
	// Labeled is false for a segment too thin to hold its own figure.
	Labeled bool
}

// StackColumn is one month of the stacked chart.
type StackColumn struct {
	Month      string
	TotalCents int64
	X          float64
	Width      float64
	Segments   []StackSegment
}

// StackKey is one entry of the legend.
type StackKey struct {
	Key      int64
	Label    string
	LabelKey string
	Color    string
}

// StackChart stacks a month's expenses into one column, so the months can be
// compared by height and read by color at the same time.
type StackChart struct {
	Grouping string
	Width    float64
	Height   float64
	Left     float64
	Right    float64
	Top      float64
	Bottom   float64
	Columns  []StackColumn
	Grid     []ChartGridLine
	Ticks    []ChartTick
	Keys     []StackKey
}

// Empty reports whether there is nothing to draw.
func (c StackChart) Empty() bool { return len(c.Columns) == 0 }

// BuildStackChart lays out the expenses of every month as one stacked column,
// grouped either by category or by the 50/30/20 class.
func BuildStackChart(d Data, months []string, member int64, grouping string, width, height float64) StackChart {
	const (
		gutterLeft   = 62.0
		gutterBottom = 24.0
		padTop       = 10.0
		padRight     = 8.0
		gridLines    = 4
		// A segment shorter than this cannot hold a legible figure.
		labelFloor = 15.0
	)
	c := StackChart{
		Grouping: CleanGrouping(grouping),
		Width:    width,
		Height:   height,
		Left:     gutterLeft,
		Right:    width - padRight,
		Top:      padTop,
		Bottom:   height - gutterBottom,
	}
	if len(months) == 0 || c.Right <= c.Left || c.Bottom <= c.Top {
		return c
	}

	keys, cents := stackTotals(d, months, member, c.Grouping)
	if len(keys) == 0 {
		return c
	}
	c.Keys = keys

	var peak int64
	for i := range months {
		var sum int64
		for _, k := range keys {
			sum += cents[k.Key][i]
		}
		peak = max(peak, sum)
	}
	top, step := niceScale(peak, gridLines)
	if top <= 0 {
		return c
	}

	scale := (c.Bottom - c.Top) / float64(top)
	for v := int64(0); v <= top; v += step {
		c.Grid = append(c.Grid, ChartGridLine{Y: c.Bottom - float64(v)*scale, Cents: v})
	}

	slot := (c.Right - c.Left) / float64(len(months))
	colW := math.Min(slot*0.62, 46)
	for i, month := range months {
		center := c.Left + slot*(float64(i)+0.5)
		c.Ticks = append(c.Ticks, ChartTick{Month: month, X: center})
		col := StackColumn{Month: month, X: center - colW/2, Width: colW}
		y := c.Bottom
		for _, k := range keys {
			v := cents[k.Key][i]
			if v <= 0 {
				continue
			}
			h := float64(v) * scale
			y -= h
			col.Segments = append(col.Segments, StackSegment{
				Key: k.Key, Label: k.Label, LabelKey: k.LabelKey, Color: k.Color,
				Cents: v, X: col.X, Y: y, Width: colW, Height: h,
				Labeled: h >= labelFloor,
			})
			col.TotalCents += v
		}
		c.Columns = append(c.Columns, col)
	}
	return c
}

// stackTotals collects the expenses per group and month, largest group first so
// the heavy blocks sit at the bottom of every column.
func stackTotals(d Data, months []string, member int64, grouping string) ([]StackKey, map[int64][]int64) {
	n := len(months)
	cents := make(map[int64][]int64)
	meta := make(map[int64]StackKey)

	cats := make(map[int64]store.Category, len(d.Categories))
	for _, c := range d.Categories {
		cats[c.ID] = c
	}
	classOrder := map[store.BudgetClass]int64{store.ClassNeed: 0, store.ClassWant: 1, store.ClassSaving: 2}
	classColor := map[store.BudgetClass]string{
		store.ClassNeed: "#6366f1", store.ClassWant: "#f59e0b", store.ClassSaving: "#0ea5e9",
	}

	for i, month := range months {
		for _, b := range d.Bookings {
			if b.Direction != store.DirExpense || !ActiveIn(b, month) {
				continue
			}
			amount := float64(AmountFor(b, d.Overrides[b.ID], month)) * monthlyFactor(b)
			if member != Everyone {
				shares, _ := allocate(amount, b, d.Splits[b.ID])
				share, ok := shares[member]
				if !ok {
					continue
				}
				amount = share
			}
			v := round(amount)
			if v == 0 {
				continue
			}

			key := b.CategoryID
			entry := StackKey{Key: key, Label: cats[key].Name, Color: cats[key].Color}
			if grouping == GroupClass {
				key = classOrder[b.BudgetClass]
				entry = StackKey{Key: key, LabelKey: "class." + string(b.BudgetClass), Color: classColor[b.BudgetClass]}
			}
			meta[key] = entry
			addAt(cents, key, i, n, v)
		}
	}

	keys := make([]StackKey, 0, len(meta))
	for _, k := range meta {
		keys = append(keys, k)
	}
	if grouping == GroupClass {
		sort.Slice(keys, func(i, j int) bool { return keys[i].Key < keys[j].Key })
		return keys, cents
	}
	// The keys come out of a map, so equal columns need a tie-break of their own
	// or the legend reorders itself between reloads.
	sort.Slice(keys, func(i, j int) bool {
		a, b := sumOf(cents[keys[i].Key]), sumOf(cents[keys[j].Key])
		if a != b {
			return a > b
		}
		if keys[i].Label != keys[j].Label {
			return keys[i].Label < keys[j].Label
		}
		return keys[i].Key < keys[j].Key
	})
	return keys, cents
}

func sumOf(v []int64) int64 {
	var out int64
	for _, x := range v {
		out += x
	}
	return out
}
