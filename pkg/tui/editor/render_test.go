package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/mattn/go-runewidth"
)

func TestSourceUUIDFollowsMirrorChain(t *testing.T) {
	orig := &item{uuid: "a", name: "alpha"}
	mir1 := &item{uuid: "b", mirrorOf: "a"} // mirror of the original
	mir2 := &item{uuid: "c", mirrorOf: "b"} // mirror of a mirror
	tr := &tree{byUUID: map[string]*item{"a": orig, "b": mir1, "c": mir2}}

	if got := tr.sourceUUID(orig); got != "a" {
		t.Errorf("non-mirror resolves to itself, got %q", got)
	}
	if got := tr.sourceUUID(mir1); got != "a" {
		t.Errorf("mirror resolves to original, got %q", got)
	}
	// the regression: a mirror of a mirror must resolve to the original,
	// not the intermediate mirror whose name is empty.
	if got := tr.sourceUUID(mir2); got != "a" {
		t.Errorf("mirror of mirror should resolve to original, got %q", got)
	}

	// a new mirror pointed at the resolved source shows the original name.
	newMir := &item{uuid: "d", mirrorOf: tr.sourceUUID(mir2)}
	tr.byUUID["d"] = newMir
	if got := tr.displayName(newMir); got != "alpha" {
		t.Errorf("mirror-of-mirror should display original name, got %q", got)
	}
}

func TestFinderRowNameResolvesMirror(t *testing.T) {
	nodes := map[string]database.Node{
		"a": {UUID: "a", Name: "alpha"},
		"b": {UUID: "b", MirrorOf: "a"}, // mirror of the original
		"c": {UUID: "c", MirrorOf: "b"}, // mirror of a mirror
	}
	resolve := func(uuid string) (database.Node, bool) {
		n, ok := nodes[uuid]
		return n, ok
	}

	if got := finderRowName(nodes["a"], resolve); got != "alpha" {
		t.Errorf("non-mirror keeps its name, got %q", got)
	}
	// the regression: a mirror's name is empty in the database, so the
	// finder must resolve it to the source name rather than show a blank.
	if got := finderRowName(nodes["b"], resolve); got != "alpha - mirror" {
		t.Errorf("mirror should resolve to source name, got %q", got)
	}
	if got := finderRowName(nodes["c"], resolve); got != "alpha - mirror" {
		t.Errorf("mirror of mirror should resolve to original, got %q", got)
	}
}

func TestFinderRowNameMissingSource(t *testing.T) {
	resolve := func(string) (database.Node, bool) { return database.Node{}, false }
	if got := finderRowName(database.Node{UUID: "b", MirrorOf: "gone"}, resolve); got != "(missing) - mirror" {
		t.Errorf("missing source falls back to placeholder, got %q", got)
	}
}

func TestSourceUUIDStopsOnCycle(t *testing.T) {
	a := &item{uuid: "a", mirrorOf: "b"}
	b := &item{uuid: "b", mirrorOf: "a"}
	tr := &tree{byUUID: map[string]*item{"a": a, "b": b}}
	// must terminate rather than loop forever.
	_ = tr.sourceUUID(a)
}

// TestRenderBodyChipsBareDate: a date is recognised by format in plain text —
// no brackets stored — and painted as a chip. The visible text is unchanged
// (no markers to hide) and the chip background appears in the SGR output.
func TestRenderBodyChipsBareDate(t *testing.T) {
	it := &item{typ: database.TypeBullets}

	rendered := renderBody(it, "due 2026-06-14 ok", -1, false, nil, false)
	if got := stripSGR(rendered); got != "due 2026-06-14 ok" {
		t.Errorf("date text must render literally: %q", got)
	}
	if !strings.Contains(rendered, bgPill) {
		t.Errorf("a bare date should be chipped: %q", rendered)
	}
}

