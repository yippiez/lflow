package nodes

import (
	"regexp"
	"strings"
	"testing"

	"github.com/lflow/lflow/packages/database"
)

// plotTree is the study subtree: five levels, mixed leaves and containers.
//
//	Budget
//	├─ Rent 2000
//	├─ Food
//	│  ├─ Groceries
//	│  │  ├─ Produce
//	│  │  │  ├─ Fruit
//	│  │  │  │  ├─ Citrus 80
//	│  │  │  │  └─ Berries 100
//	│  │  │  └─ Veg 120
//	│  │  └─ Meat 400
//	│  └─ Dining 250
//	└─ Fun 500
func plotTree() *fakeNode {
	leaf := func(text string) *fakeNode { return &fakeNode{uuid: text, typ: database.TypeBullets, text: text} }
	branch := func(text string, kids ...*fakeNode) *fakeNode {
		n := &fakeNode{uuid: text, typ: database.TypeBullets, text: text, kids: kids}
		for _, k := range kids {
			k.parent = n
		}
		return n
	}
	return branch("Budget",
		leaf("Rent 2000"),
		branch("Food",
			branch("Groceries",
				branch("Produce",
					branch("Fruit", leaf("Citrus 80"), leaf("Berries 100")),
					leaf("Veg 120")),
				leaf("Meat 400")),
			leaf("Dining 250")),
		leaf("Fun 500"))
}

func plotKid(t *testing.T, n *fakeNode, name string) *fakeNode {
	t.Helper()
	for _, k := range n.kids {
		if strings.HasPrefix(k.text, name) {
			return k
		}
	}
	t.Fatalf("no child of %q named %q", n.text, name)
	return nil
}

// TestPlotValueSumsSubtree: a node's value is its own trailing number when the
// text carries one, else the sum of everything under it. Deep numbers have to
// count whether or not the depth is drawn — a plot that silently dropped level
// five would still look right, which is exactly why this is asserted by value.
func TestPlotValueSumsSubtree(t *testing.T) {
	root := plotTree()
	food := plotKid(t, root, "Food")
	groceries := plotKid(t, food, "Groceries")
	produce := plotKid(t, groceries, "Produce")
	fruit := plotKid(t, produce, "Fruit")

	for _, c := range []struct {
		name string
		n    *fakeNode
		want float64
	}{
		{"leaf reads its own number", plotKid(t, root, "Rent"), 2000},
		{"deepest container sums its leaves", fruit, 180},
		{"one level up", produce, 300},
		{"two levels up", groceries, 700},
		{"three levels up", food, 950},
		{"the whole tree", root, 3450},
	} {
		if got := plotValue(c.n); got != c.want {
			t.Errorf("%s: plotValue(%s) = %v, want %v", c.name, c.n.text, got, c.want)
		}
	}
}

// TestPlotOwnValueWins: an explicit number beats the subtree, so a node can
// state a total its children do not add up to without the plot overruling it.
func TestPlotOwnValueWins(t *testing.T) {
	n := &fakeNode{text: "Rounded 1000", kids: []*fakeNode{{text: "a 1"}, {text: "b 2"}}}
	for _, k := range n.kids {
		k.parent = n
	}
	if got := plotValue(n); got != 1000 {
		t.Errorf("plotValue = %v, want the node's own 1000, not the children's 3", got)
	}
	if got := plotDepth(n); got != 0 {
		t.Errorf("plotDepth = %d, want 0 — an explicit number is a leaf however many children it has", got)
	}
}

