package editor

import (
	"math"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/lflow/lflow/tui/database"
)

// The BarPlot node is a PLOT READING of an ordinary subtree — not a new
// storage shape, not a saved bitmap:
//
//	barplot        the node typed /barplot; its text is the title
//	├─ Apples      a first-level child IS a bar; its text is the label
//	│  └─ 42       a bar's value is its own text when that parses as a
//	│              number, and otherwise the RECURSIVE SUM of its subtree —
//	│              so deeper levels (children of children) always count
//	└─ Food        and a bar's own children draw as stacked segments on
//	   ├─ 1200     its row, each in its own dither/color; anything deeper
//	   └─ 800      still sums, it just never draws
//
// Because it is only a reading, ANY node converts: /type Bar Plot moves nothing
// and loses nothing. The two faces are the fold state, like the table:
//
//	plot face  the node is FOLDED — the bars draw as a band beneath the row
//	nodes face the node is OPEN — the same content as a plain outline
//
// so ctrl+space / alt+↑ / alt+↓ toggle plot ⇄ nodes and the choice persists
// like any fold. There is no editor: the values ARE the nodes, typed in the
// nodes face, and the plot re-reads them on every frame.
//
// The styles a bar carries map onto its drawing: color: → the fill color,
// bold → a bright solid fill, italic → a hatched fill, underline → the bar
// draws as a hollow box (two lines), strike → dim and left out of the scale.
// The plot node itself uses two: color: tints the title and becomes the
// default fill for unstyled bars, and underline collapses the band to a
// one-line sparkline.

func init() {
	registerType(nodeType{
		key:            database.TypeBarPlot,
		label:          "Bar Plot",
		inlineEditable: true,
		glyph:          barplotGlyph,
		bands:          func(m *Model, r row, below bool, maxLine int) []string { return m.barplotBandLines(r, below, maxLine) },
		onType:         barplotOnType,
	})
}

const (
	glyphBarPlot = "▃"
	// how wide the label column may grow before bars get squeezed away.
	barplotLabelMax = 24
)

// the fill patterns: the bar itself is solid, stacked segments cycle through
// the dithers by sibling index, and anything deeper never draws.
var barplotFills = []string{"█", "▓", "▒", "░"}

// the sparkline blocks, low to high.
var barplotBlocks = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// barplotFaceBand reports the plot face: a folded bar plot draws its band, an
// open one is the plain outline.
func barplotFaceBand(it *item) bool { return it != nil && it.collapsed }

// barplotGlyph marks a bar-plot row; the plot face lights the glyph accent so
// the two faces read apart at a glance.
func barplotGlyph(it *item) (string, string) {
	if barplotFaceBand(it) {
		return glyphBarPlot, cAccent
	}
	return glyphBarPlot, cDim
}

// barplotOnType lands a node freshly converted with /type on its plot face:
// the bars and stacks it already had ARE the plot, so the band is the answer
// to "make this a plot" — the nodes face is one alt+↓ away.
func barplotOnType(m *Model, it *item) { m.setTableFace(it, true) }

// ── the plot reading ────────────────────────────────────────────────────────

// barplotBar is one first-level child read as a bar.
type barplotBar struct {
	it    *item
	label string
	value float64
	segs  []barplotSeg // the bar's own children as stacked segments
}

// barplotSeg is one child of a bar: its share of the stack.
type barplotSeg struct {
	it    *item
	label string
	value float64
}

