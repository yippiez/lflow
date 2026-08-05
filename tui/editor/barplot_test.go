package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// newBarPlotModel builds a model whose first row is a bar-plot node folded to
// its plot face, with the given children already hanging under it.
func newBarPlotModel(kids ...*item) (*Model, *item) {
	root := &item{}
	tr := &tree{root: root, byUUID: map[string]*item{}, externalNames: map[string]string{}}
	plot := &item{uuid: "plot", name: "sales", typ: database.TypeBarPlot, parent: root, collapsed: true}
	tr.byUUID["plot"] = plot
	for _, k := range kids {
		k.parent = plot
		plot.children = append(plot.children, k)
	}
	root.children = append(root.children, plot)
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	return m, plot
}

// barItem builds a bar (or segment) item; deeper children nest in.
func barItem(uuid, name string, kids ...*item) *item {
	it := &item{uuid: uuid, name: name}
	for _, k := range kids {
		k.parent = it
		it.children = append(it.children, k)
	}
	return it
}

// styled sets an item's style tokens (builder convenience).
func (it *item) styled(s string) *item { it.style = s; return it }

// TestBarPlotReadsChildrenAsBars pins the reading: every first-level child is
// a bar, a numeric leaf's own text is its value, and a non-numeric leaf with
// no children is a label with an empty bar.
func TestBarPlotReadsChildrenAsBars(t *testing.T) {
	m, plot := newBarPlotModel(
		barItem("a", "a"),
		barItem("b", "100"),
		barItem("c", "200"),
		barItem("d", "300"),
	)
	bars := m.barplotBars(plot)
	if len(bars) != 4 {
		t.Fatalf("bars = %d, want 4 (the plot's children are the bars)", len(bars))
	}
	if bars[0].label != "a" || bars[0].value != 0 {
		t.Errorf("bar a = %q/%v, want label with empty bar", bars[0].label, bars[0].value)
	}
	if bars[1].value != 100 || bars[2].value != 200 || bars[3].value != 300 {
		t.Errorf("numeric leaves must read their own text: %v %v %v",
			bars[1].value, bars[2].value, bars[3].value)
	}
}

// TestBarPlotValueIsRecursiveSum: a container's value is the sum of its whole
// subtree, however deep — children of children always count.
func TestBarPlotValueIsRecursiveSum(t *testing.T) {
	m, plot := newBarPlotModel(
		barItem("a", "Apples", barItem("a1", "42")),
		barItem("b", "Food",
			barItem("b1", "Groceries", barItem("b11", "700")),
			barItem("b2", "Dining", barItem("b21", "250")),
		),
		barItem("c", "Deep", barItem("c1", "Sub", barItem("c11", "5"))),
	)
	bars := m.barplotBars(plot)
	want := []float64{42, 950, 5}
	for i, w := range want {
		if bars[i].value != w {
			t.Errorf("bar %d value = %v, want %v (the recursive subtree sum)", i, bars[i].value, w)
		}
	}
	if len(bars[1].segs) != 2 {
		t.Errorf("Food segments = %d, want 2 (a bar's children are its stack)", len(bars[1].segs))
	}
}

// TestBarPlotBandDrawsBars is the render contract: the title, the bars and
// their values, every line the same width, none wider than the band.
func TestBarPlotBandDrawsBars(t *testing.T) {
	m, plot := newBarPlotModel(
		barItem("a", "Apples", barItem("a1", "42")),
		barItem("b", "Bananas", barItem("b1", "37")),
		barItem("c", "Cherries", barItem("c1", "12")),
	)
	lines := m.barplotLines(plot, 60)
	joined := stripSGR(strings.Join(lines, "\n"))
	for _, want := range []string{"sales", "Apples", "Bananas", "Cherries", "42", "37", "12", "█"} {
		if !strings.Contains(joined, want) {
			t.Errorf("band missing %q:\n%s", want, joined)
		}
	}
	w := visibleWidth(lines[1])
	for i, l := range lines {
		if got := visibleWidth(l); got != w {
			t.Errorf("line %d width = %d, want %d (the band must not shear)\n%s", i, got, w, joined)
		}
	}
	if w > 60 {
		t.Errorf("band width = %d, want <= 60", w)
	}
}

