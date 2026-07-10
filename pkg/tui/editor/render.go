package editor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/lflow/lflow/pkg/tui/database"
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
	bgTerm   = "\x1b[48;2;30;34;48m"    // #1e2230 terminal block behind bash rows
	bgPill   = "\x1b[48;2;38;79;120m"   // #264f78 behind date pills
	// bgPage paints the MAIN region's page background ("" = the terminal's own,
	// i.e. transparent). Scope: the rows above the status bar only — the bar
	// (divider) and the Temporary Domain panel below it always stay transparent.
	bgPage = ""
)

// The painter's window: a white bar (not themed — white is white) with dark
// text for unpainted runes; painted runes keep their color on it.
const (
	bgPaintSel = "\x1b[48;2;255;255;255m"
	fgPaintSel = "\x1b[38;2;30;30;30m"
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
	rs := m.run(uuid) // non-nil after ensureRunOutLoaded
	out := rs.out
	running := rs.cancel != nil
	if len(out) == 0 && !running {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	var lines []string
	shown := out
	const capN = 3
	if len(shown) > capN {
		more := fmt.Sprintf("  ⋯ %d more", len(shown)-capN)
		if d := rs.dropped; d > 0 {
			more += fmt.Sprintf(" · %d dropped", d)
		}
		lines = append(lines, clip(rail+cReset+cDim+more+cReset, maxLine))
		shown = shown[len(shown)-capN:]
	}
	for _, l := range shown {
		lines = append(lines, clip(rail+cReset+"  "+styleOutLine(l), maxLine))
	}
	if running {
		lines = append(lines, clip(rail+cReset+cDim+"  running… · ⌥x stop"+cReset, maxLine))
	}
	return lines
}

// friendlyTool maps a CLI's raw tool name to a short display verb; anything
// unmapped is shown with its first letter capitalized.
var friendlyTool = map[string]string{
	"read": "Read", "cat": "Read", "view": "Read",
	"write": "Write", "create": "Write", "create_file": "Write",
	"edit": "Edit", "str_replace": "Edit", "patch": "Edit", "update": "Edit", "apply_patch": "Edit",
	"bash": "Bash", "exec": "Bash", "shell": "Bash", "run": "Run",
	"grep": "Grep", "search": "Search", "glob": "Glob", "ls": "List", "list": "List",
	"fetch": "Fetch", "webfetch": "Fetch",
}

// toolColor tints a tool verb by what it does: reads cyan, writes green, edits
// yellow, shell magenta, everything else the accent color.
func toolColor(verb string) string {
	switch verb {
	case "Read", "Grep", "Search", "Glob", "List", "Fetch":
		return cCyan
	case "Write":
		return cGreen
	case "Edit":
		return cYellow
	case "Bash", "Run":
		return cMagenta
	default:
		return cAccent
	}
}

// displayTool turns a raw tool name into its display verb.
func displayTool(name string) string {
	if v, ok := friendlyTool[strings.ToLower(name)]; ok {
		return v
	}
	r := []rune(name)
	if len(r) == 0 {
		return name
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// agentBandLines renders the running @mention's live progress as one muted band
// beneath the mention node: while a tool runs, the tool verb in its color then
// the file/command in gray; otherwise a plain "Thinking…" — so the band never
// freezes on a stale tool call. Ephemeral: never persisted, never in the outline.
// The caller only invokes this while the turn is busy.
func (m *Model) agentBandLines(r row, subtreeBelow bool, maxLine int) []string {
	rail := continuationPrefix(r, subtreeBelow)
	var tl agentToolLine
	if t := m.thread(r.it.uuid); t != nil {
		tl = t.tool
	}
	body := cDim + "Thinking…" + cReset
	if tl.name != "" {
		verb := displayTool(tl.name)
		body = toolColor(verb) + verb + cReset
		if tl.detail != "" {
			body += " " + cDim + tl.detail + cReset
		}
	}
	return []string{clip(rail+cReset+"  "+body, maxLine)}
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
// code cell; the ephemeral output preview is muted text after the cell, with no
// background, so "$ ls" reads as the runnable part and "→ result" as output.
func renderCmdChip(c database.Chip, caretOn bool) string {
	var b strings.Builder
	b.WriteString(cReset)
	if caretOn {
		b.WriteString(cInvert)
	}
	b.WriteString(bgCode + cRed + "$ " + cFG + c.Value + cReset)
	if c.Label != "" {
		b.WriteString(cDim + " → " + c.Label + cReset)
	}
	return b.String()
}

// activeCmdDraftRange returns the not-yet-committed cmd chip range that contains
// the caret. A standalone "$" starts the draft immediately; single spaces stay
// in the command, while a double space means the draft has ended (and normally
// commits before render). Anchor interiors are ignored so path chips can still
// be folded into the command when the double space arrives.
func activeCmdDraftRange(runes []rune, caret int, spans []anchorSpan) (int, int) {
	if caret <= 0 || caret > len(runes) {
		return -1, -1
	}
	for i := caret - 1; i >= 0; {
		if sp := spanEndingAt(spans, i+1); sp != nil {
			i = sp.start - 1
			continue
		}
		if runes[i] == '$' && (i == 0 || runes[i-1] == ' ') {
			for j := i + 1; j < caret; j++ {
				if runes[j] == ' ' && j+1 < caret && runes[j+1] == ' ' {
					return -1, -1
				}
			}
			return i, caret
		}
		i--
	}
	return -1, -1
}

// renderBody renders a node name wysiwyg. Text keeps its normal color on
// every row — selection is carried by the red glyph alone. Unselected rows
// hide the markdown markers; the selected row shows them and the block
// cursor inverts the cell under the rune at the caret index (-1 for none).
// A per-type prefix/color/muteFrom comes from the descriptor hooks (built-in or
// a JS mod via the nodemod bridge), not a switch here.

func renderBody(it *item, name string, caret int, selected bool, chips map[string]database.Chip) string {
	name = stripControlBytes(name)
	if r := typeOf(it.typ).render; r != nil {
		return r(it, name) // per-type inline-body override (json preview)
	}
	desc := typeOf(it.typ)
	base := cFG
	// a type may set its own body color (e.g. a mod returning dim/its /color)
	if desc.baseColor != nil {
		if c := desc.baseColor(it); c != "" {
			base = c
		}
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
	}
	if desc.prefix != nil {
		prefix = desc.prefix(it) // per-type prefix, e.g. a log mod's time chip
	}
	if s := desc.sign; s != "" {
		prefix = cDim + s + cReset // type sign as prefix, e.g. "⌕ " for query
	}
	// /bold, /italic, /underline layer on top of the layout's own attributes
	attrs += styleAttrs(it.style)
	if it.completedAt > 0 {
		attrs += cStrike
	}

	runes := []rune(name)
	flags := inlineSpans(runes)
	markKeywords(runes, flags, animFrame) // ultracode/ultraloop: render-time only
	spanSGR := spanSGRFor(it.uuid, len(runes))
	paintLive := paintUUID == it.uuid
	paintLo, paintHi := paintBounds()
	chipsp := anchorSpans(runes) // inline chip anchors, drawn collapsed
	cmdDraftStart, cmdDraftEnd := activeCmdDraftRange(runes, caret, chipsp)
	if desc.muteFrom != nil {
		// a type may mute a tail — the log artifact mutes from the first " · "
		if d := desc.muteFrom(name); d >= 0 && d < len(runes) {
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
	switch it.typ {
	case database.TypeCode:
		b.WriteString(cReset + attrs + " ") // pad the code block
	}
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
				// a URL link chip emits an OSC 8 hyperlink so terminals that support
				// it make the chip Ctrl+clickable. Node links can't — the terminal
				// can't jump inside the app — so they get no OSC 8.
				if c.Kind == chipKindLink {
					col = linkChipColorCode() // /settings link.color: blue or gray
					if _, isNode := nodeLinkUUID(c.Value); !isNode {
						osc8 = c.Value
					}
				}
			}
			b.WriteString(cReset + col)
			if osc8 != "" {
				b.WriteString("\x1b]8;;" + osc8 + "\x1b\\")
			}
			if caret == sp.start {
				b.WriteString(cInvert) // cursor sits on the whole chip
			}
			b.WriteString(dispByID(sp.id, chips))
			b.WriteString(cReset)
			if osc8 != "" {
				b.WriteString("\x1b]8;;\x1b\\")
			}
			cur = ""
			i = sp.end
			continue
		}
		r := runes[i]
		f := flags[i]
		if i >= cmdDraftStart && i < cmdDraftEnd {
			// Live cmd-chip draft: as soon as a standalone "$" is typed, the
			// not-yet-committed command wears the same gray cell as the final chip.
			// A double space commits it; until then the stored text remains plain.
			s := cReset + bgCode
			if r == '$' && i == cmdDraftStart {
				s += cRed
			} else {
				s += cFG + attrs
			}
			if spanSGR != nil && spanSGR[i] != "" {
				s += spanSGR[i]
			}
			if i == caret {
				s += cInvert
			}
			if s != cur {
				b.WriteString(s)
				cur = s
			}
			b.WriteRune(r)
			i++
			continue
		}
		if i == caret {
			// the block cursor sits ON the rune: same colors as the cell —
			// including its painted span — so the block wears the character's
			// real color, then inverts
			s := sgr(f)
			if spanSGR != nil && spanSGR[i] != "" {
				s += spanSGR[i]
			}
			b.WriteString(s + cInvert)
			b.WriteRune(r)
			cur = "" // force a state re-emit after the caret cell
			i++
			continue
		}
		s := sgr(f)
		// a painted run (see paint.go) overrides the cell's color/attrs; while
		// the painter is live on this node its window sits on a white bar —
		// dark text for plain runes, painted runes keep their color on it.
		// NOT inverse video, which swaps fg/bg and makes an already-red run
		// read as a red background.
		if spanSGR != nil && spanSGR[i] != "" {
			s += spanSGR[i]
		}
		if paintLive && i >= paintLo && i < paintHi {
			if spanSGR == nil || spanSGR[i] == "" {
				s += fgPaintSel
			}
			s += bgPaintSel
		}
		if s != cur {
			b.WriteString(s)
			cur = s
		}
		b.WriteRune(r)
		i++
	}
	if caret >= len(runes) && caret >= 0 {
		// past the last rune: paint one trailing cell; keep a live cmd draft's
		// cursor inside the gray cell until the double-space commits it.
		if cmdDraftStart >= 0 && cmdDraftEnd == len(runes) {
			b.WriteString(cReset + bgCode + cFG + cInvert + " ")
		} else {
			b.WriteString(cReset + cFG + cInvert + " ")
		}
	}
	if it.typ == database.TypeCode {
		b.WriteString(cReset + attrs + " ")
	}
	if it.starred {
		b.WriteString(cReset + " " + cDim + "★") // /star mark, render-only
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
	return suffix
}
