package editor

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/database/style"
	"github.com/mattn/go-runewidth"
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
	cYellow  = "\x1b[38;2;255;215;95m"  // #ffd75f
	cGreen   = "\x1b[38;2;106;153;85m"  // #6a9955
	cMagenta = "\x1b[38;2;197;134;192m" // #c586c0
	cCyan    = "\x1b[38;2;78;201;176m"  // #4ec9b0
	bgCode   = "\x1b[48;2;31;31;31m"    // #1f1f1f block behind code rows
	bgPill   = "\x1b[48;2;38;79;120m"   // #264f78 behind date pills
	bgHit    = "\x1b[48;2;92;72;12m"    // yellow-brown query match highlight
	bgMol    = "\x1b[48;2;31;56;50m"    // #1f3832 block behind a ⌬ molecule chip
	// bgPage paints the MAIN region's page background ("" = the terminal's own,
	// i.e. transparent). Scope: the rows above the status bar only — the bar
	// (divider) and the Temporary Domain panel below it always stay transparent.
	bgPage = ""
)

// The horizontal selection bar is the SAME fill the row selection uses (selFill
// paints bgPill under a selected row): selecting rows and selecting runes must
// read as one thing, so both are that blue and both let the text keep its own
// color on it. Themed, so a /theme change moves them together.
func bgTextSel() string { return bgPill }

// A FILLED surface (the session pill worn by a chip, a node and a menu row)
// writes in whatever INK contrasts with its fill: near-black on a light fill —
// what makes Claude Code's orange read — and near-white on a dark one. Not
// themed, for the same reason the painter's bar is not: contrast is contrast.
const (
	cInkDark  = "\x1b[38;2;18;18;18m"
	cInkLight = "\x1b[38;2;245;245;245m"
)

// contrastInk picks the ink for a fill given as a foreground SGR: whichever of
// the two inks actually contrasts MORE with it, by WCAG contrast ratio. There is
// no knee to tune — a knee on raw channel values ignores sRGB gamma, and a
// saturated red is exactly where that goes wrong: it looks dark to the arithmetic
// while the eye reads it as light, so the glyph came out white-on-red and all but
// vanished. Decoding gamma first puts that red on black ink, where it belongs.
func contrastInk(fill string) string {
	r, g, b, ok := sgrRGB(fill)
	if !ok {
		return cInkDark
	}
	l := relLuminance(r, g, b)
	// contrast ratio is (lighter+0.05)/(darker+0.05); the fill sits between the
	// two inks, so each side is one division
	vsDark := (l + 0.05) / (relLuminance(18, 18, 18) + 0.05)
	vsLight := (relLuminance(245, 245, 245) + 0.05) / (l + 0.05)
	if vsDark >= vsLight {
		return cInkDark
	}
	return cInkLight
}