// TestRenderBodyDateChipUnaffectedByColor: a date chip is a structural token, so
// the node's /color never bleeds into it — the chip keeps its pill background and
// a neutral foreground even when the node is colored red.
func TestRenderBodyDateChipUnaffectedByColor(t *testing.T) {
	it := &item{typ: database.TypeBullets, style: "color:red"}

	rendered := renderBody(it, "2026-06-14", -1, false, nil, false)
	if !strings.Contains(rendered, bgPill) {
		t.Errorf("date should get the chip background: %q", rendered)
	}
	// the chip's runes use the neutral foreground, not the node's red
	if strings.Contains(rendered, bgPill+styleColorCode["red"]) ||
		strings.Contains(rendered, styleColorCode["red"]+bgPill) {
		t.Errorf("date chip must not take the node color: %q", rendered)
	}
	if !strings.Contains(rendered, cFG+bgPill) {
		t.Errorf("date chip should use the neutral foreground: %q", rendered)
	}
}

// TestRenderBodyAsterisksAreLiteral pins the decision to drop inline markdown:
// styling is a per-node attribute, so ** and * are now ordinary text that must
// not be reinterpreted or stripped, on any row.
func TestRenderBodyAsterisksAreLiteral(t *testing.T) {
	it := &item{typ: database.TypeBullets}

	for _, selected := range []bool{false, true} {
		got := stripSGR(renderBody(it, "say **hello** to *world*", -1, selected, nil, false))
		if got != "say **hello** to *world*" {
			t.Errorf("asterisks must render literally (selected=%v): %q", selected, got)
		}
	}
}

// TestRenderBodyAppliesNodeStyle checks that item.style drives the SGR output:
// bold/italic/underline codes appear and the chosen color recolors the text,
// while the visible characters are untouched.
func TestRenderBodyAppliesNodeStyle(t *testing.T) {
	it := &item{typ: database.TypeBullets, style: "bold,italic,underline,strike,color:blue"}

	rendered := renderBody(it, "hi", 0, false, nil, false)
	for _, code := range []string{cBold, cItalic, cUnderline, cStrike, styleColorCode["blue"]} {
		if !strings.Contains(rendered, code) {
			t.Errorf("style code %q missing from %q", code, rendered)
		}
	}
	if got := stripSGR(rendered); got != "hi" {
		t.Errorf("styling must not change the text: %q", got)
	}
}

// TestRenderBodyStripsStoredControlBytes is the F17 regression: a name already
// in the DB carrying a raw ESC[2J / ESC[H must render as inert text. The render
// boundary strips C0 control and escape bytes so legacy or crafted content can
// never execute a clear-screen or cursor-home, while lflow's own SGR styling
// (terminated by 'm') stays intact.
func TestRenderBodyStripsStoredControlBytes(t *testing.T) {
	it := &item{typ: database.TypeBullets}

	rendered := renderBody(it, "x\x1b[2J\x1b[Hy", -1, false, nil, false)
	// no raw escape or other C0 control byte from the content survives: every
	// ESC left in the output is one lflow itself added, terminated by 'm'.
	inEsc := false
	for _, r := range rendered {
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		switch {
		case r == '\x1b':
			inEsc = true
		case r < 0x20 || r == 0x7F:
			t.Fatalf("control byte %#x leaked to the terminal: %q", r, rendered)
		}
	}
	if inEsc {
		t.Fatalf("a non-SGR escape sequence leaked to the terminal: %q", rendered)
	}
	if got := stripSGR(rendered); got != "x[2J[Hy" {
		t.Fatalf("stored control bytes should render inert: %q", got)
	}
}

func TestRenderBodyBlockCursor(t *testing.T) {
	it := &item{typ: database.TypeBullets}

	// the cursor is a painted cell, never an inserted character
	rendered := renderBody(it, "abc", 1, true, nil, false)
	if got := stripSGR(rendered); got != "abc" {
		t.Errorf("cursor must not insert characters: %q", got)
	}
	if !strings.Contains(rendered, cInvert+"b") {
		t.Errorf("rune under the caret should carry the cursor cell: %q", rendered)
	}

	// past the end it paints one trailing cell
	rendered = renderBody(it, "abc", 3, true, nil, false)
	if got := stripSGR(rendered); got != "abc " {
		t.Errorf("caret at end should paint a trailing cell: %q", got)
	}
	if !strings.Contains(rendered, cInvert+" ") {
		t.Errorf("trailing cursor cell missing: %q", rendered)
	}
}

