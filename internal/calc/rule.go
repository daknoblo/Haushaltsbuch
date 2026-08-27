package calc

import (
	"fmt"
	"math"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// RuleArc is one segment of the 50/30/20 ring.
type RuleArc struct {
	// Class is empty on the arc that stands for income nobody has claimed yet.
	Class store.BudgetClass
	Cents int64
	Path  string
}

// RuleMark is where the rule says a segment ought to end.
type RuleMark struct {
	Percent int
	Path    string
	// LabelX and LabelY sit outside the ring, on the same spoke as the mark.
	LabelX float64
	LabelY float64
}

// RuleRing is the 50/30/20 rule drawn against what the household actually did:
// the arcs are the real split of income, the marks are where the rule wanted
// the boundaries. The gap between an arc's end and its mark is the whole
// message, so both are laid out in the same coordinates.
type RuleRing struct {
	Size  float64
	Arcs  []RuleArc
	Marks []RuleMark
	// Surplus is income no bucket consumed. It is drawn, because a rule about
	// dividing income says nothing about the part that was never divided.
	Surplus int64
}

// Empty reports whether there is nothing to draw.
func (r RuleRing) Empty() bool { return len(r.Arcs) == 0 }

const (
	ruleSize  = 280.0
	ruleOuter = 108.0
	ruleInner = 74.0
	ruleGap   = 1.6 // degrees kept clear between arcs
)

// ruleOrder is the order of the buckets around the ring, which follows the rule
// itself so the picture reads 50, then 30, then 20.
var ruleOrder = []store.BudgetClass{store.ClassNeed, store.ClassWant, store.ClassSaving}

// BuildRuleRing lays out the 50/30/20 ring for a month.
//
// The circle holds whichever is larger, income or what the buckets claim. A
// ring that always stood for income would have to cut the last bucket off the
// end once spending ran past it, and a saving rate that the panel beside it
// still reported would simply not be in the picture.
func BuildRuleRing(rep MonthReport) RuleRing {
	if rep.IncomeCents <= 0 {
		return RuleRing{}
	}

	var assigned int64
	for _, c := range ruleOrder {
		if v := rep.ByBudgetClass[c]; v > 0 {
			assigned += v
		}
	}
	total := max(rep.IncomeCents, assigned)

	ring := RuleRing{Size: ruleSize}
	at := 0.0
	for _, class := range ruleOrder {
		cents := rep.ByBudgetClass[class]
		if cents <= 0 {
			continue
		}
		sweep := 360 * float64(cents) / float64(total)
		ring.Arcs = append(ring.Arcs, RuleArc{
			Class: class,
			Cents: cents,
			Path:  ringPath(at, at+sweep),
		})
		at += sweep
	}

	if rep.BalanceCents > 0 && 360-at > 0.5 {
		ring.Surplus = rep.BalanceCents
		ring.Arcs = append(ring.Arcs, RuleArc{Cents: rep.BalanceCents, Path: ringPath(at, 360)})
	}

	// The marks are shares of income, not of the circle. Nailed to the circle
	// they would slide outwards along with the overspending, and the rule would
	// obligingly agree with whatever was spent.
	scale := float64(rep.IncomeCents) / float64(total)
	for _, p := range []int{50, 80} {
		ring.Marks = append(ring.Marks, ruleMark(p, 3.6*float64(p)*scale))
	}
	return ring
}

// ringPath is the outline of one arc of the ring, walking the outer edge
// clockwise and the inner edge back.
func ringPath(from, to float64) string {
	if to-from > ruleGap*2 {
		from += ruleGap / 2
		to -= ruleGap / 2
	}
	ox1, oy1 := ringPoint(from, ruleOuter)
	ox2, oy2 := ringPoint(to, ruleOuter)
	ix1, iy1 := ringPoint(to, ruleInner)
	ix2, iy2 := ringPoint(from, ruleInner)

	long := 0
	if to-from > 180 {
		long = 1
	}
	return fmt.Sprintf("M %.2f %.2f A %.2f %.2f 0 %d 1 %.2f %.2f L %.2f %.2f A %.2f %.2f 0 %d 0 %.2f %.2f Z",
		ox1, oy1, ruleOuter, ruleOuter, long, ox2, oy2,
		ix1, iy1, ruleInner, ruleInner, long, ix2, iy2)
}

// ruleMark is a short spoke across the ring at the angle a target sits at.
func ruleMark(percent int, deg float64) RuleMark {
	x1, y1 := ringPoint(deg, ruleInner-5)
	x2, y2 := ringPoint(deg, ruleOuter+5)
	lx, ly := ringPoint(deg, ruleOuter+17)
	return RuleMark{
		Percent: percent,
		Path:    fmt.Sprintf("M %.2f %.2f L %.2f %.2f", x1, y1, x2, y2),
		LabelX:  lx,
		LabelY:  ly,
	}
}

// ringPoint is a point on the ring, measured clockwise from the top so the
// first bucket starts where a reader starts.
func ringPoint(deg, radius float64) (x, y float64) {
	rad := (deg - 90) * math.Pi / 180
	c := ruleSize / 2
	return c + radius*math.Cos(rad), c + radius*math.Sin(rad)
}
