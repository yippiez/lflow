package editor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// SGR attributes are universal — never themed. The color palette below is, and
// lives in vars set by the active theme (see theme.go); "system" reseeds these
// with the locked design-v4 values at startup.
const (
	cReset     = "\x1b[0m"
	cBold      = "\x1b[1m"
	cItalic    = "\x1b[3m"
	cUnderline = "\x1b[4m"
	cStrike    = "\x1b[9m"
	cInvert    = "\x1b[7m" // the block cursor: inverts the cell beneath it
	// cClearEOL erases from the cursor to the end of the line. Prefixed to every
	// emitted View line so a frame fully overwrites the previous one: the inline
	// renderer rewrites lines in place without clearing, so a grow after a shrink
	// would otherwise leave the prior narrower line's cells behind the new one. It
	// leads the line rather than trailing it so the renderer's width truncation,
	// which drops escape bytes past the cut, cannot discard it on full-width rows.
	cClearEOL = "\x1b[K"
)

// The themeable palette. These are vars (not consts) so /theme can reassign them
// at runtime via applyTheme. Seeded with the "system" theme in init().
var (
	cFG      = "\x1b[38;2;212;212;212m" // #d4d4d4
	cDim     = "\x1b[38;2;122;122;122m" // #7a7a7a
	cAccent  = "\x1b[38;2;86;156;214m"  // #569cd6
	cRed     = "\x1b[38;2;244;71;71m"   // #f44747
	cYellow  = "\x1b[38;2;220;220;170m" // #dcdcaa
	cGreen   = "\x1b[38;2;106;153;85m"  // #6a9955
	cMagenta = "\x1b[38;2;197;134;192m" // #c586c0
	cCyan    = "\x1b[38;2;78;201;176m"  // #4ec9b0
	bgCode   = "\x1b[48;2;31;31;31m"    // #1f1f1f block behind code rows
	bgTerm   = "\x1b[48;2;30;34;48m"    // #1e2230 terminal block behind bash rows
	bgPill   = "\x1b[48;2;38;79;120m"   // #264f78 behind date pills
)

// glyphs (locked)
const (
	glyphOpen      = "○"
	glyphCollapsed = "●"
	glyphMirror    = "◆"
	glyphTodo      = "□"
	glyphTodoDone  = "■"
	glyphQuoteBar  = "▎"
	glyphDotted    = "◌" // Temporary Domain nodes (ephemeral)
)

// visibleWidth returns the display width of s ignoring SGR sequences. Runs of
// text between escapes are measured a grapheme cluster at a time so a ZWJ emoji
// sequence counts as its true terminal width — StringWidth folds the cluster's
// components into one cell-run — rather than the sum of its parts.
func visibleWidth(s string) int {
	w := 0
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			w += runewidth.StringWidth(run.String())
			run.Reset()
		}
	}
	inEsc := false
	for _, r := range s {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			flush()
			inEsc = true
			continue
		}
		run.WriteRune(r)
	}
	flush()
	return w
}

// clip truncates s (which may contain SGR sequences) to the given display width.
func clip(s string, width int) string {
	if visibleWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			b.WriteRune(r)
			inEsc = true
			continue
		}
		rw := runewidth.RuneWidth(r)
		if w+rw > width-1 {
			b.WriteString(cDim + "…")
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

// elideMiddle shortens plain text s to at most width display cells, dropping the
// middle and joining the kept ends with a "…" marker so both the start and end
// of the name stay legible. s must carry no SGR sequences. When s already fits
// it is returned unchanged; when width is too small for the marker the head is
// truncated instead.
func elideMiddle(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width <= 1 {
		return runewidth.Truncate(s, width, "")
	}
	const marker = "…"
	budget := width - 1 // one cell for the marker
	head := budget - budget/2
	tail := budget / 2
	runes := []rune(s)
	var h strings.Builder
	w := 0
	for _, r := range runes {
		rw := runewidth.RuneWidth(r)
		if w+rw > head {
			break
		}
		h.WriteRune(r)
		w += rw
	}
	var t strings.Builder
	w = 0
	for i := len(runes) - 1; i >= 0; i-- {
		rw := runewidth.RuneWidth(runes[i])
		if w+rw > tail {
			break
		}
		t.WriteRune(runes[i])
		w += rw
	}
	tr := []rune(t.String())
	for i, j := 0, len(tr)-1; i < j; i, j = i+1, j-1 {
		tr[i], tr[j] = tr[j], tr[i]
	}
	return h.String() + marker + string(tr)
}

// clipStr truncates s to at most n runes, adding an ellipsis when it cuts.
func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// tabWidth is the terminal's hard tab stop: tabs advance to the next multiple.
const tabWidth = 8

// expandTabs replaces tabs with spaces up to the next tab stop, tracking the
// display column across the (possibly SGR-styled) line so each \t advances to
// the next multiple of tabWidth columns — exactly how the terminal expands
// them. runewidth measures '\t' as zero, so wrapLine must see expanded spaces
// to measure and wrap tab-laden lines correctly. SGR sequences pass through
// untouched and do not advance the column.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	col := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		switch {
		case r == '\x1b':
			b.WriteRune(r)
			inEsc = true
		case r == '\t':
			n := tabWidth - col%tabWidth
			for k := 0; k < n; k++ {
				b.WriteByte(' ')
			}
			col += n
		default:
			b.WriteRune(r)
			col += runewidth.RuneWidth(r)
		}
	}
	return b.String()
}