func TestGlyphForMutedBullets(t *testing.T) {
	_, color := glyphFor(&item{typ: database.TypeBullets})
	if color != cDim {
		t.Errorf("plain bullets should be muted gray, got %q", color)
	}
	_, color = glyphFor(&item{mirrorOf: "x"})
	if color != cDim {
		t.Errorf("mirrors are the muted ◆ — the diamond marks them, got %q", color)
	}
}

func TestRenderBodyLoneAsteriskStaysPlain(t *testing.T) {
	it := &item{typ: database.TypeBullets}

	got := stripSGR(renderBody(it, "2 * 3 yields 6x", -1, false, nil, false))
	if got != "2 * 3 yields 6x" {
		t.Errorf("unpaired asterisk must not be eaten: %q", got)
	}
}

// TestNoteBandLines: a noted node renders a tinted band that shows the note
// text, draws the tree rail to its left when children follow, and is empty for
// a note-less node.
func TestNoteBandLines(t *testing.T) {
	it := &item{uuid: "n", note: "hello world note"}
	tr := &tree{byUUID: map[string]*item{"n": it}, externalNames: map[string]string{}}
	m := &Model{tree: tr}

	lines := m.noteBandLines(row{it: it, depth: 0}, 60, true, -1)
	if len(lines) == 0 {
		t.Fatal("expected band lines for a noted node")
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "\x1b[48") {
		t.Errorf("note band must carry no background fill: %q", joined)
	}
	if !strings.Contains(joined, cItalic) {
		t.Errorf("band should render the note in italic: %q", joined)
	}
	if got := stripSGR(joined); !strings.Contains(got, "hello world note") {
		t.Errorf("band should show the note text: %q", got)
	}
	if !strings.Contains(joined, "│") {
		t.Errorf("band rail should draw │ above children: %q", joined)
	}

	if b := m.noteBandLines(row{it: &item{uuid: "x"}, depth: 0}, 60, true, -1); b != nil {
		t.Errorf("a note-less node should yield no band, got %v", b)
	}
}

// TestNoteBandEditing: with a caret >= 0 the band is the editing surface — it
// draws a block cursor at the caret, and even an empty note yields a band (an
// empty editable strip) so there is somewhere to type.
func TestNoteBandEditing(t *testing.T) {
	it := &item{uuid: "n", note: "abcd"}
	tr := &tree{byUUID: map[string]*item{"n": it}, externalNames: map[string]string{}}
	m := &Model{tree: tr}

	lines := m.noteBandLines(row{it: it, depth: 0}, 60, false, 2)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, cInvert) {
		t.Errorf("editing band should draw a block cursor: %q", joined)
	}
	if got := stripSGR(joined); !strings.Contains(got, "abcd") {
		t.Errorf("editing band should show the note: %q", got)
	}

	// an empty note still yields an editable band with a trailing cursor cell
	empty := &item{uuid: "e", note: ""}
	tr.byUUID["e"] = empty
	eb := m.noteBandLines(row{it: empty, depth: 0}, 60, false, 0)
	if len(eb) == 0 || !strings.Contains(strings.Join(eb, ""), cInvert) {
		t.Errorf("empty note in edit mode should still give a cursor band: %v", eb)
	}
}

func TestRenderBodyChipsDateWithTime(t *testing.T) {
	it := &item{typ: database.TypeBullets}

	rendered := renderBody(it, "ship on 2025-02-11 15:20 sharp", -1, false, nil, false)
	got := stripSGR(rendered)
	if got != "ship on 2025-02-11 15:20 sharp" {
		t.Errorf("date text must render literally: %q", got)
	}
	if !strings.Contains(rendered, bgPill) {
		t.Errorf("date chip background missing: %q", rendered)
	}
}

