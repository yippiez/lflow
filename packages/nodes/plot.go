package nodes

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/editor"
)

// The plot node: a bar plot composed AS an outline. Each child is a bar; a
// bar's value is the trailing number in its own text when it carries one, else
// the sum of its subtree — so a number always counts whether or not it draws.
//
// Depth is the hard part. One row of cells can encode order and share but never
// containment, because only leaves own cells: a container is exactly the span
// its children occupy. Encoding depth in the glyph (█▓▒░) or in a luminance
// ramp buys four levels at most and then spends the color budget the user needs
// for /color. So depth here is VERTICAL — under each bar, one band per level,
// every cell aligned inside its parent's span and labelled in place. A tree of
// any depth plots, because level nine is just band nine, and bands appear only
// under the bars that actually have children.
//
// The chart is the node's alt+e face, not an always-on band: at rest the node
// is one line wearing a count and a total, like the Image node's row. An
// outline full of plots stays an outline you can read, and the one you are
// looking at opens.
func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key:            database.TypePlot,
		Label:          "Plot",
		InlineEditable: true,
		Glyph:          func() (string, string) { return "▚", editor.NodeTheme().Cyan },
		BodyTail:       plotTail,
		View:           plotView{},
	})
}

// plotTail is the resting face: what the plot would show, said in a few dim
// characters after the node's own text. It runs on the Model-free render path,
// where a ref carries structure and raw names only — enough, because a bar's
// value is parsed out of its own text.
func plotTail(n editor.NodeRef) string {
	bars := n.Children()
	if len(bars) == 0 {
		return ""
	}
	t := editor.NodeTheme()
	return t.Dim + fmt.Sprintf("  ▪ %s · %s · ⌥e", plotCount(len(bars), "bar"), plotNum(plotValue(n))) + t.Reset
}

