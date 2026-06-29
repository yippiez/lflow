package editor

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/utils"
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

// Path chips are created by the /file fuzzy picker (see file.go), not by typing a
// marker — so "#" stays tags-only. The chip's display marker is "›" (see chipKinds).

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
	chipKindLink = "link"
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
			return "›" + base
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
	// link chips carry a name (label) and a target (value: a URL or
	// lflow://node/<uuid>). display/expand below are fallbacks on value only —
	// chipDisplay/chipExpand special-case links to use the label.
	chipKindLink: {
		key:     chipKindLink,
		color:   cAccent + cUnderline,
		display: func(v string) string { return "→" + v },
		expand:  func(v string) string { return v },
	},
}

func chipKindOf(kind string) (chipKind, bool) {
	k, ok := chipKinds[kind]
	return k, ok
}

// chipDisplay returns the compact display string for a chip record. A link uses
// its label (the arbitrary name), not its target.
func chipDisplay(c database.Chip) string {
	if c.Kind == chipKindLink {
		return "→" + linkChipLabel(c)
	}
	if k, ok := chipKindOf(c.Kind); ok && k.display != nil {
		return k.display(c.Value)
	}
	return c.Value
}

// chipExpand returns the full underlying value for a chip record. A link expands
// to "[name](target)" so both halves survive bash/script/export surfaces.
func chipExpand(c database.Chip) string {
	if c.Kind == chipKindLink {
		return "[" + linkChipLabel(c) + "](" + c.Value + ")"
	}
	if k, ok := chipKindOf(c.Kind); ok && k.expand != nil {
		return k.expand(c.Value)
	}
	return c.Value
}

// linkChipLabel is a link's display name, falling back to its target so a link
// is never blank.
func linkChipLabel(c database.Chip) string {
	if c.Label != "" {
		return c.Label
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

// backfillChipsOnce converts every legacy plain-text #tag and canonical date in
// the outline into chips, exactly once (guarded by a system flag). It reuses the
// same detection that renders them, so what becomes a chip is exactly what
// already rendered as one.
func (m *Model) backfillChipsOnce() {
	if m.ctx.DB == nil {
		return
	}
	var done string
	if database.GetSystem(m.ctx.DB, "chips_backfilled", &done) == nil && done == "1" {
		return
	}
	converted := 0
	var walk func(it *item)
	walk = func(it *item) {
		if it.mirrorOf == "" && it.name != "" && !hasAnchor(it.name) {
			converted += m.backfillName(it)
		}
		for _, c := range it.children {
			walk(c)
		}
	}
	walk(m.tree.root)
	if converted > 0 {
		if _, err := m.tree.save(); err == nil {
			m.tree.refreshSnapshots()
		}
	}
	_ = database.UpsertSystem(m.ctx.DB, "chips_backfilled", "1")
}

// backfillName rewrites it.name, replacing every tag/date span with a chip
// anchor; it returns how many it converted.
func (m *Model) backfillName(it *item) int {
	type match struct {
		start, end  int
		kind, value string
	}
	name := it.name
	runes := []rune(name)
	var ms []match
	for _, sp := range detectTagSpans(name) {
		ms = append(ms, match{sp[0], sp[1], chipKindTag, strings.TrimPrefix(string(runes[sp[0]:sp[1]]), "#")})
	}
	for _, sp := range detectDateSpans(name) {
		ms = append(ms, match{sp[0], sp[1], chipKindDate, string(runes[sp[0]:sp[1]])})
	}
	if len(ms) == 0 {
		return 0
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].start < ms[j].start })

	var b strings.Builder
	prev, last, n := 0, -1, 0
	for _, mm := range ms {
		if mm.start < last {
			continue // overlapping span (e.g. a date inside a tag) — keep the earlier
		}
		b.WriteString(string(runes[prev:mm.start]))
		b.WriteString(m.createChip(mm.kind, mm.value))
		prev, last, n = mm.end, mm.end, n+1
	}
	b.WriteString(string(runes[prev:]))
	it.name = b.String()
	return n
}

// ── Model chip store ───────────────────────────────────────────────────────

// createChip records a new chip (kind + value) and returns the in-text anchor to
// splice into a node name. The record is written through to the DB at once so it
// survives even before the node is saved (the anchor in the name pins it).
func (m *Model) createChip(kind, value string) string {
	return m.createLabeledChip(kind, value, "")
}

// createLabeledChip is createChip with an explicit display label — used by link
// chips, whose name is separate from their target value.
func (m *Model) createLabeledChip(kind, value, label string) string {
	id, err := utils.GenerateUUID()
	if err != nil {
		return ""
	}
	c := database.Chip{ID: id, Kind: kind, Value: value, Label: label}
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