func TestRenderBodyCodeBlock(t *testing.T) {
	it := &item{typ: database.TypeCode}

	rendered := renderBody(it, "rm -rf ./dist", -1, false, nil, false)
	if !strings.Contains(rendered, bgCode) {
		t.Errorf("code background missing: %q", rendered)
	}
	if got := stripSGR(rendered); got != " rm -rf ./dist " {
		t.Errorf("code block should be padded: %q", got)
	}
}

func TestRenderBodyQuoteBar(t *testing.T) {
	it := &item{typ: database.TypeQuote}

	rendered := renderBody(it, "less is more", -1, false, nil, false)
	if got := stripSGR(rendered); got != glyphQuoteBar+" less is more" {
		t.Errorf("quote bar missing: %q", got)
	}
}

func TestGlyphForHeadingDigits(t *testing.T) {
	cases := []struct {
		typ  string
		want string
	}{
		{database.TypeH1, "1"},
		{database.TypeH2, "2"},
		{database.TypeH3, "3"},
		{database.TypeBullets, glyphOpen},
	}
	for _, tc := range cases {
		glyph, _ := glyphFor(&item{typ: tc.typ})
		if glyph != tc.want {
			t.Errorf("type %s: glyph %q, want %q", tc.typ, glyph, tc.want)
		}
	}
}

func TestWrapLinePlain(t *testing.T) {
	lines := wrapLine("aaa bbb ccc ddd", 7, "")
	want := []string{"aaa bbb", "ccc ddd"}
	if len(lines) != 2 || stripSGR(lines[0]) != want[0] || stripSGR(lines[1]) != want[1] {
		t.Errorf("wrap mismatch: %q", lines)
	}
}

func TestWrapLineHangingIndent(t *testing.T) {
	lines := wrapLine("word word word word", 12, cDim+"   ")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	for i, l := range lines[1:] {
		if !strings.HasPrefix(stripSGR(l), "   ") {
			t.Errorf("continuation %d missing hanging indent: %q", i+1, l)
		}
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > 12 {
			t.Errorf("line %d too wide: %d %q", i, w, l)
		}
	}
}

func TestWrapLineCarriesStyle(t *testing.T) {
	styled := cBold + "one two three four" + cReset
	lines := wrapLine(styled, 9, "")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	if !strings.Contains(lines[1], cBold) {
		t.Errorf("continuation should re-open bold: %q", lines[1])
	}
}

func TestWrapLineCursorDoesNotBleedAcrossWrap(t *testing.T) {
	// the block cursor (reverse-video) lands on the wrap-break space: it must
	// invert exactly that one cell at the trailing edge of the first visual
	// line and never re-open on the continuation line, where it would invert
	// the whole hanging indent.
	styled := "one two three" + cInvert + " " + cReset + "four five six"
	lines := wrapLine(styled, 14, cDim+"   ")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	// exactly one inverted cell survives, on the first visual line.
	if n := strings.Count(lines[0], cInvert); n != 1 {
		t.Errorf("line 0 should carry exactly one block cursor cell, got %d: %q", n, lines[0])
	}
	for i, l := range lines[1:] {
		if strings.Contains(l, cInvert) {
			t.Errorf("continuation %d leaks the block cursor: %q", i+1, l)
		}
	}
	// every emitted segment must close its styling so nothing leaks past a
	// line end.
	if !strings.HasSuffix(lines[0], cReset) {
		t.Errorf("line 0 should end reset: %q", lines[0])
	}
}