// plotCount pluralizes a count for the two faces' summaries.
func plotCount(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// plotView is the alt+e face: the chart itself, in bands beneath the node.
// Stateless — every number is derived from the subtree on each render, so an
// edit to any descendant is reflected the next time it is drawn.
type plotView struct{}

// Enter declines when there is nothing to plot, so alt+e on an empty plot node
// leaves the cursor where it is instead of opening a blank band.
func (plotView) Enter(h editor.NodeHost, n editor.NodeRef) bool { return len(n.Children()) > 0 }

func (plotView) Leave(h editor.NodeHost, n editor.NodeRef) {}

// Lines is the band height: the header plus a row per bar and per level.
func (plotView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	return 1 + len(plotLines(n, "", width))
}

// Key captures nothing: there is no text to edit and no state to move, so
// arrows and esc fall through to the editor's own scroll and defocus.
func (plotView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	return nil, false
}

// Bands renders the header and the chart, self-windowed to [scroll, scroll+winH).
func (plotView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	t := editor.NodeTheme()
	lines := plotLines(n, rail, width)
	if len(lines) == 0 {
		return []string{rail + t.Dim + "  plot · nothing to draw · esc close" + t.Reset}
	}
	bars := n.Children()
	head := fmt.Sprintf("  plot · %s · %s · %s · esc close",
		plotCount(len(bars), "bar"), plotNum(plotValue(n)),
		plotCount(plotTreeDepth(n), "level"))
	out := append([]string{rail + t.Dim + head + t.Reset}, lines...)

	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(out) {
		scroll = len(out)
	}
	out = out[scroll:]
	if winH > 0 && len(out) > winH {
		out = out[:winH]
	}
	return out
}

// plotTreeDepth is how many levels the plot actually draws, for the header.
func plotTreeDepth(n editor.NodeRef) int {
	d := 0
	for _, k := range n.Children() {
		if kd := plotDepth(k) + 1; kd > d {
			d = kd
		}
	}
	return d
}

// plotNum formats a value the way both faces show it.
func plotNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// plotMaxBarW caps the bar field so a wide terminal does not stretch a
// three-bar plot across the whole window.
const plotMaxBarW = 52

// plotValue is the resolving rule: own trailing number, else the subtree sum.
func plotValue(n editor.NodeRef) float64 {
	if v, ok := plotOwnValue(n); ok {
		return v
	}
	var sum float64
	for _, k := range n.Children() {
		sum += plotValue(k)
	}
	return sum
}

// plotOwnValue reads a trailing number off a node's text: "Rent 2000",
// "Rent ─ 2000", "Rent: 1,250" and a bare "2000" all resolve. Anything else is
// a container, and its value comes from its children.
func plotOwnValue(n editor.NodeRef) (float64, bool) {
	f := strings.Fields(strings.TrimSpace(n.Text()))
	if len(f) == 0 {
		return 0, false
	}
	last := strings.TrimRight(f[len(f)-1], ".,;:")
	last = strings.ReplaceAll(last, ",", "")
	v, err := strconv.ParseFloat(last, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// plotLabel is a node's text with the trailing number stripped, so a bar reads
// "Rent" and not "Rent 2000" beside its own value.
func plotLabel(n editor.NodeRef) string {
	t := strings.TrimSpace(n.Text())
	if _, ok := plotOwnValue(n); !ok {
		return t
	}
	f := strings.Fields(t)
	if len(f) <= 1 {
		return t // a bare number labels itself
	}
	return strings.TrimRight(strings.Join(f[:len(f)-1], " "), " ─-:·")
}

// plotDepth is how many levels sit below a node, so a plain leaf is 0.
func plotDepth(n editor.NodeRef) int {
	if _, ok := plotOwnValue(n); ok {
		return 0 // an explicit number is a leaf however many children it has
	}
	d := 0
	for _, k := range n.Children() {
		if kd := plotDepth(k) + 1; kd > d {
			d = kd
		}
	}
	return d
}

// plotAlloc splits total cells across weights by largest remainder, so a set of
// children always sums to exactly the parent's width and no rounding drifts as
// the recursion goes deeper.
func plotAlloc(total int, w []float64) []int {
	out := make([]int, len(w))
	var sum float64
	for _, x := range w {
		sum += x
	}
	if sum <= 0 || total <= 0 {
		return out
	}
	type rem struct {
		i int
		r float64
	}
	rs := make([]rem, 0, len(w))
	used := 0
	for i, x := range w {
		exact := x / sum * float64(total)
		out[i] = int(exact)
		used += out[i]
		rs = append(rs, rem{i, exact - math.Floor(exact)})
	}
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].r > rs[b].r })
	for k := 0; used < total && len(rs) > 0; k++ {
		out[rs[k%len(rs)].i]++
		used++
	}
	return out
}

// plotSpan is one cell run in a band: the node it belongs to, its width, and
// its index among its siblings (which drives the light/dark alternation that
// keeps touching cells apart).
type plotSpan struct {
	n   editor.NodeRef
	w   int
	sib int
}

// plotLevel collects the spans at exactly one depth below a bar. A branch
// shallower than the requested level contributes nothing, which is what leaves
// the gaps under short branches.
func plotLevel(n editor.NodeRef, w, depth, want int, sib int, out *[]plotSpan) {
	if depth == want {
		*out = append(*out, plotSpan{n, w, sib})
		return
	}
	if _, ok := plotOwnValue(n); ok {
		return
	}
	kids := n.Children()
	ws := make([]float64, len(kids))
	for i, k := range kids {
		ws[i] = plotValue(k)
	}
	cs := plotAlloc(w, ws)
	for i, k := range kids {
		if cs[i] > 0 {
			plotLevel(k, cs[i], depth+1, want, i, out)
		}
	}
}

// plotFit is the label ladder inside an icicle cell: full "name value", then
// the name alone, then the bare number, then nothing but color. It is what
// keeps a deep, narrow cell honest instead of spilling into its neighbour.
func plotFit(n editor.NodeRef, w int) string {
	name := plotLabel(n)
	num := strconv.FormatFloat(plotValue(n), 'f', -1, 64)
	// the leading space is what keeps two touching cells from reading as one
	// word ("FruitVeg"), so every rung that can afford one carries it
	for _, c := range []string{" " + name + " " + num + " ", " " + name + " ", " " + name, name, " " + num, num} {
		if r := []rune(c); len(r) <= w {
			return c + strings.Repeat(" ", w-len(r))
		}
	}
	return strings.Repeat(" ", w)
}