// TestBarPlotStackDrawsSegments: a bar with children draws its stack on one
// line, each segment in its own dither.
func TestBarPlotStackDrawsSegments(t *testing.T) {
	m, plot := newBarPlotModel(
		barItem("f", "Food",
			barItem("f1", "1200"),
			barItem("f2", "800"),
		),
	)
	lines := m.barplotLines(plot, 60)
	joined := stripSGR(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "▓") || !strings.Contains(joined, "▒") {
		t.Errorf("stack must draw segments in their own dithers:\n%s", joined)
	}
	if !strings.Contains(joined, "2000") {
		t.Errorf("stacked bar must show its sum (2000):\n%s", joined)
	}
}

// TestBarPlotStrikeIsDimAndExcluded: a struck bar draws dim, and its value
// never inflates the scale the others are drawn against.
func TestBarPlotStrikeIsDimAndExcluded(t *testing.T) {
	m, plot := newBarPlotModel(
		barItem("big", "Retired", barItem("big1", "10000")).styled("strike"),
		barItem("s", "Small", barItem("s1", "10")),
	)
	if got := barplotScale(m.barplotBars(plot)); got != 10 {
		t.Fatalf("scale = %v, want 10 (a struck bar must not inflate the scale)", got)
	}
	lines := m.barplotLines(plot, 60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, cDim) || !strings.Contains(joined, cStrike) {
		t.Errorf("struck bar must draw dim and struck-through:\n%s", joined)
	}
}

// TestBarPlotRootUnderlineIsSparkline: an underlined plot node collapses the
// band to one sparkline row of blocks, one per bar.
func TestBarPlotRootUnderlineIsSparkline(t *testing.T) {
	m, plot := newBarPlotModel(
		barItem("a", "100"),
		barItem("b", "200"),
		barItem("c", "300"),
	)
	plot.style = "underline"
	lines := m.barplotLines(plot, 60)
	if len(lines) != 1 {
		t.Fatalf("sparkline band = %d lines, want 1:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	joined := stripSGR(lines[0])
	// 100/200/300 against a scale of 300 → ▂ ▅ █
	for _, want := range []string{"sales", "▂", "▅", "█"} {
		if !strings.Contains(joined, want) {
			t.Errorf("sparkline missing %q: %q", want, joined)
		}
	}
}

// TestBarPlotGlyph: the plot face lights the ▃ glyph accent, the nodes face
// dims it — the two faces read apart at a glance.
func TestBarPlotGlyph(t *testing.T) {
	_, plot := newBarPlotModel(barItem("a", "100"))
	g, c := barplotGlyph(plot)
	if g != "▃" || c != cAccent {
		t.Errorf("folded glyph = %q/%q, want ▃ in accent", g, c)
	}
	plot.collapsed = false
	g, c = barplotGlyph(plot)
	if c != cDim {
		t.Errorf("open glyph color = %q, want dim", c)
	}
}

// TestBarPlotMarkdownRoundTrips: the marker restores the type and title, the
// display fence is consumed (never a code node), and the data list reopens as
// the bars — the same marker contract every foreign type follows.
func TestBarPlotMarkdownRoundTrips(t *testing.T) {
	doc := []*SrcNode{{
		Type: database.TypeBarPlot,
		Text: "sales",
		Kids: []*SrcNode{
			{Type: database.TypeBullets, Text: "Apples", Kids: []*SrcNode{{Type: database.TypeBullets, Text: "42"}}},
			{Type: database.TypeBullets, Text: "Bananas", Kids: []*SrcNode{{Type: database.TypeBullets, Text: "37"}}},
		},
	}}
	out, err := (markdownCodec{}).Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<!-- "+fallbackLead+"barplot: sales -->") {
		t.Fatalf("render must carry the barplot marker:\n%s", out)
	}
	if !strings.Contains(out, "```text") {
		t.Fatalf("render must carry the display fence:\n%s", out)
	}
	back, err := (markdownCodec{}).Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) == 0 {
		t.Fatalf("parsed nothing:\n%s", out)
	}
	n := back[0]
	if n.Type != database.TypeBarPlot || n.Text != "sales" {
		t.Errorf("parsed = %s/%q, want barplot/sales", n.Type, n.Text)
	}
	var codeKids int
	for _, k := range back {
		if k.Type == database.TypeCode {
			codeKids++
		}
	}
	if codeKids != 0 {
		t.Errorf("the display fence must be consumed, not a code node (%d found)", codeKids)
	}
}
