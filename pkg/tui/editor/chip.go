package editor

import (
	"path/filepath"
	"strings"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/utils"
	"github.com/mattn/go-runewidth"
)

// A chip is an inline structured token. The node's stored name carries an opaque
// ANCHOR — a sentinel-delimited chip id, "￼<id>￼" — and the chip's real
// data lives in the chips table (see pkg/tui/database/chip.go). The editor treats
// each anchor as one atomic unit (the caret jumps it, backspace deletes it whole)
// and renders it as the chip kind's compact display.
//
// WARNING (invariant): chips are the one place a node's name is NOT plain text —
// an anchor is a marker. Every surface that reads a name (render, CLI, export,
// search, bash run) resolves anchors through the chip store first.

const chipSentinel = '￼' // OBJECT REPLACEMENT CHARACTER — opens and closes an anchor

// chipAnchor builds the in-text anchor for a chip id.
func chipAnchor(id string) string {
	return string(chipSentinel) + id + string(chipSentinel)
}

// anchorSpan is one anchor's rune range [start,end) (both sentinels included)
// and the chip id it carries.
type anchorSpan struct {
	start, end int
	id         string
}

// anchorSpans returns every well-formed anchor in runes, in order.
func anchorSpans(runes []rune) []anchorSpan {
	var spans []anchorSpan
	for i := 0; i < len(runes); i++ {
		if runes[i] != chipSentinel {
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] != chipSentinel {
			j++
		}
		if j >= len(runes) {
			break // unterminated anchor: ignore the trailing sentinel
		}
		spans = append(spans, anchorSpan{start: i, end: j + 1, id: string(runes[i+1 : j])})
		i = j
	}
	return spans
}

// hasAnchor reports whether name contains any chip anchor — the guard that keeps
// the chip-aware render/caret paths off every chipless node.
func hasAnchor(name string) bool {
	return strings.ContainsRune(name, chipSentinel)
}

func isPathSpace(r rune) bool { return r == ' ' || r == '\t' }