func TestWrapLinePastEndCursorNoBlankContinuation(t *testing.T) {
	// The block cursor parked past the end of a node whose last visual line is
	// exactly full appends a trailing inverted space (renderBody's past-end
	// cell). That lone space must not spill onto a fresh continuation line that
	// would carry only the dim rail prefix — keep the inverted cell on the
	// trailing edge of the last text line instead.
	prefix := cDim + "   "
	// "word word" fills width 9 exactly; the trailing cursor space is the only
	// content that would overflow.
	styled := "word word" + cReset + cFG + cInvert + " " + cReset
	lines := wrapLine(styled, 9, prefix)
	for i, l := range lines {
		if stripSGR(l) == "" || strings.TrimSpace(stripSGR(l)) == "" {
			t.Errorf("line %d is a blank rail-only continuation: %q", i, l)
		}
	}
	// the inverted cursor cell must survive exactly once.
	var inverts int
	for _, l := range lines {
		inverts += strings.Count(l, cInvert)
	}
	if inverts != 1 {
		t.Errorf("expected exactly one block cursor cell, got %d: %q", inverts, lines)
	}
	if strings.Contains(lines[len(lines)-1], cInvert) && strings.TrimSpace(stripSGR(lines[len(lines)-1])) == "" {
		t.Errorf("cursor stranded on an otherwise-blank line: %q", lines[len(lines)-1])
	}
}

func TestWrapLineHardBreaksUnbrokenRuns(t *testing.T) {
	lines := wrapLine(strings.Repeat("x", 25), 10, cDim+"  ")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines: %q", lines)
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > 10 {
			t.Errorf("line %d too wide: %d", i, w)
		}
	}
}

func TestWrapLineWideRunes(t *testing.T) {
	// CJK runes are two cells wide; no line may exceed the width
	lines := wrapLine(strings.Repeat("字", 12), 10, "")
	for i, l := range lines {
		if w := visibleWidth(l); w > 10 {
			t.Errorf("line %d too wide: %d %q", i, w, l)
		}
	}
}

func TestWrapLineGraphemeCluster(t *testing.T) {
	// A ZWJ family emoji is a single grapheme cluster two cells wide. Summing
	// its component rune widths (2+0+2+0+2=6) would overcount and wrap early.
	// The cluster plus trailing text fits in width 10, so it must not wrap.
	family := "\U0001F468‍\U0001F469‍\U0001F467" // 👨‍👩‍👧
	if w := runewidth.StringWidth(family); w != 2 {
		t.Fatalf("test premise: family cluster should be 2 cells, got %d", w)
	}
	line := family + " abc" // 2 + 1 + 3 = 6 cells, fits in 10
	lines := wrapLine(line, 10, "")
	if len(lines) != 1 {
		t.Fatalf("ZWJ cluster plus text fits but wrapped: %q", lines)
	}
	if stripSGR(lines[0]) != line {
		t.Errorf("cluster mangled by wrap: got %q want %q", stripSGR(lines[0]), line)
	}
}

func TestVisibleWidthGraphemeCluster(t *testing.T) {
	// visibleWidth must fold a ZWJ cluster into its true terminal width, not
	// sum its component widths.
	family := "\U0001F468‍\U0001F469‍\U0001F467"
	if w := visibleWidth(family); w != 2 {
		t.Errorf("visibleWidth of ZWJ family emoji = %d, want 2", w)
	}
}

func TestWrapLineFillsBulletLineBeforeRun(t *testing.T) {
	// a bulleted node whose body is one long no-space run must fill the
	// bullet line first, not strand "○" alone with the run on line 2
	line := " " + cDim + glyphOpen + cReset + " " + strings.Repeat("x", 80)
	lines := wrapLine(line, 40, cDim+"   ")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	first := stripSGR(lines[0])
	if strings.Count(first, "x") < 30 {
		t.Errorf("bullet line should be filled with the run, got %q", first)
	}
}

func TestWrapLineExpandsTabs(t *testing.T) {
	// runewidth measures '\t' as zero, so without tab expansion wrapLine never
	// sees the width a tab adds and the terminal hard-wraps with no hang
	// indent. A tab-laden line that overflows must wrap, and the continuation
	// must carry the hanging indent.
	line := "ab\tcd\tef\tgh\tij\tkl\tmn\top"
	lines := wrapLine(line, 20, cDim+"   ")
	if len(lines) < 2 {
		t.Fatalf("expected a wrap of the tab-laden line: %q", lines)
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > 20 {
			t.Errorf("line %d too wide: %d %q", i, w, l)
		}
	}
	for i, l := range lines[1:] {
		if !strings.HasPrefix(stripSGR(l), "   ") {
			t.Errorf("continuation %d missing hanging indent: %q", i+1, l)
		}
	}
	// tabs must be gone from the rendered output, expanded to spaces
	for i, l := range lines {
		if strings.ContainsRune(l, '\t') {
			t.Errorf("line %d still contains a raw tab: %q", i, l)
		}
	}
}