// TestPlotLabelStripsNumber: the bar's label is the text without the trailing
// number, so a row reads "Rent" beside its own 2000 rather than "Rent 2000".
func TestPlotLabelStripsNumber(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Rent 2000", "Rent"},
		{"Rent ─ 2000", "Rent"},
		{"Rent: 1,250", "Rent"},
		{"Food", "Food"},
		{"2000", "2000"}, // a bare number labels itself
	} {
		if got := plotLabel(&fakeNode{text: c.in}); got != c.want {
			t.Errorf("plotLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPlotAllocSumsExactly: children must fill exactly the parent's width at
// every level. A largest-remainder split is what keeps a deep band aligned with
// the bar above it — plain rounding drifts a cell per level and the icicle
// stops lining up with what contains it.
func TestPlotAllocSumsExactly(t *testing.T) {
	for _, c := range []struct {
		total int
		w     []float64
	}{
		{52, []float64{2000, 950, 500}},
		{25, []float64{700, 250}},
		{7, []float64{180, 120}},
		{3, []float64{80, 100}},
		{1, []float64{1, 1, 1}},
	} {
		got := plotAlloc(c.total, c.w)
		sum := 0
		for _, v := range got {
			sum += v
		}
		if sum != c.total {
			t.Errorf("plotAlloc(%d, %v) = %v summing to %d, want %d", c.total, c.w, got, sum, c.total)
		}
	}
}

// TestPlotLevelAlignsWithParent: every band's spans have to sum to the width of
// the bar they sit under, at every depth. This is the invariant that makes the
// icicle readable as containment rather than as a second, unrelated chart.
func TestPlotLevelAlignsWithParent(t *testing.T) {
	food := plotKid(t, plotTree(), "Food")
	const barW = 25
	for d := 1; d <= plotDepth(food); d++ {
		var spans []plotSpan
		plotLevel(food, barW, 0, d, 0, &spans)
		if len(spans) == 0 {
			t.Fatalf("depth %d: no spans", d)
		}
		sum := 0
		for _, s := range spans {
			sum += s.w
		}
		// a band stops where its branch runs out, so it may be narrower than the
		// bar — never wider, which would spill past what contains it
		if sum > barW {
			t.Errorf("depth %d: spans sum to %d, wider than the %d-cell bar", d, sum, barW)
		}
	}
	// the first band is the one level that must fill the bar exactly
	var top []plotSpan
	plotLevel(food, barW, 0, 1, 0, &top)
	sum := 0
	for _, s := range top {
		sum += s.w
	}
	if sum != barW {
		t.Errorf("depth 1 spans sum to %d, want the full %d", sum, barW)
	}
}

// TestPlotFitLadder: a cell shows as much as it can afford — "name value", then
// the name, then the bare number, then nothing but color — and it always
// occupies exactly its own width so the next cell starts where it should.
func TestPlotFitLadder(t *testing.T) {
	n := &fakeNode{text: "Meat 400"}
	for _, c := range []struct {
		w    int
		want string
	}{
		{12, " Meat 400   "},
		{7, " Meat  "},
		{5, " Meat"},
		{4, "Meat"},
		{3, "400"},
		{2, "  "},
	} {
		got := plotFit(n, c.w)
		if got != c.want {
			t.Errorf("plotFit(w=%d) = %q, want %q", c.w, got, c.want)
		}
		if len([]rune(got)) != c.w {
			t.Errorf("plotFit(w=%d) is %d cells wide, want %d", c.w, len([]rune(got)), c.w)
		}
	}
}

var plotSGR = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// TestPlotBandsRenderDepth: the whole plot, end to end — a bar per child and
// one band per level beneath the bars that have depth. The bars must line up in
// a single column, which is the thing that makes the values comparable.
func TestPlotBandsRenderDepth(t *testing.T) {
	lines := plotLines(plotTree(), "  ", 80)
	if len(lines) == 0 {
		t.Fatal("no lines rendered")
	}
	plain := make([]string, len(lines))
	for i, l := range lines {
		plain[i] = plotSGR.ReplaceAllString(l, "")
	}
	joined := strings.Join(plain, "\n")

	// three bars, and every level of Food's subtree got its own band
	for _, want := range []string{"Rent", "Food", "Fun", "Groceries", "Produce", "Meat", "Dining", "Veg"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rendered plot is missing %q:\n%s", want, joined)
		}
	}
	// Rent and Fun are leaves: no bands under them, so the line count is
	// 3 bars + one band per level of Food
	if want := 3 + plotDepth(plotKid(t, plotTree(), "Food")); len(lines) != want {
		t.Errorf("got %d lines, want %d (3 bars + %d bands)\n%s", len(lines), want, want-3, joined)
	}
	// the bars share one left edge
	var barCols []int
	for _, p := range plain {
		if i := strings.Index(p, "█"); i >= 0 {
			barCols = append(barCols, len([]rune(p[:i])))
		}
	}
	if len(barCols) != 3 {
		t.Fatalf("expected 3 bar rows, got %d", len(barCols))
	}
	for _, c := range barCols[1:] {
		if c != barCols[0] {
			t.Errorf("bars start at differing columns %v — the values stop being comparable", barCols)
		}
	}
}

// TestPlotBandsTooNarrow: below a usable bar width the plot draws nothing
// rather than a misleading one-cell bar.
func TestPlotBandsTooNarrow(t *testing.T) {
	if got := plotLines(plotTree(), "  ", 20); got != nil {
		t.Errorf("expected no plot in a 20-column window, got %d lines", len(got))
	}
}

// TestPlotBandsNoChildren: a plot node with nothing under it renders nothing.
func TestPlotBandsNoChildren(t *testing.T) {
	if got := plotLines(&fakeNode{text: "Empty"}, "  ", 80); got != nil {
		t.Errorf("expected no lines for a childless plot, got %v", got)
	}
}

// TestPlotTailRestingFace: at rest the node is one line — the chart is not
// drawn until alt+e, so an outline full of plots stays an outline. The tail
// runs on the Model-free path where a ref yields raw names, which is exactly
// where a total computed from child text could silently come out zero.
func TestPlotTailRestingFace(t *testing.T) {
	got := plotSGR.ReplaceAllString(plotTail(plotTree()), "")
	for _, want := range []string{"3 bars", "3450"} {
		if !strings.Contains(got, want) {
			t.Errorf("resting tail %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "█") {
		t.Errorf("resting tail draws bars, so the node is not a single line: %q", got)
	}
	if strings.Count(got, "\n") != 0 {
		t.Errorf("resting tail spans more than one line: %q", got)
	}
	// singular reads right for one bar, and a childless plot says nothing at all
	one := plotSGR.ReplaceAllString(plotTail(&fakeNode{text: "Solo", kids: []*fakeNode{{text: "a 1"}}}), "")
	if !strings.Contains(one, "1 bar ") {
		t.Errorf("one-bar tail = %q, want a singular 'bar'", one)
	}
	if got := plotTail(&fakeNode{text: "Empty"}); got != "" {
		t.Errorf("childless plot tail = %q, want nothing", got)
	}
}

// TestPlotViewOpensToTheChart: alt+e is the face that draws. It declines to
// open when there is nothing to plot, and what it renders is the header plus
// every line of the chart.
func TestPlotViewOpensToTheChart(t *testing.T) {
	h, v := newFakeHost(t), plotView{}
	tree := plotTree()

	if !v.Enter(h, tree) {
		t.Error("Enter declined a plot that has bars to draw")
	}
	if v.Enter(h, &fakeNode{text: "Empty"}) {
		t.Error("Enter opened a blank band for a plot with no children")
	}

	const width = 80
	bands := v.Bands(h, tree, "  ", width, 0, 40, true)
	body := plotLines(tree, "  ", width)
	if len(bands) != len(body)+1 {
		t.Fatalf("view rendered %d lines, want %d (header + %d chart lines)", len(bands), len(body)+1, len(body))
	}
	if head := plotSGR.ReplaceAllString(bands[0], ""); !strings.Contains(head, "3 bars") ||
		!strings.Contains(head, "esc close") {
		t.Errorf("view header = %q, want the count and the way out", head)
	}
	if want := v.Lines(h, tree, width); want != len(bands) {
		t.Errorf("Lines said %d but Bands rendered %d — the editor would clip or gap the view", want, len(bands))
	}
}

// TestPlotViewWindowsToItsViewport: Bands self-windows to [scroll, scroll+winH),
// so a plot taller than the space it is given scrolls instead of overrunning
// the rows beneath it.
func TestPlotViewWindowsToItsViewport(t *testing.T) {
	h, v := newFakeHost(t), plotView{}
	tree := plotTree()
	full := v.Bands(h, tree, "  ", 80, 0, 99, true)

	if got := v.Bands(h, tree, "  ", 80, 0, 3, true); len(got) != 3 {
		t.Errorf("winH=3 rendered %d lines, want 3", len(got))
	}
	got := v.Bands(h, tree, "  ", 80, 2, 99, true)
	if len(got) != len(full)-2 {
		t.Errorf("scroll=2 rendered %d lines, want %d", len(got), len(full)-2)
	}
	if len(got) > 0 && got[0] != full[2] {
		t.Error("scroll=2 did not start at the third line")
	}
	// scrolling past the end empties out rather than panicking
	if got := v.Bands(h, tree, "  ", 80, len(full)+50, 99, true); len(got) != 0 {
		t.Errorf("scrolling past the end rendered %d lines, want 0", len(got))
	}
}

// TestPlotCountPluralizes: both faces count out loud, and "1 levels" in a
// header is the kind of thing that reads as a half-finished feature.
func TestPlotCountPluralizes(t *testing.T) {
	for _, c := range []struct {
		n    int
		unit string
		want string
	}{
		{0, "bar", "0 bars"},
		{1, "bar", "1 bar"},
		{2, "bar", "2 bars"},
		{1, "level", "1 level"},
		{5, "level", "5 levels"},
	} {
		if got := plotCount(c.n, c.unit); got != c.want {
			t.Errorf("plotCount(%d, %q) = %q, want %q", c.n, c.unit, got, c.want)
		}
	}
	// and the header actually uses it
	flat := &fakeNode{text: "Flat", kids: []*fakeNode{{text: "only 1"}}}
	for _, k := range flat.kids {
		k.parent = flat
	}
	head := plotSGR.ReplaceAllString(plotView{}.Bands(newFakeHost(t), flat, "  ", 80, 0, 9, true)[0], "")
	if !strings.Contains(head, "1 bar ") || !strings.Contains(head, "1 level") {
		t.Errorf("header = %q, want singular units", head)
	}
}