// plotFG / plotBG build SGR from the theme's own /color swatches.
func plotFG(r, g, b int) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b) }
func plotBG(r, g, b int) string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b) }

// plotTint walks a swatch toward the terminal ground — one step separates
// touching sibling cells without inventing a color outside the palette.
func plotTint(r, g, b int, f float64) (int, int, int) {
	const ground = 12.0
	s := func(c int) int { return int(ground + (float64(c)-ground)*f) }
	return s(r), s(g), s(b)
}

// plotInk picks a legible foreground for a label sitting on a filled cell.
func plotInk(r, g, b int) string {
	if 0.299*float64(r)+0.587*float64(g)+0.114*float64(b) > 150 {
		return "\x1b[38;2;24;20;16m"
	}
	return "\x1b[38;2;246;240;234m"
}

// plotSeries is the categorical order the bars cycle through — the same locked
// swatches the /color picker offers, resequenced for adjacent contrast. It
// leads with blue rather than the picker's red, because a red first bar reads
// as a failure in a terminal, and it drops gray, which is the rail's own color.
var plotSeries = []string{
	"blue", "orange", "lightgreen", "purple",
	"cyan", "yellow", "lightpurple", "green", "red",
}

// plotHue assigns each bar a swatch, so a plot is readable before anyone has
// colored anything.
func plotHue(i int) (int, int, int) {
	if r, g, b, ok := editor.NodeColorRGB(plotSeries[i%len(plotSeries)]); ok {
		return r, g, b
	}
	return 200, 200, 200
}

// plotLines draws the chart: a bar per child, and under each bar one icicle
// band per level of its subtree. Pure over the subtree and the space it is
// given, so both faces and the tests can call it.
func plotLines(n editor.NodeRef, rail string, maxLine int) []string {
	bars := n.Children()
	if len(bars) == 0 {
		return nil
	}
	theme := editor.NodeTheme()
	railW := len([]rune(rail))

	// widest label and value decide the two fixed columns; the bar field takes
	// what is left, capped so a wide window does not stretch three bars across it
	labelW, valW := 0, 0
	var max float64
	for _, b := range bars {
		if w := len([]rune(plotLabel(b))); w > labelW {
			labelW = w
		}
		v := plotValue(b)
		if w := len(strconv.FormatFloat(v, 'f', -1, 64)); w > valW {
			valW = w
		}
		if v > max {
			max = v
		}
	}
	barW := maxLine - railW - labelW - valW - 4
	if barW > plotMaxBarW {
		barW = plotMaxBarW
	}
	if barW < 8 || max <= 0 {
		return nil // too narrow to say anything true
	}

	cells := func(v float64) int {
		c := int(math.Round(v / max * float64(barW)))
		if c < 1 && v > 0 {
			c = 1
		}
		return c
	}

	var out []string
	for i, b := range bars {
		r, g, bl := plotHue(i)
		w := cells(plotValue(b))
		val := strconv.FormatFloat(plotValue(b), 'f', -1, 64)
		out = append(out, fmt.Sprintf("%s%s%-*s %s%s%s%s %s%*s%s",
			rail, theme.Dim, labelW, plotLabel(b), theme.Reset,
			plotFG(r, g, bl), strings.Repeat("█", w), theme.Reset,
			theme.Dim, valW+barW-w, val, theme.Reset))

		// one band per level below this bar, each cell inside its parent's span
		for d := 1; d <= plotDepth(b); d++ {
			var spans []plotSpan
			plotLevel(b, w, 0, d, 0, &spans)
			if len(spans) == 0 {
				break
			}
			var line strings.Builder
			line.WriteString(rail + strings.Repeat(" ", labelW+1))
			for _, s := range spans {
				cr, cg, cb := r, g, bl
				if s.sib%2 == 1 {
					cr, cg, cb = plotTint(r, g, bl, 0.62)
				}
				line.WriteString(plotBG(cr, cg, cb) + plotInk(cr, cg, cb) + plotFit(s.n, s.w))
			}
			line.WriteString(theme.Reset)
			out = append(out, line.String())
		}
	}
	return out
}