func TestExpandTabsToTabStops(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"a\tb", "a       b"},                // col 1 -> next stop is 8
		{"\tx", "        x"},                 // col 0 -> stop at 8
		{"ab\tcd", "ab      cd"},             // col 2 -> stop at 8
		{"12345678\tz", "12345678        z"}, // col 8 -> stop at 16
		{"no tabs", "no tabs"},
	}
	for _, tc := range cases {
		if got := expandTabs(tc.in); got != tc.want {
			t.Errorf("expandTabs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// SGR sequences must not advance the tab-stop column
	if got := expandTabs(cBold + "a" + cReset + "\tb"); !strings.HasSuffix(stripSGR(got), "a       b") {
		t.Errorf("SGR should not shift tab stops: %q", stripSGR(got))
	}
}

func TestContinuationPrefixKeepsRail(t *testing.T) {
	// a depth-1 node with a later sibling and a child: its wrapped
	// continuation must carry │ in its own branch column and under the
	// glyph so the rail stays continuous down to the child.
	r := row{depth: 1, last: false, branch: []bool{true}}
	prefix := continuationPrefix(r, true)
	if got := stripSGR(prefix); got != " │  │ " {
		t.Fatalf("prefix rail mismatch: %q", got)
	}

	line := " " + cDim + connector(r) + cDim + glyphOpen + cReset + " " + strings.Repeat("word ", 12)
	lines := wrapLine(line, 20, prefix)
	if len(lines) < 2 {
		t.Fatalf("expected a wrap: %q", lines)
	}
	if !strings.Contains(lines[1], "│") {
		t.Errorf("continuation should carry the tree rail: %q", lines[1])
	}
}

// TestContinuationKeepsRailAtNarrowWidth is the F1 regression: at terminal
// width 13 (maxLine 12) a depth-1 node's glyph prefix is 6 cols. The old guard
// (hang >= width/2) tripped at 6 >= 6 and zeroed the prefix, dropping the rail
// to column 0. Six text columns remain, so the rail and hanging indent must
// survive — the prefix only collapses when no text columns are left.
func TestContinuationKeepsRailAtNarrowWidth(t *testing.T) {
	r := row{depth: 1, last: false, branch: []bool{true}}
	prefix := continuationPrefix(r, true)
	line := " " + cDim + connector(r) + cDim + glyphOpen + cReset + " " + strings.Repeat("word ", 8)
	lines := wrapLine(line, 12, prefix)
	if len(lines) < 2 {
		t.Fatalf("expected a wrap at width 12: %q", lines)
	}
	for i, l := range lines[1:] {
		if !strings.HasPrefix(l, prefix) {
			t.Errorf("continuation %d dropped the rail prefix at width 12: %q", i+1, l)
		}
		if !strings.Contains(l, "│") {
			t.Errorf("continuation %d missing the tree rail at width 12: %q", i+1, l)
		}
		// body text must still render past the prefix rather than vanish
		if body := strings.TrimSpace(strings.TrimPrefix(stripSGR(l), stripSGR(prefix))); body == "" {
			t.Errorf("continuation %d dropped the text at width 12: %q", i+1, l)
		}
	}
}

func TestRenderBodyCompletedStrikethrough(t *testing.T) {
	it := &item{typ: database.TypeTodo, completedAt: 1}

	rendered := renderBody(it, "done thing", -1, false, nil, false)
	if !strings.Contains(rendered, cStrike) {
		t.Errorf("completed nodes should strike through: %q", rendered)
	}
}
