package editor

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// This file holds the generic text/ANSI layout primitives — width, clip, wrap,
// tab-expansion, cluster and caret helpers. They are node-agnostic; the
// node-body and band rendering that uses them lives in render.go.

// ── ANSI escape scanning ───────────────────────────────────────────────────

// A rendered line carries TWO shapes of escape: SGR color (CSI, "\x1b[…m") and
// the OSC 8 hyperlink a URL link chip wraps itself in — oscLink(target) …
// oscLink("") — so a terminal can ctrl+click the chip (see renderBody).
//
// WARNING (invariant): every scanner that walks a styled line must skip BOTH
// through ansiEscapeEnd / escapeEndRunes. A hand-rolled "consume until 'm'"
// loop ends an OSC 8 escape inside its own target (the 'm' of ".com"), and the
// rest of the URL is then measured and printed as if it were text: rows wrap
// early, a continuation line re-emits the half-eaten opener, and the terminal
// swallows whatever follows it as OSC payload.

const (
	oscLinkPrefix = "\x1b]8;;" // OSC 8: prefix + target + ST; an empty target closes
	oscST         = "\x1b\\"   // string terminator
)

// oscLink is one OSC 8 hyperlink escape; oscLink("") closes the open link.
func oscLink(target string) string { return oscLinkPrefix + target + oscST }

// hyperlink wraps already-styled text in an OSC 8 hyperlink to url. Terminals
// without OSC 8 ignore the brackets and show the text alone.
func hyperlink(url, text string) string {
	if url == "" {
		return text
	}
	return oscLinkPrefix + url + oscST + text + oscLinkPrefix + oscST
}

// ansiEscapeEnd returns the byte index just after the escape beginning at s[i]
// (s[i] must be ESC). An unterminated escape runs to the end of s.
func ansiEscapeEnd(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[': // CSI: the final byte is anything in 0x40..0x7e ('m' for SGR)
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1
			}
		}
		return len(s)
	case ']': // OSC: BEL or ST ends the payload — never a bare 'm'
		for j := i + 2; j < len(s); j++ {
			if s[j] == '\a' {
				return j + 1
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	default:
		return i + 2
	}
}

// escapeEndRunes is ansiEscapeEnd in rune coordinates, for the wrappers that
// index a []rune. Escape syntax is ASCII, so the two scan the same bytes.
func escapeEndRunes(runes []rune, i int) int {
	if i+1 >= len(runes) {
		return len(runes)
	}
	switch runes[i+1] {
	case '[':
		for j := i + 2; j < len(runes); j++ {
			if runes[j] >= 0x40 && runes[j] <= 0x7e {
				return j + 1
			}
		}
		return len(runes)
	case ']':
		for j := i + 2; j < len(runes); j++ {
			if runes[j] == '\a' {
				return j + 1
			}
			if runes[j] == '\x1b' && j+1 < len(runes) && runes[j+1] == '\\' {
				return j + 2
			}
		}
		return len(runes)
	default:
		return i + 2
	}
}

// isSGR reports whether an escape sequence is a color/attribute one — the only
// kind a continuation line re-opens as carried style.
func isSGR(seq string) bool { return strings.HasPrefix(seq, "\x1b[") }

// linkTargetOf returns the target an OSC 8 escape opens ("" for the closing
// escape) and whether seq is an OSC 8 escape at all.
func linkTargetOf(seq string) (string, bool) {
	if !strings.HasPrefix(seq, oscLinkPrefix) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(seq, oscLinkPrefix), oscST), true
}

// visibleWidth returns the display width of s ignoring escape sequences. Runs of
// text between escapes are measured a grapheme cluster at a time so a ZWJ emoji
// sequence counts as its true terminal width — StringWidth folds the cluster's
// components into one cell-run — rather than the sum of its parts.
func visibleWidth(s string) int {
	if !strings.ContainsRune(s, '\x1b') {
		return runewidth.StringWidth(s)
	}
	w := 0
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			i = ansiEscapeEnd(s, i)
			continue
		}
		j := i
		for j < len(s) && s[j] != '\x1b' {
			j++
		}
		w += runewidth.StringWidth(s[i:j])
		i = j
	}
	return w
}

// clip truncates s (which may carry escape sequences) to the given display width.
func clip(s string, width int) string {
	if visibleWidth(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	linked := false
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			e := ansiEscapeEnd(s, i)
			seq := s[i:e]
			b.WriteString(seq)
			if strings.HasPrefix(seq, oscLinkPrefix) {
				linked = seq != oscLink("")
			}
			i = e
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		rw := runewidth.RuneWidth(r)
		if w+rw > width-1 {
			b.WriteString(cDim + "…")
			break
		}
		b.WriteString(s[i : i+n])
		w += rw
		i += n
	}
	if linked {
		b.WriteString(oscLink(""))
	}
	return b.String()
}

