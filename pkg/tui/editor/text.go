package editor

import (
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// This file holds the generic text/ANSI layout primitives — width, clip, wrap,
// tab-expansion, cluster and caret helpers. They are node-agnostic; the
// node-body and band rendering that uses them lives in render.go.

// capFirst upper-cases the first rune of s, leaving the rest untouched — for
// presenting a lowercase error string as "Error: Grok is not installed".
func capFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

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

// wrapSGR wraps a styled line to at most width visible columns per line,
// breaking at spaces where possible and carrying the active SGR state onto each
// continuation line so a color span survives the break. Used by the status bar,
// which wraps instead of truncating.
func wrapSGR(s string, width int) []string {
	if width <= 0 || visibleWidth(s) <= width {
		return []string{s}
	}
	var lines []string
	var cur strings.Builder
	curStyle := "" // SGR state carried onto the current line's start
	style := ""    // live SGR state as escapes are consumed
	w := 0
	spIdx, spStyle := -1, "" // byte offset in cur of the last breakable space + style there
	inEsc := false
	var esc strings.Builder
	for _, r := range s {
		if inEsc {
			esc.WriteRune(r)
			if r == 'm' {
				inEsc = false
				seq := esc.String()
				esc.Reset()
				cur.WriteString(seq)
				if seq == cReset {
					style = ""
				} else {
					style += seq
				}
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			esc.WriteRune(r)
			continue
		}
		rw := runewidth.RuneWidth(r)
		if w+rw > width {
			line, rest, restStyle := cur.String(), "", style
			if spIdx >= 0 {
				rest = line[spIdx+1:] // past the space
				line = line[:spIdx]
				restStyle = spStyle
			}
			lines = append(lines, curStyle+line+cReset)
			cur.Reset()
			cur.WriteString(rest)
			curStyle = restStyle
			w = visibleWidth(rest)
			spIdx, spStyle = -1, ""
			if r == ' ' {
				continue // the breaking space itself never leads the next line
			}
		}
		if r == ' ' && w > 0 {
			spIdx, spStyle = cur.Len(), style
		}
		cur.WriteRune(r)
		w += rw
	}
	if cur.Len() > 0 {
		lines = append(lines, curStyle+cur.String())
	}
	return lines
}

// selFill paints the multi-select background under an already-styled line,
// padded with spaces to width so the bar runs edge to edge. Real cells, not an
// \x1b[K flood: the renderer appends its own end-of-line erase after each
// line, which would repaint a flood back to the default background. Every
// reset inside the content re-arms the background so mixed-color rows keep
// the highlight; the frame wrapper's trailing cReset closes it.
func selFill(s string, width int) string {
	if pad := width - visibleWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return bgPill + strings.ReplaceAll(s, cReset, cReset+bgPill)
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
				continue // re-check the same cluster on the new line
			}
			if i > lineStart {
				emit(i)
				continue // re-check the same cluster on the new line
			}
			// a cluster wider than a whole line (a wide rune on a 1-cell line)
			// can never fit — consume it or the loop re-emits this start forever
			curWidth += rw
			i = clEnd
			continue
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