// firstCluster returns the first grapheme cluster at the start of runes (a run
// of text runes containing no escape) and how many runes it spans. A ZWJ emoji
// sequence such as a family emoji is a single cluster, so wrapLine advances over
// it as one unit — measured with StringWidth — instead of summing its component
// widths, which would overcount and wrap early.
func firstCluster(runes []rune) (string, int) {
	if len(runes) == 0 {
		return "", 0
	}
	cluster, _, _, _ := uniseg.FirstGraphemeClusterInString(string(runes), -1)
	return cluster, len([]rune(cluster))
}

// wrapLine soft-wraps an SGR-styled line to the given display width, breaking
// at spaces when one is available. Continuation lines carry the given prefix —
// a dim-styled hanging indent that keeps the tree rail continuous under the
// text column — and re-open the styles active at the break, so spans and the
// cursor cell survive the wrap.
func wrapLine(s string, width int, prefix string) []string {
	s = expandTabs(s)
	if width <= 0 || visibleWidth(s) <= width {
		return []string{s}
	}
	hang := visibleWidth(prefix)
	// bodyCol is the column the node text begins at — the glyph prefix width,
	// which matches the continuation indent. Spaces left of it (inside the glyph
	// prefix) must never be wrap candidates, or the bullet strands on its own
	// line while the text drops below. Keep this floor even when the pathological
	// guard zeroes the visible continuation indent, so the glyph line still fills
	// and the text renders rather than getting stranded or clipped away.
	bodyCol := hang
	if hang >= width {
		hang = 0    // pathological widths: the prefix leaves no room for text, so
		prefix = "" // give the continuation the whole line
	}
	runes := []rune(s)

	var lines []string
	var state []string // SGR sequences active since the last reset
	lineStart := 0
	var startState []string // state at lineStart
	curWidth := 0
	avail := width
	lastSpace := -1
	var lastSpaceState []string

	emitLine := func(end int, cursorBreak bool) {
		seg := string(runes[lineStart:end])
		// When the break consumes a space that carried the block cursor, the
		// inverted space rune lands on neither line — re-emit a single inverted
		// cell at the trailing edge of this line so the one-cell cursor stays
		// visible there, without leaking reverse-video onto the continuation.
		tail := ""
		if cursorBreak {
			// the cursor opener sits at the very end of seg with no cell after
			// it (the space it inverted was dropped); strip that dangling opener
			// and re-emit a single complete inverted cell in its place.
			seg = strings.TrimSuffix(seg, cInvert)
			tail = cInvert + " " + cReset
		}
		if len(lines) == 0 {
			lines = append(lines, seg+tail+cReset)
		} else {
			// The cursor cell is a single cell and never spans a wrap, so the
			// reverse-video sequence must never appear in a continuation
			// prefix — drop it from the carried state.
			carried := startState[:0:0]
			for _, sgr := range startState {
				if sgr == cInvert {
					continue
				}
				carried = append(carried, sgr)
			}
			lines = append(lines, prefix+cReset+strings.Join(carried, "")+seg+tail+cReset)
		}
	}

	i := 0
	for i < len(runes) {
		r := runes[i]
		if r == '\x1b' {
			j := i
			for j < len(runes) && runes[j] != 'm' {
				j++
			}
			if seq := string(runes[i:min(j+1, len(runes))]); seq == cReset {
				state = state[:0]
			} else {
				state = append(state, seq)
			}
			i = j + 1
			continue
		}

		// Advance by grapheme cluster, not single rune: a ZWJ emoji sequence
		// (e.g. a family emoji) is one terminal cell-run, so measure the whole
		// cluster with StringWidth rather than summing each component's width —
		// summing overcounts and forces a premature wrap. Clusters never start
		// with an escape (handled above) and never contain one, so we segment
		// the run of text runes up to the next escape.
		clEnd := i + 1
		for clEnd < len(runes) && runes[clEnd] != '\x1b' {
			clEnd++
		}
		_, clusterLen := firstCluster(runes[i:clEnd])
		clEnd = i + clusterLen
		rw := runewidth.StringWidth(string(runes[i:clEnd]))
		if curWidth+rw > avail {
			if r == ' ' {
				// the overflowing rune is itself a space: break right here
				emitLine(i, hasInvert(state))
				lineStart = i + 1
				startState = append([]string(nil), state...)
				curWidth = 0
				avail = width - hang
				lastSpace = -1
				i++
				continue
			}
			if lastSpace > lineStart {
				// break at the last space; what follows it moves down
				emitLine(lastSpace, hasInvert(lastSpaceState))
				lineStart = lastSpace + 1
				startState = append([]string(nil), lastSpaceState...)
				curWidth = visibleWidth(string(runes[lineStart:i]))
			} else {
				// no space on this line: hard break before the cluster — never
				// in the middle of it.
				emitLine(i, false)
				lineStart = i
				startState = append([]string(nil), state...)
				curWidth = 0
			}
			avail = width - hang
			lastSpace = -1
			continue // re-check the same cluster against the new line
		}

		if r == ' ' && curWidth >= bodyCol {
			// only spaces past the prefix/indent column are wrap candidates:
			// the glyph's trailing space must not strand the bullet on its own
			// line when the body is one long unbroken run.
			lastSpace = i
			lastSpaceState = append([]string(nil), state...)
		}
		curWidth += rw
		i = clEnd
	}
	// Don't open a final continuation line that would carry nothing but the
	// dim rail prefix. This happens when the only content past the last full
	// visual line is the trailing past-end cursor space: the overflow-space
	// break above already re-emitted that inverted cell on the trailing edge
	// of the last text line, so the trailing segment here holds no visible
	// runes — only leftover escape sequences — and would just add a blank
	// rail-only line below it.
	if len(lines) == 0 || visibleWidth(string(runes[lineStart:])) > 0 {
		emitLine(len(runes), false)
	}
	return lines
}

