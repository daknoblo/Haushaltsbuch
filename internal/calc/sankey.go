package calc

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Sankey layer indices. Income sources feed one trunk, the trunk splits into
// the three budget classes plus whatever is left over, and each class fans out
// into its categories.
const (
	layerIncome = 0
	layerTrunk  = 1
	layerClass  = 2
	layerLeaf   = 3
)

// sankeySmallShare is the fraction of total income below which a category is
// merged into a collected "Sonstige" node, so a household with forty
// categories still produces a readable diagram.
const sankeySmallShare = 0.02

// SankeyNode is one box of the diagram, already laid out in user space.
type SankeyNode struct {
	ID     string
	Label  string
	Color  string
	Cents  int64
	Layer  int
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// LabelX is where the caption of a node starts.
func (n SankeyNode) LabelX() float64 { return n.X + n.Width + 6 }

// LabelY centers the caption on the node.
func (n SankeyNode) LabelY() float64 { return n.Y + n.Height/2 }

// SankeyLink is one ribbon between two nodes, rendered as a single SVG path.
type SankeyLink struct {
	Source string
	Target string
	Label  string
	Cents  int64
	Color  string
	Path   string
}

// Sankey is a complete, laid-out diagram ready to be emitted as SVG.
type Sankey struct {
	Width  float64
	Height float64
	Nodes  []SankeyNode
	Links  []SankeyLink
	// Deficit is set when the plan spends more than it earns. The diagram then
	// contains an explicit withdrawal node, because a Sankey cannot show a
	// negative flow.
	Deficit bool
}

// Empty reports whether there is nothing to draw.
func (s Sankey) Empty() bool { return len(s.Nodes) == 0 }

type sankeyBuilder struct {
	nodes []SankeyNode
	index map[string]int
	links []SankeyLink
	// A node passes its amount through, so its value is the larger of what
	// flows in and what flows out - never the sum of both.
	inflow  map[string]int64
	outflow map[string]int64
}

func (b *sankeyBuilder) node(id, label, color string, layer int) {
	if b.index == nil {
		b.index = make(map[string]int)
		b.inflow = make(map[string]int64)
		b.outflow = make(map[string]int64)
	}
	if _, ok := b.index[id]; ok {
		return
	}
	b.index[id] = len(b.nodes)
	b.nodes = append(b.nodes, SankeyNode{ID: id, Label: label, Color: color, Layer: layer})
}

func (b *sankeyBuilder) link(source, target string, cents int64, color string) {
	if cents <= 0 {
		return
	}
	si, ok := b.index[source]
	if !ok {
		return
	}
	ti, ok := b.index[target]
	if !ok {
		return
	}
	b.outflow[source] += cents
	b.inflow[target] += cents
	b.links = append(b.links, SankeyLink{
		Source: source,
		Target: target,
		Label:  b.nodes[si].Label + " → " + b.nodes[ti].Label,
		Cents:  cents,
		Color:  color,
	})
}

// finish gives every node the amount that actually passes through it.
func (b *sankeyBuilder) finish() {
	for i := range b.nodes {
		id := b.nodes[i].ID
		b.nodes[i].Cents = max64(b.inflow[id], b.outflow[id])
	}
}

// budget class presentation, in the order they should stack.
var sankeyClasses = []struct {
	class store.BudgetClass
	label i18n.Key
	color string
}{
	{store.ClassNeed, "class.need", "#6366f1"},
	{store.ClassWant, "class.want", "#f59e0b"},
	{store.ClassSaving, "class.saving", "#0ea5e9"},
}

// BuildSankey turns a report into a laid-out flow diagram: income sources on
// the left, the three budget classes in the middle and the categories on the
// right, with anything unspent ending in an explicit surplus node so that every
// euro is accounted for. months is the range rep was built from.
func BuildSankey(ctx context.Context, d Data, rep MonthReport, months []string, width, height float64) Sankey {
	if rep.IncomeCents <= 0 && rep.ExpenseCents <= 0 {
		return Sankey{}
	}
	var b sankeyBuilder
	b.node("trunk", i18n.C(ctx, "overview.income"), "#10b981", layerTrunk)

	for _, in := range rep.IncomeCategories {
		id := fmt.Sprintf("in-%d", in.Key)
		b.node(id, in.Label, colorOr(in.Color, "#34d399"), layerIncome)
		b.link(id, "trunk", in.Cents, colorOr(in.Color, "#34d399"))
	}

	// A plan that overspends has to draw the difference from somewhere, and a
	// ribbon cannot be negative, so the shortfall enters as its own source.
	deficit := rep.ExpenseCents > rep.IncomeCents
	if deficit {
		b.node("withdrawal", i18n.C(ctx, "sankey.withdrawal"), "#f43f5e", layerIncome)
		b.link("withdrawal", "trunk", rep.ExpenseCents-rep.IncomeCents, "#f43f5e")
	}

	// Categories are grouped under the budget class their bookings carry.
	perClass := classCategoryTotals(ctx, d, months, rep.Member)
	threshold := int64(float64(max64(rep.IncomeCents, rep.ExpenseCents)) * sankeySmallShare)

	for _, c := range sankeyClasses {
		total := rep.ByBudgetClass[c.class]
		if total <= 0 {
			continue
		}
		classID := "class-" + string(c.class)
		b.node(classID, i18n.C(ctx, c.label), c.color, layerClass)
		b.link("trunk", classID, total, c.color)

		var rest int64
		for _, leaf := range perClass[c.class] {
			if leaf.Cents < threshold {
				rest += leaf.Cents
				continue
			}
			id := fmt.Sprintf("leaf-%s-%d", c.class, leaf.Key)
			b.node(id, leaf.Label, colorOr(leaf.Color, c.color), layerLeaf)
			b.link(classID, id, leaf.Cents, colorOr(leaf.Color, c.color))
		}
		if rest > 0 {
			id := "leaf-" + string(c.class) + "-rest"
			b.node(id, i18n.C(ctx, "sankey.other"), "#94a3b8", layerLeaf)
			b.link(classID, id, rest, "#94a3b8")
		}
	}

	if !deficit && rep.BalanceCents > 0 {
		b.node("surplus", i18n.C(ctx, "dash.surplus"), "#22c55e", layerClass)
		b.link("trunk", "surplus", rep.BalanceCents, "#22c55e")
	}
	b.finish()

	s := Sankey{Width: width, Height: height, Nodes: b.nodes, Links: b.links, Deficit: deficit}
	layoutSankey(&s)
	return s
}

// classCategoryTotals splits the per-category totals across budget classes,
// because one category may carry bookings of more than one class. The result
// is the monthly average over the period, matching PeriodReport.
func classCategoryTotals(ctx context.Context, d Data, months []string, member int64) map[store.BudgetClass][]LabeledTotal {
	active := activeMonths(d, months)
	n := int64(len(active))
	if n == 0 {
		return nil
	}

	type key struct {
		class store.BudgetClass
		cat   int64
	}
	sums := make(map[key]int64)
	for _, m := range active {
		for _, bk := range d.Bookings {
			if bk.Direction != store.DirExpense || !ActiveIn(bk, m) {
				continue
			}
			amount := float64(AmountFor(bk, d.Overrides[bk.ID], m)) * monthlyFactor(bk)
			if member != Everyone {
				shares, _ := allocate(amount, bk, d.Splits[bk.ID], d.Members)
				share, ok := shares[member]
				if !ok {
					continue
				}
				amount = share
			}
			sums[key{bk.BudgetClass, bk.CategoryID}] += round(amount)
		}
	}

	names := make(map[int64]store.Category, len(d.Categories))
	for _, c := range d.Categories {
		names[c.ID] = c
	}

	out := make(map[store.BudgetClass][]LabeledTotal)
	for k, v := range sums {
		if v /= n; v <= 0 {
			continue
		}
		c := names[k.cat]
		label := c.Name
		if label == "" {
			label = i18n.C(ctx, "label.noCategory")
		}
		out[k.class] = append(out[k.class], LabeledTotal{Key: k.cat, Label: label, Color: c.Color, Cents: v})
	}
	for class := range out {
		sortByCentsDesc(out[class])
	}
	return out
}

// layoutSankey assigns coordinates. Nodes are stacked per layer with a fixed
// gap, scaled by the busiest layer so the whole diagram fits the canvas.
func layoutSankey(s *Sankey) {
	const (
		nodeWidth = 14.0
		gap       = 10.0
		padTop    = 8.0
		// Captions sit to the right of their node, so the rightmost column
		// needs room or its labels would run off the canvas.
		labelPad = 110.0
	)
	if len(s.Nodes) == 0 {
		return
	}

	byLayer := make(map[int][]int)
	layers := make([]int, 0, 4)
	for i, n := range s.Nodes {
		if _, ok := byLayer[n.Layer]; !ok {
			layers = append(layers, n.Layer)
		}
		byLayer[n.Layer] = append(byLayer[n.Layer], i)
	}
	sort.Ints(layers)

	// The scale has to satisfy the tightest layer, otherwise a column overflows.
	scale := 0.0
	for _, l := range layers {
		idx := byLayer[l]
		var sum int64
		for _, i := range idx {
			sum += s.Nodes[i].Cents
		}
		if sum <= 0 {
			continue
		}
		avail := s.Height - padTop*2 - gap*float64(len(idx)-1)
		if avail <= 0 {
			continue
		}
		if f := avail / float64(sum); scale == 0 || f < scale {
			scale = f
		}
	}
	if scale <= 0 {
		return
	}

	maxLayer := layers[len(layers)-1]
	for _, l := range layers {
		idx := byLayer[l]
		sort.SliceStable(idx, func(a, b int) bool {
			return s.Nodes[idx[a]].Cents > s.Nodes[idx[b]].Cents
		})

		var stack float64
		for _, i := range idx {
			stack += float64(s.Nodes[i].Cents)*scale + gap
		}
		stack -= gap
		y := (s.Height - stack) / 2

		x := 0.0
		if maxLayer > 0 {
			x = float64(l) / float64(maxLayer) * (s.Width - labelPad - nodeWidth)
		}
		for _, i := range idx {
			h := float64(s.Nodes[i].Cents) * scale
			s.Nodes[i].X = x
			s.Nodes[i].Y = y
			s.Nodes[i].Width = nodeWidth
			s.Nodes[i].Height = h
			y += h + gap
		}
	}

	buildSankeyPaths(s, scale)
}

// buildSankeyPaths turns every link into a ribbon, consuming each node's edge
// from the top down so ribbons never overlap.
func buildSankeyPaths(s *Sankey, scale float64) {
	pos := make(map[string]int, len(s.Nodes))
	for i, n := range s.Nodes {
		pos[n.ID] = i
	}

	outOff := make(map[string]float64)
	inOff := make(map[string]float64)

	order := make([]int, len(s.Links))
	for i := range s.Links {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		na := s.Nodes[pos[s.Links[order[a]].Source]]
		nb := s.Nodes[pos[s.Links[order[b]].Source]]
		if na.Y != nb.Y {
			return na.Y < nb.Y
		}
		return s.Links[order[a]].Cents > s.Links[order[b]].Cents
	})

	for _, li := range order {
		l := &s.Links[li]
		src := s.Nodes[pos[l.Source]]
		dst := s.Nodes[pos[l.Target]]
		h := float64(l.Cents) * scale

		sy0 := src.Y + outOff[l.Source]
		ty0 := dst.Y + inOff[l.Target]
		outOff[l.Source] += h
		inOff[l.Target] += h

		x0 := src.X + src.Width
		x1 := dst.X
		xm := (x0 + x1) / 2

		var sb strings.Builder
		fmt.Fprintf(&sb, "M%.2f,%.2f", x0, sy0)
		fmt.Fprintf(&sb, "C%.2f,%.2f %.2f,%.2f %.2f,%.2f", xm, sy0, xm, ty0, x1, ty0)
		fmt.Fprintf(&sb, "L%.2f,%.2f", x1, ty0+h)
		fmt.Fprintf(&sb, "C%.2f,%.2f %.2f,%.2f %.2f,%.2f", xm, ty0+h, xm, sy0+h, x0, sy0+h)
		sb.WriteString("Z")
		l.Path = sb.String()
	}
}

func colorOr(c, fallback string) string {
	if c == "" {
		return fallback
	}
	return c
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