// chipTokenAt finds the in-progress "@..." completion token under the caret: an
// "@" at a word boundary, running to the next whitespace (or anchor). It returns
// the token's [start,end) rune range and whether one was found. This is the plain
// text being typed before it becomes a committed chip anchor.
func chipTokenAt(runes []rune, caret int) (int, int, bool) {
	start := -1
	for i := caret - 1; i >= 0; i-- {
		if isPathSpace(runes[i]) || runes[i] == chipSentinel {
			break
		}
		if runes[i] == '@' && (i == 0 || isPathSpace(runes[i-1]) || runes[i-1] == chipSentinel) {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	end := start + 1
	for end < len(runes) && !isPathSpace(runes[end]) && runes[end] != chipSentinel {
		end++
	}
	return start, end, true
}

// spanStartingAt returns the anchor beginning exactly at rune index i, or nil.
func spanStartingAt(spans []anchorSpan, i int) *anchorSpan {
	for k := range spans {
		if spans[k].start == i {
			return &spans[k]
		}
	}
	return nil
}

// spanContaining returns the anchor whose interior strictly contains i, or nil —
// used to keep the caret off an anchor's interior.
func spanContaining(spans []anchorSpan, i int) *anchorSpan {
	for k := range spans {
		if i > spans[k].start && i < spans[k].end {
			return &spans[k]
		}
	}
	return nil
}

// spanEndingAt returns the anchor whose end is exactly i, or nil (backspace target).
func spanEndingAt(spans []anchorSpan, i int) *anchorSpan {
	for k := range spans {
		if spans[k].end == i {
			return &spans[k]
		}
	}
	return nil
}

// ── chip-kind registry ─────────────────────────────────────────────────────

// chipKind declares how one kind of chip behaves: its color, its compact display
// form, and its expansion to the full underlying value (for bash, CLI and search).
type chipKind struct {
	key     string
	color   string
	display func(value string) string // compact form, e.g. "@readme.txt"
	expand  func(value string) string // full value, e.g. the absolute path
}

const (
	chipKindPath = "path"
	chipKindTag  = "tag"
	chipKindDate = "date"
)

var chipKinds = map[string]chipKind{
	chipKindPath: {
		key:   chipKindPath,
		color: cCyan,
		display: func(v string) string {
			base := filepath.Base(v)
			if base == "" || base == "." || base == string(filepath.Separator) {
				base = v
			}
			return "@" + base
		},
		expand: func(v string) string { return v },
	},
	// tag/date kinds make the chip model uniform (see the chip-kind design). Their
	// display equals their value, so nothing is hidden; legacy plain-text #tags and
	// dates still render via inlineSpans until they are backfilled into chips.
	chipKindTag: {
		key:     chipKindTag,
		color:   cDim,
		display: func(v string) string { return "#" + v },
		expand:  func(v string) string { return "#" + v },
	},
	chipKindDate: {
		key:     chipKindDate,
		color:   bgPill + cFG, // the date pill, matching legacy date rendering
		display: func(v string) string { return v },
		expand:  func(v string) string { return v },
	},
}

func chipKindOf(kind string) (chipKind, bool) {
	k, ok := chipKinds[kind]
	return k, ok
}

// chipDisplay returns the compact display string for a chip record.
func chipDisplay(c database.Chip) string {
	if k, ok := chipKindOf(c.Kind); ok && k.display != nil {
		return k.display(c.Value)
	}
	return c.Value
}

// chipExpand returns the full underlying value for a chip record.
func chipExpand(c database.Chip) string {
	if k, ok := chipKindOf(c.Kind); ok && k.expand != nil {
		return k.expand(c.Value)
	}
	return c.Value
}

// chipifyBeforeCaret converts a #tag or canonical date ending exactly at the
// caret into a chip anchor — called when a token is committed (a space typed, or
// Enter). It reuses the same detection that renders legacy tags/dates, so there
// are no new false positives. Returns true if it converted something.
func (m *Model) chipifyBeforeCaret(cur *item) bool {
	if cur == nil || cur.mirrorOf != "" || !typeOf(cur.typ).inlineEditable || cur.readonly {
		return false
	}
	name := cur.name
	runes := []rune(name)
	for _, sp := range detectTagSpans(name) {
		if sp[1] == m.caret {
			val := strings.TrimPrefix(string(runes[sp[0]:sp[1]]), "#")
			return m.replaceRangeWithChip(cur, sp[0], sp[1], chipKindTag, val)
		}
	}
	for _, sp := range detectDateSpans(name) {
		if sp[1] == m.caret {
			return m.replaceRangeWithChip(cur, sp[0], sp[1], chipKindDate, string(runes[sp[0]:sp[1]]))
		}
	}
	return false
}

// replaceRangeWithChip swaps runes[start:end) for a new chip anchor of the given
// kind/value and parks the caret just after it.
func (m *Model) replaceRangeWithChip(cur *item, start, end int, kind, value string) bool {
	anchor := m.createChip(kind, value)
	if anchor == "" {
		return false
	}
	runes := []rune(cur.name)
	cur.name = string(runes[:start]) + anchor + string(runes[end:])
	m.caret = start + len([]rune(anchor))
	m.unsaved = true
	return true
}

// ── Model chip store ───────────────────────────────────────────────────────

// createChip records a new chip (kind + value) and returns the in-text anchor to
// splice into a node name. The record is written through to the DB at once so it
// survives even before the node is saved (the anchor in the name pins it).
func (m *Model) createChip(kind, value string) string {
	id, err := utils.GenerateUUID()
	if err != nil {
		return ""
	}
	c := database.Chip{ID: id, Kind: kind, Value: value}
	if m.chips == nil {
		m.chips = map[string]database.Chip{}
	}
	m.chips[id] = c
	if m.ctx.DB != nil {
		_ = database.UpsertChip(m.ctx.DB, c)
	}
	return chipAnchor(id)
}

// deleteChipID drops a chip record (in-memory and on disk). The caller removes
// its anchor from the node name.
func (m *Model) deleteChipID(id string) {
	delete(m.chips, id)
	if m.ctx.DB != nil {
		_ = database.DeleteChip(m.ctx.DB, id)
	}
}

// ── resolving anchors in a name ────────────────────────────────────────────

// dispByID returns an anchor's display string from the chip store, or a dim
// placeholder when the record is missing (orphaned anchor).
func dispByID(id string, chips map[string]database.Chip) string {
	if c, ok := chips[id]; ok {
		return chipDisplay(c)
	}
	return "@?"
}

// expandAnchors replaces every anchor in name with the chip's full value — used
// for bash runs and machine-readable surfaces.
func expandAnchors(name string, chips map[string]database.Chip) string {
	if !hasAnchor(name) {
		return name
	}
	runes := []rune(name)
	var b strings.Builder
	i := 0
	for _, sp := range anchorSpans(runes) {
		b.WriteString(string(runes[i:sp.start]))
		if c, ok := chips[sp.id]; ok {
			b.WriteString(chipExpand(c))
		}
		i = sp.end
	}
	b.WriteString(string(runes[i:]))
	return b.String()
}

// displayAnchors replaces every anchor in name with the chip's compact display —
// used for human-readable surfaces outside the editor (CLI list/grep).
func displayAnchors(name string, chips map[string]database.Chip) string {
	if !hasAnchor(name) {
		return name
	}
	runes := []rune(name)
	var b strings.Builder
	i := 0
	for _, sp := range anchorSpans(runes) {
		b.WriteString(string(runes[i:sp.start]))
		b.WriteString(dispByID(sp.id, chips))
		i = sp.end
	}
	b.WriteString(string(runes[i:]))
	return b.String()
}

// ── chip-aware visual layout ───────────────────────────────────────────────
// These mirror visualRows / caretColumn / caretAtColumn but treat each anchor as
// one atomic cluster whose width is its chip display width, so a collapsed chip
// occupies the right columns and never splits across a wrap.

func anchorWidth(sp anchorSpan, chips map[string]database.Chip) int {
	return visibleWidth(dispByID(sp.id, chips))
}

// chipDispWidth returns the display width of runes[start:end] with anchors collapsed.
func chipDispWidth(runes []rune, start, end int, spans []anchorSpan, chips map[string]database.Chip) int {
	w, seg := 0, start
	flush := func(to int) {
		if to > seg {
			w += visibleWidth(string(runes[seg:to]))
		}
	}
	for i := start; i < end; {
		if sp := spanStartingAt(spans, i); sp != nil && sp.end <= end {
			flush(i)
			w += anchorWidth(*sp, chips)
			i = sp.end
			seg = i
			continue
		}
		i++
	}
	flush(end)
	return w
}

// chipVisualRows is visualRows with anchors as atomic clusters; it returns the
// stored rune offset that begins each visual line.
func chipVisualRows(name string, width, firstCol, hang int, chips map[string]database.Chip) []int {
	starts := []int{0}
	runes := []rune(name)
	if width <= 0 {
		return starts
	}
	spans := anchorSpans(runes)
	bodyCol := hang
	if hang >= width {
		hang = 0
	}
	if firstCol >= width {
		firstCol = 0
	}
	lineStart := 0
	curWidth := firstCol
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
		var clEnd, rw int
		isAnchor := false
		if sp := spanStartingAt(spans, i); sp != nil {
			clEnd, rw, isAnchor = sp.end, anchorWidth(*sp, chips), true
		} else {
			_, cl := firstCluster(runes[i:])
			clEnd = i + cl
			rw = runewidth.StringWidth(string(runes[i:clEnd]))
		}
		r := runes[i]
		if curWidth+rw > avail {
			if r == ' ' && !isAnchor {
				emit(i + 1)
				i++
				continue
			}
			if lastSpace > lineStart {
				next := lastSpace + 1
				emit(next)
				curWidth = chipDispWidth(runes, next, i, spans, chips)
			} else {
				emit(i)
			}
			continue
		}
		if r == ' ' && !isAnchor && curWidth >= bodyCol {
			lastSpace = i
		}
		curWidth += rw
		i = clEnd
	}
	return starts
}

// chipCaretAtColumn returns the stored offset on a visual line nearest display
// column col, snapped to a cluster/anchor boundary (never an anchor interior).
func chipCaretAtColumn(runes []rune, start, end, col int, spans []anchorSpan, chips map[string]database.Chip) int {
	w := 0
	for i := start; i < end; {
		var clEnd, rw int
		if sp := spanStartingAt(spans, i); sp != nil {
			clEnd, rw = sp.end, anchorWidth(*sp, chips)
		} else {
			_, cl := firstCluster(runes[i:])
			clEnd = i + cl
			rw = runewidth.StringWidth(string(runes[i:clEnd]))
		}
		if w+rw > col {
			return i
		}
		w += rw
		i = clEnd
	}
	return end
}