// hasInvert reports whether the reverse-video cursor sequence is active in the
// given SGR state — i.e. a broken-at space carried the block cursor.
func hasInvert(state []string) bool {
	for _, sgr := range state {
		if sgr == cInvert {
			return true
		}
	}
	return false
}

// glyphFor returns the bullet glyph and its color for an item. Bullets and
// todo boxes are muted gray — the selected row turns its glyph red. Glyphs
// with an identity keep their own color: ◆ mirrors red, heading digits
// yellow. Headings show their level digit instead of a circle: that is how
// h1/h2/h3 stay visible in a single-line wysiwyg row.
func glyphFor(it *item) (string, string) {
	if it.mirrorOf != "" {
		return glyphMirror, cDim // a mirror is the muted ◆ — the diamond marks it, red stays the cursor
	}
	if g := typeOf(it.typ).glyph; g != nil {
		return g(it) // per-type glyph (todo box, heading digit)
	}
	if len(it.children) > 0 && it.collapsed {
		return glyphCollapsed, cDim
	}
	return glyphOpen, cDim
}

// connector builds the tree-connector prefix for a row: │ continuation
// columns for ancestors with later siblings, then ├─ or ╰─ dropping from the
// parent's bullet column. Depth-0 bullets sit at column 0, so the depth-0
// ancestor contributes no continuation column.
func connector(r row) string {
	if r.depth == 0 {
		return ""
	}
	var b strings.Builder
	for i, hasMore := range r.branch {
		if i == 0 {
			continue // no column exists left of depth-0 bullets
		}
		if hasMore {
			b.WriteString("│  ")
		} else {
			b.WriteString("   ")
		}
	}
	if r.last {
		b.WriteString("╰─ ")
	} else {
		b.WriteString("├─ ")
	}
	return b.String()
}