// barplotNum parses a node's own text as a number. Inf/NaN are not numbers a
// plot can draw, so they are refused and the node reads as a container.
func barplotNum(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// barplotValue is a node's value: its own text when numeric, else the
// recursive sum of its subtree — unbounded depth, always the true total.
func (m *Model) barplotValue(it *item) float64 {
	if it == nil {
		return 0
	}
	if v, ok := barplotNum(m.tableCellText(it)); ok {
		return v
	}
	sum := 0.0
	for _, c := range tableKids(m, it) {
		sum += m.barplotValue(c)
	}
	return sum
}

// barplotBars reads the plot: every first-level child is a bar in outline
// order (rank order is the x-axis order; the plot never re-sorts).
func (m *Model) barplotBars(it *item) []barplotBar {
	var bars []barplotBar
	for _, c := range tableKids(m, it) {
		b := barplotBar{it: c, label: m.tableCellText(c), value: m.barplotValue(c)}
		if len(tableKids(m, c)) > 0 {
			for _, s := range tableKids(m, c) {
				b.segs = append(b.segs, barplotSeg{it: s, label: m.tableCellText(s), value: m.barplotValue(s)})
			}
		}
		bars = append(bars, b)
	}
	return bars
}

// barplotStruck reports whether a bar is struck out: drawn dim and left out of
// the scale, so a "dead" row stops inflating the others.
func barplotStruck(b barplotBar) bool { return styleHas(b.it.style, "strike") }

// barplotScale is the longest drawn bar — struck bars are excluded so a
// struck-out giant cannot flatten the whole plot.
func barplotScale(bars []barplotBar) float64 {
	scale := 0.0
	for _, b := range bars {
		if barplotStruck(b) {
			continue
		}
		if b.value > scale {
			scale = b.value
		}
	}
	return scale
}

// barplotVal formats a value for the right-hand column: integers stay plain,
// anything else prints shortest.
func barplotVal(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// barplotFillColor is a bar's fill color: its own /color, else the plot
// node's color (the default tint), else the plain foreground.
func barplotFillColor(b barplotBar, plotColor string) string {
	if c := styleColor(b.it.style); c != "" {
		return styleColorCode[c]
	}
	if plotColor != "" {
		return styleColorCode[plotColor]
	}
	return cFG
}

// ── the plot face (the always-on band) ──────────────────────────────────────

// barplotLines paints the band: an optional dim title line, then one row per
// bar — label, fill, value — or, when the plot node itself is underlined, a
// single sparkline row instead.
func (m *Model) barplotLines(it *item, width int) []string {
	bars := m.barplotBars(it)
	if len(bars) == 0 {
		return []string{cDim + "  empty plot · unfold to add bars" + cReset}
	}
	if width < 16 {
		width = 16
	}
	plotColor := styleColor(it.style)
	if styleHas(it.style, "underline") {
		return []string{m.barplotSparkline(it, bars, width)}
	}

	title := ""
	if t := m.tableCellText(it); t != "" {
		title = cDim + t + cReset
		// the band must not shear: the title row pads out to the bar width
		if pad := width - visibleWidth(title); pad > 0 {
			title += strings.Repeat(" ", pad)
		}
	}

	labelW := 0
	for _, b := range bars {
		if w := runewidth.StringWidth(b.label); w > labelW {
			labelW = w
		}
	}
	if labelW > barplotLabelMax {
		labelW = barplotLabelMax
	}
	valueW := 0
	for _, b := range bars {
		if w := runewidth.StringWidth(barplotVal(b.value)); w > valueW {
			valueW = w
		}
	}
	fillW := width - labelW - valueW - 3 // two spaces after the label, one before the value
	if fillW < 4 {
		fillW = 4
	}

	scale := barplotScale(bars)
	lines := make([]string, 0, len(bars)+1)
	if title != "" {
		lines = append(lines, title)
	}
	for _, b := range bars {
		lines = append(lines, m.barplotBarLines(b, labelW, valueW, fillW, scale, plotColor)...)
	}
	return lines
}

// barplotBarLines draws one bar: one fill row, or — an underlined bar — the
// hollow box, two rows. All rows share the label/value/fill columns so the
// band never shears.
func (m *Model) barplotBarLines(b barplotBar, labelW, valueW, fillW int, scale float64, plotColor string) []string {
	label := clip(elideMiddle(b.label, labelW), labelW)
	label = label + strings.Repeat(" ", labelW-runewidth.StringWidth(label))
	val := barplotVal(b.value)
	val = val + strings.Repeat(" ", valueW-runewidth.StringWidth(val))
	pre := label + "  "
	suf := " " + val

	if barplotStruck(b) {
		fill := cDim + cStrike + strings.Repeat("░", fillW) + cReset
		return []string{pre + fill + suf}
	}

	fill := m.barplotFill(b, fillW, scale, plotColor)
	if styleHas(b.it.style, "underline") {
		top := "┌" + strings.Repeat("─", fillW) + "┐"
		bot := "└" + strings.Repeat("─", fillW) + "┘"
		return []string{pre + cFG + top + cReset + suf, pre + cFG + bot + cReset + suf}
	}
	return []string{pre + fill + suf}
}

// barplotFill draws a bar's fill to exactly fillW cells. A bar with segments
// draws its stack: each segment gets its share of the cells in its own
// dither/color. A plain bar is one solid run. Bars fill only their share of
// the scale — the cells past it stay empty — and zero/negative values draw
// nothing (the label still says the number).
func (m *Model) barplotFill(b barplotBar, fillW int, scale float64, plotColor string) string {
	if b.value <= 0 || scale <= 0 {
		return strings.Repeat(" ", fillW)
	}
	totalFill := int(float64(fillW) * b.value / scale)
	if totalFill > fillW {
		totalFill = fillW
	}
	base := barplotFillColor(b, plotColor)
	if styleHas(b.it.style, "bold") {
		base += cBold
	}
	empty := strings.Repeat(" ", fillW-totalFill)
	// one child or none: the bar is its value — a plain solid run. A stack is
	// only a stack when it has two or more segments to tell apart.
	if len(b.segs) <= 1 {
		fill := "█"
		if styleHas(b.it.style, "italic") {
			fill = "▓"
		}
		return base + strings.Repeat(fill, totalFill) + cReset + empty
	}

	// the stack: each segment covers its share of the bar's own cells; segment
	// boundaries land on cell edges (a fractional edge rounds down to the next
	// cell, so a short segment may disappear rather than borrow a neighbor's).
	var out []string
	cum := 0.0
	cells := 0
	for i, s := range b.segs {
		cum += s.value
		end := int(float64(totalFill) * cum / b.value)
		if end < cells {
			end = cells
		}
		if end > totalFill {
			end = totalFill
		}
		segFill := "█"
		segColor := cFG
		if c := styleColor(s.it.style); c != "" {
			segColor = styleColorCode[c]
		}
		if i+1 < len(barplotFills) {
			segFill = barplotFills[i+1]
		}
		out = append(out, segColor+strings.Repeat(segFill, end-cells))
		cells = end
	}
	return strings.Join(out, "") + cReset + empty
}

// barplotSparkline is the underlined plot node's face: the whole band
// collapses to one row — title, then one block per bar scaled to the longest.
// Struck bars stand in as a dim dot.
func (m *Model) barplotSparkline(it *item, bars []barplotBar, width int) string {
	title := m.tableCellText(it)
	head := ""
	if title != "" {
		head = cDim + title + cReset + " "
	}
	scale := barplotScale(bars)
	var b strings.Builder
	b.WriteString(head)
	for _, bar := range bars {
		if barplotStruck(bar) {
			b.WriteString(cDim + "·" + cReset)
			continue
		}
		idx := 0
		if bar.value > 0 && scale > 0 {
			idx = int(bar.value/scale*8) - 1
			if idx < 0 {
				idx = 0
			}
			if idx > 7 {
				idx = 7
			}
		}
		b.WriteString(barplotBlocks[idx])
	}
	line := b.String()
	if w := runewidth.StringWidth(line); w < width {
		line += strings.Repeat(" ", width-w)
	}
	return line
}

// barplotBandLines hangs the plot beneath a folded bar-plot node — the
// always-on face, drawn like the table grid and run-output bands.
func (m *Model) barplotBandLines(r row, below bool, maxLine int) []string {
	if !barplotFaceBand(r.it) {
		return nil // the nodes face: the bars render as ordinary rows
	}
	if m.quitting {
		// the final scrollback frame is the whole outline FULLY EXPANDED —
		// this fold opens there too, so the band would print the plot twice.
		return nil
	}
	rail := continuationPrefix(r, below)
	lines := m.barplotLines(m.renderItem(r.it), maxLine-visibleWidth(rail))
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = clip(rail+cReset+l, maxLine)
	}
	return out
}