// windowLine returns the display columns [from, from+width) of a styled line,
// with a dim … marking each edge it cut. It is the horizontal viewport of a
// no-wrap row (see layoutRow): the rail and glyph are not part of s, so they
// stay put while the body slides under the caret. The SGR state and any OSC 8
// link open where the window starts are reopened at its head, so a color span
// or a link chip the window opens inside still renders as itself.
func windowLine(s string, from, width int) string {
	if width <= 0 {
		return ""
	}
	if from <= 0 {
		return clip(s, width)
	}
	var state []string // SGR sequences active since the last reset
	link := ""         // OSC 8 target open at the walk position
	col := 0
	i := 0
	for i < len(s) && col < from {
		if s[i] == '\x1b' {
			e := ansiEscapeEnd(s, i)
			seq := s[i:e]
			switch {
			case strings.HasPrefix(seq, oscLinkPrefix):
				link = strings.TrimSuffix(strings.TrimPrefix(seq, oscLinkPrefix), oscST)
			case seq == cReset:
				state = state[:0]
			default:
				state = append(state, seq)
			}
			i = e
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		col += runewidth.RuneWidth(r)
		i += n
	}
	head := cDim + "…" + cReset + strings.Join(state, "")
	rest := s[i:]
	if link != "" {
		head += oscLink(link)
		rest += oscLink("")
	}
	return head + clip(rest, width-1)
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
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			e := ansiEscapeEnd(s, i)
			seq := s[i:e]
			cur.WriteString(seq)
			// only color state is re-opened on a continuation line; an OSC
			// hyperlink passes through and stays open on its own.
			if isSGR(seq) {
				if seq == cReset {
					style = ""
				} else {
					style += seq
				}
			}
			i = e
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
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
				i += n
				continue // the breaking space itself never leads the next line
			}
		}
		if r == ' ' && w > 0 {
			spIdx, spStyle = cur.Len(), style
		}
		cur.WriteString(s[i : i+n])
		w += rw
		i += n
	}
	if cur.Len() > 0 {
		lines = append(lines, curStyle+cur.String())
	}
	return lines
}

// selFill paints the selection bar under an already-styled line, padded with
// spaces to width so it runs edge to edge. Real cells, not an \x1b[K flood: the
// renderer appends its own end-of-line erase after each line, which would
// repaint a flood back to the default background. The frame wrapper's trailing
// cReset closes it.
//
// The line's own COLORS are dropped as it goes under the bar (see selPaint):
// black on white is what "selected" looks like, and a row that kept its red or
// its yellow would read as a red stripe rather than as a selected row.
func selFill(s string, width int) string {
	bar := bgTextSel()
	s = bar + selPaint(s, bar)
	if pad := width - visibleWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// selPaint rewrites an already-styled line for the selection bar: every color
// instruction it carries becomes the bar itself, while bold, italic, underline
// and strike pass through — the row keeps its SHAPE and loses only the colors
// that would compete with the highlight.
func selPaint(s, bar string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\x1b' {
			b.WriteByte(s[i])
			i++
			continue
		}
		e := ansiEscapeEnd(s, i)
		seq := s[i:e]
		switch {
		case seq == cReset:
			b.WriteString(cReset + bar)
		case sgrIsColor(seq):
			b.WriteString(bar)
		default:
			b.WriteString(seq) // an attribute (or an OSC link) survives the bar
		}
		i = e
	}
	return b.String()
}

// sgrIsColor reports whether an escape sets a foreground or background color —
// the SGR parameters the selection bar takes over.
func sgrIsColor(seq string) bool {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return false
	}
	for _, p := range strings.Split(strings.TrimSuffix(seq[2:], "m"), ";") {
		switch n, err := strconv.Atoi(p); {
		case err != nil:
			continue
		case n >= 30 && n <= 49, n >= 90 && n <= 97, n >= 100 && n <= 107:
			return true
		}
	}
	return false
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

// oneLine flattens s to a single line — newline runs (and surrounding
// whitespace) collapse to one space — so a multi-paragraph node renders as one
// truncated picker row instead of wrapping the list.
func oneLine(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
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
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			e := ansiEscapeEnd(s, i)
			b.WriteString(s[i:e])
			i = e
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == '\t' {
			pad := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", pad))
			col += pad
		} else {
			b.WriteString(s[i : i+n])
			col += runewidth.RuneWidth(r)
		}
		i += n
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
	link := ""         // OSC 8 target open at the walk position ("" = none)
	lineStart := 0
	var startState []string // state at lineStart
	startLink := ""         // link open at lineStart
	curWidth := 0
	avail := width
	lastSpace := -1
	var lastSpaceState []string
	lastSpaceLink := ""

	// endLink is the OSC 8 target open where the line breaks. A hyperlink never
	// spans a wrap: it is closed at the break and reopened past the rail prefix,
	// so the dim indent is not swallowed into the link.
	emitLine := func(end int, cursorBreak bool, endLink string) {
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
		if endLink != "" {
			tail += oscLink("")
		}
		head := ""
		if len(lines) > 0 {
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
			head = prefix + cReset + strings.Join(carried, "")
		}
		if startLink != "" {
			head += oscLink(startLink) // reopen past the prefix, not around it
		}
		lines = append(lines, head+seg+tail+cReset)
	}

	i := 0
	for i < len(runes) {
		r := runes[i]
		if r == '\x1b' {
			e := escapeEndRunes(runes, i)
			seq := string(runes[i:e])
			switch {
			case isSGR(seq):
				if seq == cReset {
					state = state[:0]
				} else {
					state = append(state, seq)
				}
			default:
				// an OSC 8 hyperlink is not style state — it is carried across a
				// break as an open target, closed and reopened by emitLine.
				if target, ok := linkTargetOf(seq); ok {
					link = target
				}
			}
			i = e
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
				emitLine(i, hasInvert(state), link)
				lineStart = i + 1
				startState = append([]string(nil), state...)
				startLink = link
				curWidth = 0
				avail = width - hang
				lastSpace = -1
				i++
				continue
			}
			if lastSpace > lineStart {
				// break at the last space; what follows it moves down
				emitLine(lastSpace, hasInvert(lastSpaceState), lastSpaceLink)
				lineStart = lastSpace + 1
				startState = append([]string(nil), lastSpaceState...)
				startLink = lastSpaceLink
				curWidth = visibleWidth(string(runes[lineStart:i]))
			} else {
				// no space on this line: hard break before the cluster — never
				// in the middle of it.
				emitLine(i, false, link)
				lineStart = i
				startState = append([]string(nil), state...)
				startLink = link
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
			lastSpaceLink = link
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
		emitLine(len(runes), false, "") // renderBody always closes its own links
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