// dividerLine renders a divider node as a single horizontal rule. The glyph
// (circle) is hidden: the rule is ~90% of the width available after the row's
// indent/rail, CENTERED in that space so equal gaps hang on the left and right.
// Muted gray normally, red under the cursor — the rule itself is the selection
// cue since there's no glyph.
func dividerLine(r row, maxLine int, selected bool) string {
	prefix := " " + cDim + connector(r)
	col := cDim
	if selected {
		col = cRed
	}
	avail := maxLine - visibleWidth(prefix) // content width after the indent/rail
	ruleW := avail * 24 / 25                // ~96%, a small centered gap each side
	if ruleW < 1 {
		ruleW = 1
	}
	leftGap := (avail - ruleW) / 2
	if leftGap < 0 {
		leftGap = 0
	}
	return prefix + cReset + strings.Repeat(" ", leftGap) + col + strings.Repeat("─", ruleW) + cReset
}

// continuationPrefix builds the dim-styled hanging indent for a row's wrapped
// continuation lines. It keeps the tree rail continuous: a │ sits in every
// ancestor column that has a later sibling, in the node's own branch column
// when the node itself has a later sibling, and under the glyph when the
// subtree continues below (the node has a visible child). Columns are laid
// out as 1 margin + 3 per depth level + 2 for the glyph and its space.
func continuationPrefix(r row, subtreeBelow bool) string {
	width := 1 + 3*r.depth + 2
	cells := make([]rune, width)
	for i := range cells {
		cells[i] = ' '
	}
	// ancestor columns: branch[i] for i in 1..depth-1 (i==0 has no column,
	// depth-0 bullets sit at column 0).
	for i := 1; i < r.depth; i++ {
		if r.branch[i] {
			cells[1+3*(i-1)] = '│'
		}
	}
	// the node's own branch column, when it has a later sibling.
	if r.depth > 0 && !r.last {
		cells[1+3*(r.depth-1)] = '│'
	}
	// the glyph column, when the subtree continues below.
	if subtreeBelow {
		cells[1+3*r.depth] = '│'
	}
	return cDim + string(cells)
}

// styleOutLine renders one captured output line. If the program emitted its own
// ANSI color (a SGR escape is present), it is passed through faithfully so the
// command's colors survive; an uncolored line falls back to muted gray, stderr
// red. A trailing reset guards against an unterminated sequence bleeding out.
func styleOutLine(l outLine) string {
	if strings.ContainsRune(l.text, '\x1b') {
		return l.text + cReset
	}
	col := cDim
	if l.err {
		col = cRed
	}
	return col + l.text + cReset
}