// relLuminance is the WCAG relative luminance of an sRGB triple — channels
// gamma-decoded to light before they are weighted.
func relLuminance(r, g, b int) float64 {
	lin := func(c int) float64 {
		v := float64(c) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// sgrRGB reads the channels out of a truecolor SGR sequence ("\x1b[38;2;r;g;bm").
func sgrRGB(s string) (r, g, b int, ok bool) {
	i := strings.Index(s, ";2;")
	if i < 0 || !strings.HasSuffix(s, "m") {
		return 0, 0, 0, false
	}
	parts := strings.Split(strings.TrimSuffix(s[i+3:], "m"), ";")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	if _, err := fmt.Sscanf(strings.Join(parts, " "), "%d %d %d", &r, &g, &b); err != nil {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

// glyphs (locked)
const (
	glyphOpen      = "○"
	glyphCollapsed = "●"
	glyphTodo      = "□"
	glyphTodoDone  = "■"
	glyphQuoteBar  = "▎"
	glyphDotted    = "◌" // Temporary Domain nodes (ephemeral)
	glyphSuggest   = "○" // a node with a proposal waiting on review (painted yellow)
	glyphCloud     = "☁" // a HOSTED coding session (started on the web or a phone)
	// glyphThinking is the reference mark, and it does NOT live in the glyph
	// column. ※ is CJK punctuation that fonts draw at a full-width design even
	// where the terminal allots it one cell, so standing where the bullet goes it
	// pushed the tree rail out of line with every other row. As a PREFIX it sits
	// inside the body instead: the row keeps the ordinary circle, the rail stays
	// straight, and only this row's own text shifts by however wide the mark
	// draws. Always muted gray, whatever /color says.
	glyphThinking = "※"
	// a Bash node wears the cmd chip's prompt, always (see bashGlyph)
	glyphBashPrompt = "$"
)

// underCompleted reports whether it sits under a completed ancestor in the
// real parent chain. Mirrors are separate rows with their own parent, so a
// child muted under a completed parent stays normal where it is mirrored
// elsewhere.
func underCompleted(it *item) bool {
	for p := it.parent; p != nil; p = p.parent {
		if p.completedAt > 0 {
			return true
		}
	}
	return false
}

// glyphFor returns the bullet glyph and its color for an item. Bullets and
// todo boxes are muted gray — the selected row turns its glyph red. Glyphs
// with an identity keep their own color: heading digits yellow. Headings show
// their level digit instead of a circle: that is how h1/h2/h3 stay visible in
// a single-line wysiwyg row. Mirrors keep their per-type glyph — the dim
// " · mirror" suffix marks them, not the bullet.
func glyphFor(it *item) (string, string) {
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

// emptyLine renders an empty node: a divider stripped of its rule — a blank
// row of pure vertical breathing room. The connector rail is kept so the tree
// stays continuous through the gap. There is no glyph or rule to turn red, so
// under the cursor a single red · centered in the row marks the selection.
func emptyLine(r row, maxLine int, selected bool) string {
	prefix := " " + cDim + connector(r) + cReset + "  "
	if !selected {
		return prefix
	}
	lead := (maxLine - visibleWidth(prefix)) / 2
	if lead < 0 {
		lead = 0
	}
	return prefix + strings.Repeat(" ", lead) + cRed + "·" + cReset
}

// dividerLine renders a divider node as a single horizontal rule. The glyph
// (circle) is hidden, but its SLOT is still reserved — a normal row is
// " connector ○ body", so the rule starts where the body would, and the ~96%
// footprint is of the content width after that bump, not the whole line. The
// rule hangs LEFT of center: the reserved glyph slot is a node gap that
// already contributes space on the left, so the rule's own left gap is half
// of what the right side carries. A non-empty body (the node's text, already
// rendered by renderBody with chips, styles and caret) sits on the midpoint
// of the rule — equal rule runs on each side center the text, with one space
// of breathing room around it; if the text leaves no room for a rule it
// stands alone. Muted gray normally, red under the cursor — the rule itself
// is the selection cue since there's no glyph.
func dividerLine(r row, maxLine int, body string, selected bool) string {
	// same indent as an ordinary row — connector, then a blank cell where the
	// glyph would sit, then the separator space
	prefix := " " + cDim + connector(r) + cReset + "  "
	col := cDim
	if selected {
		col = cRed
	}
	avail := maxLine - visibleWidth(prefix) // content width after the indent/rail

	// the whole divider — rule with or without text — keeps the same ~96%
	// footprint, hung left: the left gap is half of the right's, so a named
	// divider hangs short like an empty one instead of stretching the full
	// width.
	span := avail * 24 / 25
	if span < 1 {
		span = 1
	}
	lead := (avail - span) / 4 // half the centering: the node gap sits left already
	if lead < 0 {
		lead = 0
	}

	if visibleWidth(body) == 0 {
		return prefix + strings.Repeat(" ", lead) + col + strings.Repeat("─", span) + cReset
	}

	ruleW := span - visibleWidth(body) - 2
	if ruleW < 1 {
		return prefix + strings.Repeat(" ", lead) + body + cReset // text wider than the row — bare text
	}
	if ruleW%2 == 1 {
		ruleW-- // keep the rule runs equal so the text stays dead-center
	}
	left := ruleW / 2
	right := ruleW - left
	return prefix + strings.Repeat(" ", lead) + col + strings.Repeat("─", left) + cReset + " " + body + cReset + " " + col + strings.Repeat("─", right) + cReset
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

// quoteContinuationPrefix carries the quote bar onto every wrapped
// continuation line. The first line's text starts after the rail indent plus
// renderBody's own "▎ " prefix; continuation lines get the same rail indent
// from continuationPrefix with "▎ " appended after it (not swapped into its
// trailing cell), so the text column lines up under the first line instead
// of drifting one column left.
func quoteContinuationPrefix(r row, subtreeBelow bool) string {
	return continuationPrefix(r, subtreeBelow) + cAccent + glyphQuoteBar + cReset + " "
}

// rowOpts is the per-call context renderRow needs beyond the row itself. The
// three row loops — viewRenderRows (interactive), finalView (the quit dump) and
// readonlyRegionLines (the temp panel + the stashed main outline) — differ only
// in how they compute these flags per row; the row-shape branching (empty /
// divider / block code / normal) is identical across all three and lives once
// in renderRow.
type rowOpts struct {
	selected bool // this row is the one caret/cursor row of its face — drives the inline caret and the note-edit caret
	redGlyph bool // force the glyph/rule red — selected, OR (interactive only) part of the shift+↑/↓ multi-select range
	dashed   bool // Temporary Domain space: a non-mirror row shows the dotted glyph in place of its own
	// interactive gates the state that only means something in the live,
	// currently-focused frame: flash-mode dimming and match chips,
	// the query-hit / readonly-breadcrumb tint, and the pending-review band.
	// Neither the quit dump nor a read-only region has a "mode" of its own to
	// dim for or a review surface to open, so these must not leak into them.
	interactive  bool
	flashTargets []flashTarget // labelled matches to paint white+red on this row
	below        bool          // a visible child follows — the rail carries a │ down through the glyph column
	maxLine      int
}

// renderRow renders one visible row to its group lines (the node's own line(s),
// wrapped, with caret/selection baked in) and band lines (note, run output,
// per-type bands, pending review) — the one row-shape switch shared by
// viewRenderRows, finalView and readonlyRegionLines. tr is the row's OWN tree,
// passed explicitly rather than read off m.tree: a read-only region must
// resolve mirrors against the tree it was HANDED, even while m.tree points at a
// different one (the stashed main outline while the Temporary Domain holds
// focus) — reading m.tree there resolved against the wrong tree, or not at all.
func (m *Model) renderRow(tr *tree, r row, o rowOpts) (group []string, bands []string) {
	it := r.it
	maxLine := o.maxLine
	noteCaret := -1
	if o.selected && m.mode == modeNote {
		noteCaret = m.caret
	}

	// a ghost row is a proposed node, not a node: it renders as the row it would
	// be and nothing else — no note editing, no run band, no type face
	if it.ghost != "" {
		return m.ghostRowLines(r, o)
	}

	// an empty node is a blank spacer row: no glyph, no rule, no text — the
	// cursor cue is a single red · (see emptyLine). It still hangs a note.
	if it.typ == database.TypeEmpty {
		line := emptyLine(r, maxLine, o.selected && m.mode != modeFlash)
		if o.interactive && m.mode == modeFlash {
			line = cDim + stripSGR(line) + cReset
		}
		group = []string{line}
		bands = m.noteBandLines(tr, r, maxLine, o.below, noteCaret)
		if o.interactive {
			bands = append(bands, m.suggestBlockLines(r, o.below, maxLine)...)
		}
		return group, bands
	}

	// a divider is a full-width rule hiding the glyph; its text (if any) sits on
	// the midpoint of the rule and edits inline like any node — it still hangs a
	// note. A text wider than the row wraps under the tree rail.
	if it.typ == database.TypeDivider {
		shown := tr.resolve(it)
		name := tr.displayName(it)
		caret := -1
		// only a named divider carries a caret slot on the rule — an empty one
		// stays a clean rule (the red rule is itself the selection cue)
		if o.selected && name != "" && m.mode != modeNote && m.mode != modeFlash {
			caret = m.caret
		}
		body := renderBody(shown, name, caret, o.selected, m.chips)
		if o.interactive && m.mode == modeFlash {
			body = flashPaintBody(body, name, o.flashTargets, m.flashInput, m.chips)
		}
		line := dividerLine(r, maxLine, body, o.selected && m.mode != modeFlash)
		group = wrapLine(line, maxLine, continuationPrefix(r, o.below))
		bands = m.noteBandLines(tr, r, maxLine, o.below, noteCaret)
		if o.interactive {
			bands = append(bands, m.suggestBlockLines(r, o.below, maxLine)...)
		}
		return group, bands
	}

	// a code-block node renders AS the borderless block, standing in for its row
	// (no glyph/body line): the line-number gutter + code hang at the node's
	// indent. While focused the block carries the edit caret.
	if bc := typeOf(it.typ).blockCode; bc != nil {
		if code, caret, ok := bc(m, it, o.selected && m.focused); ok {
			inner := maxLine - visibleWidth(continuationPrefix(r, o.below))
			content := codeBlockLines(code, caret, inner)
			shown := tr.resolve(it)
			glyph, glyphColor := glyphFor(shown)
			if o.dashed && !r.mirrored {
				glyph = glyphDotted
			}
			glyph, glyphColor = m.suggestGlyph(it, glyph, glyphColor)
			if o.redGlyph {
				glyphColor = cRed
			}
			if o.interactive && m.mode == modeFlash {
				glyphColor = cDim
				for i, line := range content {
					content[i] = cDim + stripSGR(line) + cReset
				}
			}
			group = m.blockGroupLines(r, content, o.below, glyphColor+glyph+cReset)
			bands = m.noteBandLines(tr, r, maxLine, o.below, noteCaret)
			// runnable nodes (bash/query) hang their ephemeral output beneath them,
			// block-faced ones included
			bands = append(bands, m.runBandLines(r, o.below, maxLine)...)
			if b := typeOf(it.typ).bands; b != nil {
				bands = append(bands, b(m, r, o.below, maxLine)...)
			}
			if o.interactive {
				bands = append(bands, m.suggestBlockLines(r, o.below, maxLine)...)
			}
			return group, bands
		}
	}

	// the ordinary row-shaped face: glyph, wysiwyg body, dim suffix.
	shown := tr.resolve(it)
	glyph, glyphColor := glyphFor(shown)
	if o.dashed && !r.mirrored {
		glyph = glyphDotted // every Temporary Domain node shows a dashed icon
	}
	glyph, glyphColor = m.suggestGlyph(it, glyph, glyphColor)
	if o.redGlyph {
		glyphColor = cRed
	}
	name := tr.displayName(it)

	// The caret is drawn on EVERY selected row, including the ones that refuse
	// to be edited. A row you can put the cursor on and cannot see the cursor on
	// reads as a row the editor has lost track of; where the caret sits is how
	// you know which character the next key is aimed at, and a fixed row still
	// answers that question — with "nothing happens".
	caret := -1
	if o.selected && m.mode != modeNote && m.mode != modeFlash {
		caret = m.caret
	}
	body := renderBody(shown, name, caret, o.selected, m.chips)
	if rm := typeOf(shown.typ).renderM; rm != nil {
		body = rm(m, shown, caret) // Model-aware override (voice waveform)
	}
	if o.interactive && it.queryGenerated() {
		if it.readonly {
			// Breadcrumb scaffolding is a real nested tree, but deliberately gray
			// and content-locked because it is context, not a result.
			body = cDim + stripSGR(body) + cReset
		} else {
			body = m.highlightQueryHit(it, name, body)
		}
	}

	// a proposal about this node is shown AS the change: its text diffed in
	// place, or the whole row shining on its way out (see suggest.go)
	suffix := m.typeSuffix(r)
	if o.interactive {
		body = m.suggestBody(it, body, caret)
		suffix += m.suggestSuffix(it)
	}
	// flash.nvim: the whole outline goes gray; the typed match is black-on-white
	// and the remaining jump letters overlay black-on-red in place.
	flash := o.interactive && m.mode == modeFlash
	if flash {
		glyphColor = cDim
		body = flashPaintBody(body, name, o.flashTargets, m.flashInput, m.chips)
		suffix = cDim + stripSGR(suffix) + cReset
	}
	line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + body + suffix
	prefix := continuationPrefix(r, o.below)
	if it.typ == database.TypeQuote {
		prefix = quoteContinuationPrefix(r, o.below)
	}
	group = wrapLine(line, maxLine, prefix)

	bands = m.noteBandLines(tr, r, maxLine, o.below, noteCaret)
	// runnable nodes (bash/query) hang their ephemeral output beneath them. the
	// focused bash node shows its full scrollable viewer (the nodeView band
	// below) instead of this capped inline band, so don't render both
	focusedView := o.interactive && o.selected && m.focused && m.activeView(it) != nil
	if !focusedView {
		bands = append(bands, m.runBandLines(r, o.below, maxLine)...)
		if b := typeOf(it.typ).bands; b != nil {
			bands = append(bands, b(m, r, o.below, maxLine)...)
		}
	}
	// flash grays the note / run-output bands too, so nothing competes with the chips
	if flash {
		for k := range bands {
			bands[k] = cDim + stripSGR(bands[k]) + cReset
		}
	}
	return group, bands
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

// runBandCap is how many of the newest output lines an inline run band shows —
// the rest is one "⋯ N more" line, with the whole band an alt+e away.
const runBandCap = 3

// runOutTail renders the tail of a run band as rail-prefixed rows: an optional
// "⋯ N more" head line, then the newest capN lines with the program's own
// colors. (A running cmd chip shows no band — its tail lives in the chip label —
// so this serves the runnable-node band alone.)
func runOutTail(rail string, rs *runState, capN, maxLine int) []string {
	var lines []string
	shown := rs.lines()
	if len(shown) > capN {
		more := fmt.Sprintf("  ⋯ %d more", len(shown)-capN)
		if d := rs.dropCount(); d > 0 {
			more += fmt.Sprintf(" · %d dropped", d)
		}
		lines = append(lines, clip(rail+cReset+cDim+more+cReset, maxLine))
		shown = shown[len(shown)-capN:]
	}
	for _, l := range shown {
		lines = append(lines, clip(rail+cReset+"  "+styleOutLine(l), maxLine))
	}
	return lines
}

// runBandLines renders a runnable node's run output beneath it: stdout in the
// normal color, stderr red, capped to the last few lines, with a shimmering
// running indicator and its elapsed clock while the command is still going. The
// band is hydrated from its on-disk cache on first render (see runout.go) so it
// survives a restart, but it never enters the DB or sync.
func (m *Model) runBandLines(r row, subtreeBelow bool, maxLine int) []string {
	// a type whose run lives in its row tail (Bash, reading like the cmd chip)
	// never hangs a band: the "→" headline is the signal, alt+e is the terminal
	if typeOf(r.it.typ).runInTail {
		return nil
	}
	uuid := r.it.uuid
	m.ensureRunOutLoaded(uuid)
	rs := m.run(uuid) // non-nil after ensureRunOutLoaded
	running := rs.cancel != nil
	if len(rs.lines()) == 0 && !running {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	lines := runOutTail(rail, rs, runBandCap, maxLine)
	if running {
		foot := "  " + runningTag(rs) + cDim + " · ⌥k stop"
		lines = append(lines, clip(rail+cReset+cDim+foot+cReset, maxLine))
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
// when there is no note and we are not editing. tr is the row's OWN tree,
// passed explicitly rather than read off m.tree — see renderRow.
func (m *Model) noteBandLines(tr *tree, r row, maxLine int, subtreeBelow bool, caret int) []string {
	note := stripControlBytes(tr.displayNote(r.it))
	editing := caret >= 0
	if !editing {
		note = strings.TrimSpace(note)
		if note == "" {
			return nil
		}
	}
	rail := continuationPrefix(r, subtreeBelow)
	railW := 1 + 3*r.depth + 2
	textW := maxLine - railW - 2
	if textW < 8 {
		textW = 8
	}

	// A note is rich text on a SEPARATE surface: it understands every chip anchor
	// and every legacy plain tag or date, and it carries its own styling — the
	// node's is not its own. A red node used to hand its note the same red, so a
	// note could never be anything but a tinted echo of the row above it.
	//
	// What is left is a default, not an inheritance: muted gray and italic, the
	// note's resting look, which a note's own styled runs paint over. That is the
	// one difference between the two surfaces — a node rests white, a note rests
	// gray. The synthetic uuid is what keeps the two sets of runs apart.
	noteItem := *r.it
	noteItem.uuid = noteSpanUUID(r.it)
	noteItem.typ = database.TypeBullets
	noteItem.style = noteRestingStyle
	styled := renderBody(&noteItem, note, caret, editing, m.chips)
	segs := wrapLine(styled, textW, "")
	if len(segs) == 0 {
		segs = []string{""}
	}
	if !editing && len(segs) > noteBandMaxLines {
		hidden := len(segs) - noteBandMaxLines
		noun := "lines"
		if hidden == 1 {
			noun = "line"
		}
		suffix := fmt.Sprintf(" … +%d %s", hidden, noun)
		last := noteBandMaxLines - 1
		plainRoom := textW - visibleWidth(suffix)
		if plainRoom < 1 {
			plainRoom = 1
		}
		segs[last] = clip(segs[last], plainRoom) + cReset + cDim + cItalic + suffix + cReset
		segs = segs[:noteBandMaxLines]
	}

	bandW := 1
	for _, seg := range segs {
		if w := visibleWidth(seg); w > bandW {
			bandW = w
		}
	}
	var out []string
	for _, seg := range segs {
		gap := strings.Repeat(" ", max(0, bandW-visibleWidth(seg)))
		out = append(out, rail+cReset+" "+seg+gap+" "+cReset)
	}
	return out
}

// noteRestingStyle is a note's look before it is styled: the gray swatch (the
// same muted gray the theme gives /color gray) and italic. It is a STYLE rather
// than a hardcoded SGR so it follows the active theme, and so a note's own runs
// override it through the ordinary span path.
var noteRestingStyle = style.Normalize("italic,color:gray")

// noteBandMaxLines is how much of a note the outline shows at rest. A note is a
// footnote to its node, not a second outline: past this it earns a count of what
// it is holding back instead of pushing the tree down the screen.
const noteBandMaxLines = 2

// truncateNote cuts a wrapped note to noteBandMaxLines, spending the end of the
// last shown line on how many lines are not shown. The count rides ON that line
// rather than taking one of its own, so a note can never cost the outline more
// rows than it is allowed. Only the resting band is cut — editing it (caret >= 0
// in noteBandLines) renders every line, so nothing here is unreachable.
func truncateNote(segs []string, textW int) []string {
	if len(segs) <= noteBandMaxLines {
		return segs
	}
	hidden := len(segs) - noteBandMaxLines
	noun := "lines"
	if hidden == 1 {
		noun = "line"
	}
	more := fmt.Sprintf(" +%d %s", hidden, noun)
	room := textW - runewidth.StringWidth(more)
	if room < 1 {
		room = 1
	}
	out := append([]string(nil), segs[:noteBandMaxLines]...)
	out[len(out)-1] = clipStr(out[len(out)-1], room) + more
	return out
}

// spanFlags is the per-rune mask the renderer uses. Text styling (bold, italic,
// underline, color) is a per-node attribute — see item.style — not inline
// markup, so no syntax markers leak into the stored name, search or export.
// Dates carry no markers either: the renderer recognises the canonical date
// format in the plain text and chips those runes.
type spanFlags struct {
	date    bool   // part of a canonical YYYY-MM-DD[ HH:MM] date, painted as a chip
	tag     bool   // part of a #tag, painted muted gray or its assigned pill color
	tagWord string // the tag's word (no '#'), for the per-tag color lookup
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
		word := strings.TrimPrefix(string(runes[span[0]:span[1]]), "#")
		for k := span[0]; k < span[1] && k < len(flags); k++ {
			flags[k].tag = true
			flags[k].tagWord = word
		}
	}
	return flags
}

// renderCmdChip paints a committed cmd chip. The prompt+command sits on a gray
// code cell; the output preview (in-memory label, rehydrated from node_output)
// is muted text after the cell, with no background, so "$ ls" reads as the
// runnable part and "→ result" as last-run chrome.
//
// While the command is RUNNING (cmdChipRunning, refreshed per frame by
// syncLiveCmdRuns) that flat code cell becomes a shimmer: the same cell colors
// with a soft highlight sliding across the background, text untouched. Only
// colors change — the chip's display width stays chipDisplay's, which the caret
// and wrap math depend on.
//
// The caret is normally the whole chip inverted, but alt+r runs the chip the
// caret sits on, so that is exactly the state a running chip is in — and reverse
// video would swallow the pulse (dark text on a bright block). A running chip
// under the caret therefore inverts ONE cell, its "$" prompt, the same one-cell
// cursor the rest of the editor draws, and shimmers the rest.
func renderCmdChip(c database.Chip, caretOn bool) string {
	var b strings.Builder
	b.WriteString(cReset)
	switch {
	case cmdChipRunning(c.ID):
		runes := []rune("$ " + c.Value)
		bg, fg := "", ""
		for j, r := range runes {
			// the "$ " prompt keeps its red, the command the normal text color;
			// only the background moves. Re-emit an SGR only when it changes, so a
			// long command is not a per-cell escape storm.
			want := cRed
			if j >= 2 {
				want = cFG
			}
			if s := shimmerBG(len(runes), j, animFrame); s != bg {
				b.WriteString(s)
				bg = s
			}
			if want != fg {
				b.WriteString(want)
				fg = want
			}
			if caretOn && j == 0 {
				// the caret cell: reset after it and let the next cell re-arm its
				// colors, so reverse video covers this one cell only
				b.WriteString(cInvert + string(r) + cReset)
				bg, fg = "", ""
				continue
			}
			b.WriteRune(r)
		}
		b.WriteString(cReset)
	case caretOn:
		b.WriteString(cInvert + bgCode + cRed + "$ " + cFG + c.Value + cReset)
	default:
		b.WriteString(bgCode + cRed + "$ " + cFG + c.Value + cReset)
	}
	if c.Label != "" {
		b.WriteString(cDim + " → " + c.Label + cReset)
	}
	return b.String()
}

// renderAgentChip draws a session chip as a filled PILL: the agent's glyph and
// the session's name on the session's own color, near-black ink on top. A hosted
// session carries the cloud mark inside the pill (see refreshAgentChip). Like
// the cmd chip this owns its whole look, so the generic chip-color path in
// renderBody steps aside for it.
//
// struck carries the completed row's strikethrough INTO the pill. The pill keeps
// its color when the row is done — a finished session is still the agent it was —
// so the line through it is what says finished, and it is drawn in the pill's own
// ink, which contrastInk keeps dark against every fill in the palette.
func renderAgentChip(c database.Chip, caretOn, struck bool) string {
	glyph, col := "◈", cDim
	if v, ok := agentVariantByID(c.Value); ok {
		glyph = v.glyph
		col = v.colorSGR()
	}
	if l, ok := agentLooks[c.ID]; ok && l != "" {
		col = l // the session's own color, when its CLI gave it one
	}
	label := c.Label
	if label == "" {
		label = c.Value // no session yet: the pill names the CLI
	}
	var b strings.Builder
	b.WriteString(cReset)
	if caretOn {
		b.WriteString(cInvert)
	}
	b.WriteString(bgOf(col) + contrastInk(col))
	if struck {
		b.WriteString(cStrike)
	}
	b.WriteString(" " + glyph + " " + label + " " + cReset)
	return b.String()
}

// renderZoteroChip paints a citation as a FILLED pill in the Zotero brand red —
// the same filled-pill shape a session chip wears (see renderAgentChip), so a
// citation reads as one filled object in the sentence. The brand color fills
// the background and the ink contrasts with it (see contrastInk) — which is
// what keeps the near-black mark legible on the red. The pill keeps the OSC 8
// hyperlink the plain citation advertised, so ctrl+click still opens the entry.
func renderZoteroChip(c database.Chip, caretOn bool) string {
	col := iconColorSGR(zoteroBrandColor())
	if col == "" {
		col = cRed
	}
	link := zoteroLinkTarget(c)
	var b strings.Builder
	b.WriteString(cReset)
	if caretOn {
		b.WriteString(cInvert)
	}
	b.WriteString(bgOf(col) + contrastInk(col))
	if link != "" {
		b.WriteString(oscLink(link))
	}
	b.WriteString(nonBreaking(" " + zoteroMark + " " + zoteroChipLabel(c) + " "))
	b.WriteString(cReset)
	if link != "" {
		b.WriteString(oscLink(""))
	}
	return b.String()
}

// bgOf turns a foreground SGR ("\x1b[38;2;r;g;bm") into the matching BACKGROUND
// sequence, so a chip can be filled with the color a session is themed in
// without a second palette. A code it cannot read leaves the pill unfilled.
func bgOf(fg string) string {
	if s, ok := strings.CutPrefix(fg, "\x1b[38;"); ok {
		return "\x1b[48;" + s
	}
	return ""
}

// renderBody renders a node name wysiwyg. Text keeps its normal color on
// every row — selection is carried by the red glyph alone. Unselected rows
// hide the markdown markers; the selected row shows them and the block
// cursor inverts the cell under the rune at the caret index (-1 for none).
// A per-type prefix/color/muteFrom comes from the descriptor hooks, not a
// switch here.

func renderBody(it *item, name string, caret int, selected bool, chips map[string]database.Chip) string {
	name = stripControlBytes(name)
	if r := typeOf(it.typ).render; r != nil {
		return r(it, name) // per-type inline-body override (json preview)
	}
	desc := typeOf(it.typ)
	base := cFG
	// a type may set its own body color (e.g. log's dim)
	if desc.baseColor != nil {
		if c := desc.baseColor(it); c != "" {
			base = c
		}
	}
	// a /color picks the node's foreground; default stays the palette gray. A
	// type whose color is FIXED (thinking is muted gray, always) says so and
	// keeps it — the color is part of what the type means, not a preference.
	if c := styleBaseColor(it.style); c != "" && !desc.fixedColor {
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
	}
	if desc.prefix != nil {
		prefix = desc.prefix(it) // per-type prefix, e.g. the log time chip
	}
	if s := desc.sign; s != "" {
		prefix = cDim + s + cReset // type sign as prefix, e.g. "⌕ " for query
	}
	// /bold, /italic, /underline layer on top of the layout's own attributes
	attrs += styleAttrs(it.style)
	// completed nodes are muted gray + struck through, overriding /color so done
	// work reads as quiet no matter the node's style. Descendants of a completed
	// ancestor mute the same way (no strike) so a finished parent quiets its
	// subtree — but only along that parent chain: a mirror of the child sitting
	// under a different parent keeps normal color (it.parent is the mirror's
	// own parent, not the completed one).
	if it.completedAt > 0 {
		base = cDim
		attrs += cStrike
	} else if underCompleted(it) {
		base = cDim
	}

	runes := []rune(name)
	flags := inlineSpans(runes)
	if magicKeywordRow(it) {
		markKeywords(runes, flags, animFrame) // ultracode/ultraloop: render-time only
	}
	spanSGR := spanSGRFor(it.uuid, len(runes))
	// the live horizontal selection (shift+←/→) draws as a bar over its runes
	selLive := textSelUUID != "" && textSelUUID == it.uuid
	selLo, selHi := textSelLo, textSelHi
	chipsp := anchorSpans(runes) // inline chip anchors, drawn collapsed
	if desc.muteFrom != nil {
		// a type may mute a tail — the log type mutes from the first " · "
		if d := desc.muteFrom(name); d >= 0 && d < len(runes) {
			for k := d; k < len(runes); k++ {
				flags[k].mute = true
			}
		}
	}
	if desc.spanColor != nil {
		// a type may tint individual runes (math operators) — reuses the per-rune
		// kwColor channel so the caret/selection path colors the cell correctly.
		for k, c := range desc.spanColor(it, runes) {
			if k >= 0 && k < len(flags) && c != "" {
				flags[k].kwColor = c
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
		// node's /color never bleeds into them: tags stay muted gray (or their
		// assigned pill color), date chips keep a neutral foreground on their pill.
		if f.tag {
			if pill := tagColorSGR(f.tagWord); pill != "" {
				return cReset + attrs + pill
			}
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
	for i := 0; i < len(runes); {
		// a chip anchor renders collapsed: the chip kind's color + compact display,
		// atomic. The caret only ever sits at its boundaries (see snapCaret).
		if sp := spanStartingAt(chipsp, i); sp != nil {
			// a cmd chip is a code cell: "$ cmd" on the gray code tint — "$"
			// red, the command in the normal text color — while the run-output
			// preview after the → is muted gray outside the background. The
			// caret-on-chip cursor adds reverse video ON TOP of those colors: the
			// wrap machinery drops cInvert (a cursor never spans lines) but carries
			// the colors, so a chip that wraps stays tinted on every continuation.
			if c, ok := chips[sp.id]; ok && c.Kind == chipKindCmd {
				b.WriteString(renderCmdChip(c, caret == sp.start))
				cur = ""
				i = sp.end
				continue
			}
			// a session chip is a filled pill in the agent's (or the session's)
			// own color — it owns its whole look, like the cmd chip's code cell.
			// The row's strike is the one attribute that carries into it: a chip
			// in a finished row is a finished session.
			if c, ok := chips[sp.id]; ok && c.Kind == chipKindAgent {
				b.WriteString(renderAgentChip(c, caret == sp.start, strings.Contains(attrs, cStrike)))
				cur = ""
				i = sp.end
				continue
			}
			// a citation chip is a filled pill in the Zotero brand red — the same
			// filled-pill shape a session chip wears (see renderZoteroChip).
			if c, ok := chips[sp.id]; ok && c.Kind == chipKindZotero {
				b.WriteString(renderZoteroChip(c, caret == sp.start))
				cur = ""
				i = sp.end
				continue
			}
			col := cCyan
			osc8 := "" // URL link target for an OSC 8 hyperlink, "" = none
			if c, ok := chips[sp.id]; ok {
				if k, ok := chipKindOf(c.Kind); ok {
					col = k.color
				}
				// a tag chip with an assigned color wears its pill
				if c.Kind == chipKindTag {
					if pill := tagColorSGR(c.Value); pill != "" {
						col = pill
					}
				}
				// a painted icon chip wears its catalog brand color (label=shortcode)
				if c.Kind == chipKindIcon {
					if s := iconColorSGR(iconColorForChip(c)); s != "" {
						col = s
					}
				}
				// a URL link chip emits an OSC 8 hyperlink so terminals that support
				// it make the chip Ctrl+clickable. Node links can't — the terminal
				// can't jump inside the app — so they get no OSC 8.
				if c.Kind == chipKindLink {
					col = linkChipColorCode() // /settings link.color: blue or gray
					// a link to a known service swaps that gray for the
					// service's own muted hue — same weight (see service.go)
					if svc, ok := linkService(c); ok {
						col = serviceChipColor(svc)
					}
					// a color assigned in the ⌥e editor beats both: it is this
					// chip's own, chosen for this link (see link.go)
					if s := linkChipColorSGR(c.ID); s != "" {
						col = s
					}
					if _, isNode := nodeLinkUUID(c.Value); !isNode {
						osc8 = c.Value
					}
				}
			}
			b.WriteString(cReset + col)
			if osc8 != "" {
				b.WriteString(oscLink(osc8))
			}
			if caret == sp.start {
				b.WriteString(cInvert) // cursor sits on the whole chip
			}
			// atomic on screen as well as to the caret: a soft wrap never breaks
			// a chip apart mid-name (see nonBreaking)
			b.WriteString(nonBreaking(dispByID(sp.id, chips)))
			b.WriteString(cReset)
			if osc8 != "" {
				b.WriteString(oscLink(""))
			}
			cur = ""
			i = sp.end
			continue
		}
		r := runes[i]
		f := flags[i]
		if i == caret {
			// the block cursor sits ON the rune: same colors as the cell —
			// including its styled run — so the block wears the character's
			// real color, then inverts. Inside a selection it inverts the bar
			// instead, so the caret stays legible on the white run.
			s := sgr(f)
			if spanSGR != nil && spanSGR[i] != "" {
				s += spanSGR[i]
			}
			if selLive && i >= selLo && i < selHi {
				s += bgTextSel()
			}
			b.WriteString(s + cInvert)
			b.WriteRune(r)
			cur = "" // force a state re-emit after the caret cell
			i++
			continue
		}
		s := sgr(f)
		// a styled run (see spans.go) overrides the cell's color/attrs; a live
		// horizontal selection lays the row-selection blue under those cells and
		// nothing else. NOT inverse video, which swaps fg/bg and makes an
		// already-red run read as a red background.
		if spanSGR != nil && spanSGR[i] != "" {
			s += spanSGR[i]
		}
		if selLive && i >= selLo && i < selHi {
			s += bgTextSel()
		}
		if s != cur {
			b.WriteString(s)
			cur = s
		}
		b.WriteRune(r)
		i++
	}
	if caret >= len(runes) && caret >= 0 {
		// past the last rune: paint one trailing block-cursor cell
		b.WriteString(cReset + cFG + cInvert + " ")
	}
	if bt := desc.bodyTail; bt != nil {
		if tail := bt(it, chips); tail != "" {
			b.WriteString(cReset + " " + tail) // math's dim subtree preview
		}
	}
	if it.starred {
		b.WriteString(cReset + " " + cDim + "★") // /star mark, render-only
	}
	b.WriteString(cReset)
	return b.String()
}

// renderJSONPreview renders a json node as a one-line entry: a {} marker plus a
// whitespace-collapsed, truncated preview of the JSON. Invalid JSON turns the {}
// marker red and appends a red " · invalid" (the same word the alt+e editor's own
// status chip uses). Editing happens only in
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
		cRed + " · invalid" + cReset
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
// noteBandLines) — so the suffix only carries mirror, collapsed-child and
// backlink counts.
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

func (m *Model) typeSuffix(r row) string {
	it := r.it
	var parts []string
	if it.mirrorOf != "" {
		parts = append(parts, "mirror")
	}
	// a mirrored Zotero entry is locked on every one of its rows; saying so ten
	// times down a subtree is noise, and its own marks already say whose it is
	if it.readonly && !zoteroMirrored(it) {
		parts = append(parts, "locked")
	}
	if it.structureLocked && !it.readonly {
		parts = append(parts, "fixed")
	}
	if it.typ == database.TypeQuery {
		parts = append(parts, fmt.Sprintf("%d hits", queryHitCount(it)))
		if ts := m.queryUpdatedAt(it.uuid); ts > 0 {
			parts = append(parts, "updated "+relTime(ts))
		}
	}
	if it.typ == database.TypeWeb {
		if n := webResultCount(it); n > 0 {
			parts = append(parts, fmt.Sprintf("%d results", n))
		}
		if ts := m.webUpdatedAt(it.uuid); ts > 0 {
			parts = append(parts, "updated "+relTime(ts))
		}
	}
	// an nlpcompute cell that has generated says which language it holds. The
	// code block normally REPLACES the row, so this shows where the block cannot
	// be drawn (the temp panel, the prose face) — and it lives here, in the
	// shared suffix, rather than in a render override that would cost the row its
	// chips and its style.
	if it.typ == database.TypeNLPCompute {
		if d := ncLoad(m, it.uuid); d.Code != "" {
			lang := d.Lang
			if lang == "" {
				lang = "code"
			}
			parts = append(parts, lang)
		}
	}
	// a markup row wears its language's mark and, after it, the document it
	// composes — truncated, because the row is a glance and ⌥e is the reading
	if mark := markupMark(it.typ); mark != "" {
		parts = append(parts, mark)
		if p := m.markupPreview(it); p != "" && len(markupKids(it)) > 0 {
			parts = append(parts, "→ "+clipStr(p, markupTailCap))
		}
	}
	// a cycled row folds its children like a collapsed one — same count hint
	if kids := m.tree.childItems(it); len(kids) > 0 && (it.collapsed || r.cycled) {
		noun := "children"
		if len(kids) == 1 {
			noun = "child"
		}
		parts = append(parts, fmt.Sprintf("%d %s", len(kids), noun))
	}
	// the nodes elsewhere that mirror or [[-link to this one. Unlike children,
	// backlinks are never visible from the row, so the count shows whenever it
	// is nonzero — a mirror row counts its source's.
	if n := m.backlinkCount(it); n > 0 {
		noun := "backlinks"
		if n == 1 {
			noun = "backlink"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, noun))
	}
	// mirrors of this node elsewhere in the outline. Like backlinks, the count
	// is always visible when nonzero — a mirror row counts its source's.
	if n := m.mirrorCount(it); n > 0 {
		noun := "mirrors"
		if n == 1 {
			noun = "mirror"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, noun))
	}
	suffix := ""
	if len(parts) > 0 {
		suffix = cDim + " · " + strings.Join(parts, " · ") + cReset
	}
	return suffix
}