// runBandLines renders a bash node's run output beneath it: stdout in the normal
// color, stderr red, capped to the last few lines, with a running indicator. The
// band is hydrated from its on-disk cache on first render (see runout.go) so it
// survives a restart, but it never enters the DB or sync.
func (m *Model) runBandLines(r row, subtreeBelow bool, maxLine int) []string {
	uuid := r.it.uuid
	m.ensureRunOutLoaded(uuid)
	out := m.runOut[uuid]
	_, running := m.runCancel[uuid]
	if len(out) == 0 && !running {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	var lines []string
	shown := out
	const capN = 3
	if len(shown) > capN {
		lines = append(lines, clip(rail+cReset+cDim+fmt.Sprintf("  ⋯ %d more", len(shown)-capN)+cReset, maxLine))
		shown = shown[len(shown)-capN:]
	}
	for _, l := range shown {
		lines = append(lines, clip(rail+cReset+"  "+styleOutLine(l), maxLine))
	}
	if running {
		lines = append(lines, clip(rail+cReset+cDim+"  running…"+cReset, maxLine))
	}
	return lines
}

// noteBandLines renders a node's note as a muted, background-tinted band that
// hangs under the node, in the child-indent region. It reuses the row's
// continuation rail so the tree line runs down the band's left edge and curves
// into the children below (subtreeBelow draws the │ under the glyph column when
// the node has visible children). The band is sized to its widest wrapped line
// so it reads as a clean panel — clearly content, never another node.
//
// caret < 0 renders the band read-only (whitespace tidied for display). caret
// >= 0 makes the band the editing surface for the note: the exact text is kept
// so offsets line up, and a block cursor is drawn at caret. Returns nil only
// when there is no note and we are not editing.
func (m *Model) noteBandLines(r row, maxLine int, subtreeBelow bool, caret int) []string {
	note := stripControlBytes(m.tree.displayNote(r.it))
	editing := caret >= 0
	if !editing {
		note = strings.TrimSpace(note)
		if note == "" {
			return nil
		}
	}
	rail := continuationPrefix(r, subtreeBelow)
	railW := 1 + 3*r.depth + 2
	textW := maxLine - railW - 2 // room inside the band, minus a space of pad each side
	if textW < 8 {
		textW = 8
	}
	style := cDim + cItalic

	if !editing {
		segs := wrapPlain(note, textW)
		if len(segs) == 0 {
			return nil
		}
		bandW := 0
		for _, s := range segs {
			if w := runewidth.StringWidth(s); w > bandW {
				bandW = w
			}
		}
		var out []string
		for _, seg := range segs {
			gap := strings.Repeat(" ", bandW-runewidth.StringWidth(seg))
			out = append(out, rail+cReset+style+" "+seg+gap+" "+cReset)
		}
		return out
	}

	runes := []rune(note)
	segs := wrapNoteSegs(runes, textW)
	bandW := 1
	for _, s := range segs {
		if w := runewidth.StringWidth(string(runes[s.start:s.end])); w > bandW {
			bandW = w
		}
	}
	var out []string
	for idx, s := range segs {
		seg := runes[s.start:s.end]
		caretInSeg := -1
		if caret >= s.start && caret < s.end {
			caretInSeg = caret - s.start
		} else if caret >= len(runes) && idx == len(segs)-1 {
			caretInSeg = len(seg) // the block cursor sits past the last rune
		}
		out = append(out, rail+cReset+style+renderBandSeg(seg, caretInSeg, bandW, style)+cReset)
	}
	return out
}

// renderBandSeg renders one wrapped note segment's inner content, side-padded to
// bandW columns. It inverts the cell at caretInSeg for the block cursor and
// re-asserts the band style afterwards; caretInSeg < 0 draws no cursor, and
// caretInSeg == len(seg) draws a trailing cursor cell past the text.
func renderBandSeg(seg []rune, caretInSeg, bandW int, style string) string {
	var b strings.Builder
	b.WriteString(" ")
	w := 0
	for i, r := range seg {
		if i == caretInSeg {
			b.WriteString(cInvert + string(r) + cReset + style)
		} else {
			b.WriteString(string(r))
		}
		w += runewidth.RuneWidth(r)
	}
	if caretInSeg == len(seg) {
		b.WriteString(cInvert + " " + cReset + style)
		w++
	}
	if w < bandW {
		b.WriteString(strings.Repeat(" ", bandW-w))
	}
	b.WriteString(" ")
	return b.String()
}

// bandSeg is a [start,end) rune range of one wrapped note line.
type bandSeg struct{ start, end int }

// wrapNoteSegs splits runes into wrapped segments fitting width, breaking at the
// last space before the limit when possible, hard-breaking an over-long word,
// and honoring explicit newlines. Unlike wrapPlain it preserves exact offsets so
// the editing band can map the caret back to the text.
func wrapNoteSegs(runes []rune, width int) []bandSeg {
	if width < 1 {
		width = 1
	}
	n := len(runes)
	if n == 0 {
		return []bandSeg{{0, 0}}
	}
	var segs []bandSeg
	i := 0
	for i < n {
		col, j, lastSpace := 0, i, -1
		for j < n && runes[j] != '\n' {
			rw := runewidth.RuneWidth(runes[j])
			if col+rw > width {
				break
			}
			if runes[j] == ' ' {
				lastSpace = j
			}
			col += rw
			j++
		}
		end := j
		if j < n && runes[j] != '\n' && lastSpace > i {
			end = lastSpace + 1
		}
		if end == i {
			end = i + 1 // always make progress
		}
		segs = append(segs, bandSeg{i, end})
		i = end
		if i < n && runes[i] == '\n' {
			i++
			if i == n {
				segs = append(segs, bandSeg{n, n}) // a blank line after a trailing newline
			}
		}
	}
	return segs
}

// wrapPlain word-wraps plain text to a display width, breaking on spaces and
// hard-breaking any single word too long to fit. Explicit newlines start a new
// line. It is the note band's wrapper; the outline body uses wrapLine instead,
// which carries SGR state across breaks.
func wrapPlain(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			for runewidth.StringWidth(w) > width {
				head := cutToWidth(w, width)
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, head)
				w = string([]rune(w)[len([]rune(head)):])
			}
			cand := w
			if line != "" {
				cand = line + " " + w
			}
			if runewidth.StringWidth(cand) > width {
				out = append(out, line)
				line = w
			} else {
				line = cand
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// cutToWidth returns the longest prefix of s whose display width fits in width.
func cutToWidth(s string, width int) string {
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > width {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

// visualRows splits a node's plain name into the rune offsets at which each
// soft-wrapped visual line begins, mirroring wrapLine's break logic so the
// caret can be mapped to the same visual layout the renderer shows. firstCol
// is the display column the body starts at (the glyph prefix width) and hang
// is the continuation indent width. The returned slice always starts with 0;
// its length is the number of visual lines the name occupies.
func visualRows(name string, width, firstCol, hang int) []int {
	starts := []int{0}
	runes := []rune(name)
	if width <= 0 {
		return starts
	}
	// match wrapLine's clamp: when the prefix leaves no room for text it gives
	// the text the whole line, but keep the original indent as the wrap-candidate
	// floor so the body fills the glyph line before breaking, exactly as wrapLine.
	bodyCol := hang
	if hang >= width {
		hang = 0
	}
	if firstCol >= width {
		firstCol = 0
	}

	lineStart := 0
	curWidth := firstCol // the body begins after the glyph prefix
	avail := width
	lastSpace := -1

	emit := func(start int) {
		starts = append(starts, start)
		lineStart = start
		curWidth = 0
		avail = width - hang
		lastSpace = -1
	}

	i := 0
	for i < len(runes) {
		r := runes[i]
		// advance by grapheme cluster, measured as one width unit — mirror
		// wrapLine so the caret maps to the same break points (a ZWJ emoji is
		// one cluster, never split, never overcounted).
		_, clusterLen := firstCluster(runes[i:])
		clEnd := i + clusterLen
		rw := runewidth.StringWidth(string(runes[i:clEnd]))
		if curWidth+rw > avail {
			if r == ' ' {
				emit(i + 1)
				i++
				continue
			}
			if lastSpace > lineStart {
				next := lastSpace + 1
				emit(next)
				curWidth = visibleWidth(string(runes[next:i]))
			} else {
				emit(i)
			}
			continue // re-check the same cluster on the new line
		}
		if r == ' ' && curWidth >= bodyCol {
			lastSpace = i
		}
		curWidth += rw
		i = clEnd
	}
	return starts
}

// caretVisualLine returns which visual line of the wrapped name the caret sits
// on, given the line-start offsets from visualRows.
func caretVisualLine(starts []int, caret int) int {
	line := 0
	for j, s := range starts {
		if caret >= s {
			line = j
		}
	}
	return line
}

// withCaret marks the rune at the caret index with the inverting block
// cursor; past the end it paints a single trailing cell.
func withCaret(text string, caret int) string {
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	if caret >= len(runes) {
		return string(runes) + cInvert + " " + cReset + cFG
	}
	return string(runes[:caret]) + cInvert + string(runes[caret]) + cReset + cFG + string(runes[caret+1:])
}

// stripControlBytes removes every C0 control byte (0x00-0x1F) and DEL (0x7F)
// from content before it is emitted to the terminal. Input is sanitized the
// same way on the way in, but this is the render-boundary defense in depth: a
// legacy or crafted name or note already in the DB carrying a raw ESC[2J or
// ESC[H must render as inert text, never execute as a clear-screen or
// cursor-home. lflow's own SGR styling is added after this strip, so it stays
// intact — only control bytes that originate from stored content are dropped.
func stripControlBytes(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7F }) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, s)
}

// spanFlags is the per-rune mask the renderer uses. Text styling (bold, italic,
// underline, color) is a per-node attribute — see item.style — not inline
// markup, so no syntax markers leak into the stored name, search or export.
// Dates carry no markers either: the renderer recognises the canonical date
// format in the plain text and chips those runes.
type spanFlags struct {
	date    bool   // part of a canonical YYYY-MM-DD[ HH:MM] date, painted as a chip
	tag     bool   // part of a #tag, always painted muted gray
	mute    bool   // forced muted gray (log node's "· description" tail)
	kwColor string // animated magic-keyword foreground (ultracode/ultraloop), "" = none
}

// inlineSpans marks the runes inside a canonical date or a #tag so renderBody
// can paint them specially. Detection is purely by format — the stored text has
// no brackets or markers.
func inlineSpans(runes []rune) []spanFlags {
	flags := make([]spanFlags, len(runes))
	name := string(runes)
	for _, span := range detectDateSpans(name) {
		for k := span[0]; k < span[1] && k < len(flags); k++ {
			flags[k].date = true
		}
	}
	for _, span := range detectTagSpans(name) {
		for k := span[0]; k < span[1] && k < len(flags); k++ {
			flags[k].tag = true
		}
	}
	return flags
}

// renderBody renders a node name wysiwyg. Text keeps its normal color on
// every row — selection is carried by the red glyph alone. Unselected rows
// hide the markdown markers; the selected row shows them and the block
// cursor inverts the cell under the rune at the caret index (-1 for none).
// logTime formats a node's creation time for the log chip ("2026-06-24 14:11");
// a zero timestamp (a brand-new unsaved node) falls back to now.
func logTime(addedOn int64) string {
	t := time.Now()
	if addedOn > 0 {
		t = time.Unix(0, addedOn)
	}
	return t.Format("2006-01-02 15:04")
}

// logDescStart returns the rune index of the " · " separating a log line's label
// from its description, or -1 when there is none.
func logDescStart(runes []rune) int {
	for i := 0; i+2 < len(runes); i++ {
		if runes[i] == ' ' && runes[i+1] == '·' && runes[i+2] == ' ' {
			return i
		}
	}
	return -1
}

func renderBody(it *item, name string, caret int, selected bool, chips map[string]database.Chip) string {
	name = stripControlBytes(name)
	if r := typeOf(it.typ).render; r != nil {
		return r(it, name) // per-type inline-body override (json preview)
	}
	base := cFG
	if it.typ == database.TypeLog {
		base = cDim // a log line is muted gray by default; /color tints the label + →
	}
	// a /color picks the node's foreground; default stays the palette gray
	if c := styleBaseColor(it.style); c != "" {
		base = c
	}

	attrs := ""
	prefix := ""
	switch it.typ {
	case database.TypeH1, database.TypeH2, database.TypeH3:
		attrs += cBold
	case database.TypeQuote:
		attrs += cItalic
		prefix = cAccent + glyphQuoteBar + cReset + " "
	case database.TypeCode:
		attrs += bgCode
	case database.TypeBash:
		attrs += bgTerm // dark terminal block; the "$ " prompt is folded into the pad
	case database.TypeLog:
		prefix = cDim + "(" + logTime(it.addedOn) + ") " + cReset // muted time chip
	}
	if s := typeOf(it.typ).sign; s != "" && it.typ != database.TypeBash {
		prefix = cDim + s + cReset // type sign, e.g. "⌕ " for query
	}
	// /bold, /italic, /underline layer on top of the layout's own attributes
	attrs += styleAttrs(it.style)
	if it.completedAt > 0 {
		attrs += cStrike
	}

	runes := []rune(name)
	flags := inlineSpans(runes)
	markKeywords(runes, flags, animFrame) // ultracode/ultraloop: render-time only
	chipsp := anchorSpans(runes)          // inline chip anchors, drawn collapsed
	if it.typ == database.TypeLog {
		// everything from the first " · " on is the muted description
		if d := logDescStart(runes); d >= 0 {
			for k := d; k < len(runes); k++ {
				flags[k].mute = true
			}
		}
	}

	sgr := func(f spanFlags) string {
		fg := base
		if f.mute {
			fg = cDim
		}
		// a magic keyword paints its runes with the animated color, replacing the
		// node's foreground for those cells only.
		if f.kwColor != "" {
			fg = f.kwColor
		}
		// #tags and date chips are structural tokens with a fixed color, so a
		// node's /color never bleeds into them: tags stay muted gray, date chips
		// keep a neutral foreground on their pill.
		if f.tag {
			fg = cDim
		}
		if f.date {
			fg = cFG
		}
		s := cReset + fg + attrs
		if f.date {
			s += bgPill
		}
		return s
	}

	var b strings.Builder
	b.WriteString(prefix)
	cur := ""
	switch it.typ {
	case database.TypeCode:
		b.WriteString(cReset + attrs + " ") // pad the code block
	case database.TypeBash:
		b.WriteString(cReset + attrs + cDim + "$ " + cReset + attrs) // prompt on the tint
	}
	for i := 0; i < len(runes); {
		// a chip anchor renders collapsed: the chip kind's color + compact display,
		// atomic. The caret only ever sits at its boundaries (see snapCaret).
		if sp := spanStartingAt(chipsp, i); sp != nil {
			col := cCyan
			if c, ok := chips[sp.id]; ok {
				if k, ok := chipKindOf(c.Kind); ok {
					col = k.color
				}
			}
			b.WriteString(cReset + col)
			if caret == sp.start {
				b.WriteString(cInvert) // cursor sits on the whole chip
			}
			b.WriteString(dispByID(sp.id, chips))
			b.WriteString(cReset)
			cur = ""
			i = sp.end
			continue
		}
		r := runes[i]
		f := flags[i]
		if i == caret {
			// the block cursor sits ON the rune: same colors, dark red cell
			b.WriteString(sgr(f) + cInvert)
			b.WriteRune(r)
			cur = "" // force a state re-emit after the caret cell
			i++
			continue
		}
		if s := sgr(f); s != cur {
			b.WriteString(s)
			cur = s
		}
		b.WriteRune(r)
		i++
	}
	if caret >= len(runes) && caret >= 0 {
		// past the last rune: paint one trailing cell
		b.WriteString(cReset + cFG + cInvert + " ")
	}
	if it.typ == database.TypeCode || it.typ == database.TypeBash {
		b.WriteString(cReset + attrs + " ")
	}
	b.WriteString(cReset)
	return b.String()
}

// renderJSONPreview renders a json node as a one-line entry: a {} marker plus a
// whitespace-collapsed, truncated preview of the JSON. Invalid JSON turns the {}
// marker red and appends a red " · JSON parsing failed". Editing happens only in
// the alt+e editor, so this is never an inline edit surface.
func renderJSONPreview(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return cDim + "{}" + cReset + " " + cDim + "empty" + cReset
	}
	if json.Valid([]byte(trimmed)) {
		return cDim + "{}" + cReset + " " + cFG + jsonPreview(name, 50) + cReset
	}
	return cRed + "{}" + cReset + " " + cFG + jsonPreview(name, 50) + cReset +
		cRed + " · JSON parsing failed" + cReset
}

// jsonPreview collapses whitespace and truncates to n display runes.
func jsonPreview(s string, n int) string {
	one := strings.Join(strings.Fields(s), " ")
	r := []rune(one)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return one
}

// typeSuffix returns a dim suffix describing non-default state. The note is no
// longer flagged here — it shows in full as a tinted band under the node (see
// noteBandLines) — so the suffix only carries mirror and collapsed-child counts.
// relTime renders a coarse "how long ago" for a unix-seconds timestamp.
func relTime(ts int64) string {
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	}
}

func (m *Model) typeSuffix(it *item) string {
	var parts []string
	if it.mirrorOf != "" {
		parts = append(parts, "mirror")
	}
	if it.readonly {
		parts = append(parts, "locked")
	}
	if it.typ == database.TypeQuery {
		parts = append(parts, fmt.Sprintf("%d hits", queryHitCount(it)))
		if ts := m.queryUpdatedAt(it.uuid); ts > 0 {
			parts = append(parts, "updated "+relTime(ts))
		}
	}
	if kids := m.tree.childItems(it); len(kids) > 0 && it.collapsed {
		noun := "children"
		if len(kids) == 1 {
			noun = "child"
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(kids), noun))
	}
	suffix := ""
	if len(parts) > 0 {
		suffix = cDim + " · " + strings.Join(parts, " · ") + cReset
	}
	if it.linkTo != "" { // → linked node, muted gray, on the right (alt+g jumps)
		suffix += cDim + "  → " + clipStr(m.linkName(it), 28) + cReset
	}
	return suffix
}

// linkName resolves the display name of a node's link target.
func (m *Model) linkName(it *item) string {
	if it.linkTo == "" {
		return ""
	}
	if t, ok := m.tree.byUUID[it.linkTo]; ok {
		return m.tree.displayName(t)
	}
	if n := m.tree.externalNames[it.linkTo]; n != "" {
		return n
	}
	if n, err := database.GetNode(m.db, it.linkTo); err == nil && n.Name != "" {
		return n.Name
	}
	return "(missing)"
}
