package editor

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestPasteLinesNormalizesNewlines guards the paste fan-out against empty ghost
// nodes. tmux ONLCR rewrites \r\n into \r\r\n, which a naive replacement would
// split into blank rows; pasteLines must collapse any CR/LF run to one break.
func TestPasteLinesNormalizesNewlines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"tmux onlcr crlf", "line one\r\r\nline two\r\r\nline three", []string{"line one", "line two", "line three"}},
		{"plain crlf", "line one\r\nline two", []string{"line one", "line two"}},
		{"lone cr", "line one\rline two", []string{"line one", "line two"}},
		{"trailing newline", "only\r\n", []string{"only"}},
		{"single line", "just one", []string{"just one"}},
		{"empty", "", nil},
		{"blank only", "\r\n\r\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pasteLines(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pasteLines(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeNameStripsControlBytes guards against pasted ESC sequences
// executing on render. The reproducer pastes "BEFORE\x1b[HAFTER" wrapped in
// bracketed-paste markers; the ESC[H must never reach the terminal as a
// cursor-home, so sanitizeName drops the markers and every C0/DEL control byte
// while leaving the literal printable text intact.
func TestSanitizeNameStripsControlBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"esc cursor home", "BEFORE\x1b[HAFTER", "BEFORE[HAFTER"},
		{"esc clear screen", "x\x1b[2Jy", "x[2Jy"},
		{"bracketed paste markers", "\x1b[200~hello\x1b[201~", "hello"},
		{"null and del", "a\x00b\x7fc", "abc"},
		{"plain text unchanged", "plain text", "plain text"},
		{"unicode preserved", "café — résumé", "café — résumé"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeName(tc.in); got != tc.want {
				t.Fatalf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPasteLinesSanitizesControlBytes confirms the paste path strips the ESC
// sequences from the reproducer before any text becomes a node name.
func TestPasteLinesSanitizesControlBytes(t *testing.T) {
	got := pasteLines("\x1b[200~BEFORE\x1b[HAFTER\x1b[201~")
	want := []string{"BEFORE[HAFTER"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pasteLines = %#v, want %#v", got, want)
	}
}

// TestPasteFanOutSkipsEmptySanitizedLines pastes three logical lines where the
// middle one is only C0/DEL bytes. sanitizeName strips it to empty, so the
// fan-out must not leave a ghost empty-named node between the two real lines.
func TestPasteFanOutSkipsEmptySanitizedLines(t *testing.T) {
	m := newTestModel(80, "root")
	cur := m.tree.root.children[0]
	cur.name = ""
	m.caret = 0

	lines := pasteLines("\x1b[200~hello world\n\x01\x02\x03\x1b\x7f\ngoodbye world\x1b[201~")
	want := []string{"hello world", "", "goodbye world"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("pasteLines = %#v, want %#v", lines, want)
	}

	m.pasteFanOut(cur, lines)

	var names []string
	for _, it := range m.tree.root.children {
		names = append(names, it.name)
	}
	wantNames := []string{"hello world", "goodbye world"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("after fan-out children = %#v, want %#v", names, wantNames)
	}
}

// TestNoteKeySanitizesControlBytes is the F16 regression: pasting text with an
// embedded ESC sequence into note mode must strip the control bytes the same way
// node-name input does, so an ESC[H cursor-home never reaches the terminal and
// recolors the note on render. The literal printable text survives.
func TestNoteKeySanitizesControlBytes(t *testing.T) {
	m := newTestModel(80, "root")
	cur := m.tree.root.children[0]
	m.cursor = 0
	m.mode = modeNote
	m.notePrev = cur.note
	m.caret = 0

	m.handleNoteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("BEFORE\x1b[HAFTER")})

	want := "BEFORE[HAFTER"
	if cur.note != want {
		t.Fatalf("note = %q, want %q", cur.note, want)
	}
	if strings.ContainsAny(cur.note, "\x1b\x00\x7f") {
		t.Fatalf("note retained control bytes: %q", cur.note)
	}
	if m.caret != len([]rune(want)) {
		t.Fatalf("caret = %d, want %d", m.caret, len([]rune(want)))
	}
}

// newTestModel builds a Model over a flat list of sibling names at the given
// width so the wrapped-node Up/Down behaviour can be exercised without a DB.
func newTestModel(width int, names ...string) *Model {
	root := &item{}
	t := &tree{
		root:          root,
		byUUID:        map[string]*item{},
		externalNames: map[string]string{},
	}
	for _, n := range names {
		it := &item{name: n, parent: root}
		root.children = append(root.children, it)
	}
	m := &Model{tree: t, viewStack: []*item{root}, width: width, height: 24}
	m.refreshRows()
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+right":
		return tea.KeyMsg{Type: tea.KeyCtrlRight}
	case "ctrl+left":
		return tea.KeyMsg{Type: tea.KeyCtrlLeft}
	case "alt+right":
		return tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	case "alt+left":
		return tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "alt+e":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e"), Alt: true}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestPageDownKeepsViewOnType: after pgdown peeks past the cursor, typing must
// not yank the window back to pin the cursor at the bottom — sticky follow keeps
// the paged viewTop when the cursor is still on screen.
func TestPageDownKeepsViewOnType(t *testing.T) {
	names := make([]string, 40)
	for i := range names {
		names[i] = fmt.Sprintf("node-%02d", i)
	}
	m := newTestModel(80, names...)
	m.height = 12
	// park the cursor near the bottom of the initial window so a page peeks below
	m.cursor = 8
	_ = m.View() // seed viewTop/viewRows
	before := m.viewTop

	m.handleKey(key("pgdown"))
	_ = m.View()
	paged := m.viewTop
	if paged <= before {
		t.Fatalf("pgdown did not advance viewTop: before=%d after=%d", before, paged)
	}
	// cursor still on screen in the paged window
	if m.cursor < 0 {
		t.Fatal("cursor lost")
	}

	// typing clears the pin; sticky follow must keep the paged window
	m.handleKey(key("x"))
	_ = m.View()
	if m.viewTop != paged {
		t.Fatalf("typing after pgdown moved viewTop from %d to %d", paged, m.viewTop)
	}
	if m.scrolling {
		t.Fatal("typing should clear scrolling pin")
	}
}

func (m *Model) press(s string) {
	mm, _ := m.handleKey(key(s))
	*m = *mm.(*Model)
}

// TestDownWalksWrappedVisualLinesFirst is the F2 regression: with the caret on
// the first visual line of a node that wraps to two lines, Down must move the
// caret down within the same node, not jump to the next node.
func TestDownWalksWrappedVisualLinesFirst(t *testing.T) {
	// width 13 -> maxLine 12, firstCol 3, hang 3. "aaaa bbbb cccc"
	// first visual line fits "aaaa bbbb" (3+9=12), continuation holds "cccc".
	m := newTestModel(13, "aaaa bbbb cccc", "second")
	m.cursor = 0
	m.caret = 0

	starts := m.selectedVisualRows()
	if len(starts) < 2 {
		t.Fatalf("node should wrap to >=2 visual lines, got starts=%v", starts)
	}

	m.press("down")
	if m.cursor != 0 {
		t.Fatalf("Down from first visual line must stay on the node, cursor=%d", m.cursor)
	}
	if line := caretVisualLine(m.selectedVisualRows(), m.caret); line != 1 {
		t.Fatalf("Down should land on visual line 1, got %d (caret=%d)", line, m.caret)
	}

	// from the last visual line, Down crosses to the next node
	m.press("down")
	if m.cursor != 1 {
		t.Fatalf("Down from last visual line must move to next node, cursor=%d", m.cursor)
	}
}

// TestUpWalksWrappedVisualLinesFirst is the symmetric case: Up from a non-first
// visual line stays in the node; from the first line it lands on the previous
// node's last visual line.
func TestUpWalksWrappedVisualLinesFirst(t *testing.T) {
	m := newTestModel(13, "first", "aaaa bbbb cccc")
	m.cursor = 1
	// place caret on the last visual line
	starts := m.selectedVisualRows()
	if len(starts) < 2 {
		t.Fatalf("node should wrap, starts=%v", starts)
	}
	m.caret = len([]rune("aaaa bbbb cccc"))

	m.press("up")
	if m.cursor != 1 {
		t.Fatalf("Up from last visual line must stay on the node, cursor=%d", m.cursor)
	}
	if line := caretVisualLine(m.selectedVisualRows(), m.caret); line != 0 {
		t.Fatalf("Up should land on visual line 0, got %d", line)
	}

	// from the first visual line, Up crosses to the previous node
	m.press("up")
	if m.cursor != 0 {
		t.Fatalf("Up from first visual line must move to previous node, cursor=%d", m.cursor)
	}
}

// TestHomeMovesToStartOfCurrentVisualLine is the F3 regression: on a wrapped
// node Home must move the caret to the first position of the current visual
// line, not the start of the whole node.
func TestHomeMovesToStartOfCurrentVisualLine(t *testing.T) {
	// width 13 -> first visual line "aaaa bbbb", continuation holds "cccc".
	m := newTestModel(13, "aaaa bbbb cccc", "second")
	m.cursor = 0

	starts := m.selectedVisualRows()
	if len(starts) < 2 {
		t.Fatalf("node should wrap to >=2 visual lines, got starts=%v", starts)
	}
	// place the caret on visual line 2, not at its start
	m.caret = len([]rune("aaaa bbbb cccc"))
	if line := caretVisualLine(m.selectedVisualRows(), m.caret); line != 1 {
		t.Fatalf("setup: caret should be on visual line 1, got %d", line)
	}

	m.press("home")
	if m.caret != starts[1] {
		t.Fatalf("Home should move to the start of visual line 2 (%d), got %d", starts[1], m.caret)
	}

	// a second Home stays put; on the first visual line Home reaches 0
	m.caret = starts[0]
	m.press("home")
	if m.caret != 0 {
		t.Fatalf("Home on the first visual line should stay at 0, got %d", m.caret)
	}
}

// TestEndMovesToEndOfCurrentVisualLine is the F4 regression: on a wrapped node
// End must move the caret to the last position of the current visual line, not
// the end of the whole node.
func TestEndMovesToEndOfCurrentVisualLine(t *testing.T) {
	// width 13 -> first visual line "aaaa bbbb", continuation holds "cccc".
	m := newTestModel(13, "aaaa bbbb cccc", "second")
	m.cursor = 0

	starts := m.selectedVisualRows()
	if len(starts) < 2 {
		t.Fatalf("node should wrap to >=2 visual lines, got starts=%v", starts)
	}
	// place the caret on visual line 0
	m.caret = 0
	if line := caretVisualLine(starts, m.caret); line != 0 {
		t.Fatalf("setup: caret should be on visual line 0, got %d", line)
	}

	m.press("end")
	if line := caretVisualLine(m.selectedVisualRows(), m.caret); line != 0 {
		t.Fatalf("End must stay on the current visual line, got line %d (caret=%d)", line, m.caret)
	}
	// the break space is consumed by the wrap, so End lands just before it
	want := starts[1]
	runes := []rune("aaaa bbbb cccc")
	if want > 0 && runes[want-1] == ' ' {
		want--
	}
	if m.caret != want {
		t.Fatalf("End on visual line 0 should land at %d, got %d", want, m.caret)
	}

	// on the final visual line End reaches the node end
	m.caret = starts[len(starts)-1]
	m.press("end")
	if m.caret != len(runes) {
		t.Fatalf("End on the last visual line should reach the node end %d, got %d", len(runes), m.caret)
	}
}

// TestDownSingleLineNodeMovesToNextNode keeps the simple case working: a node
// that does not wrap moves straight to the next node.
func TestDownSingleLineNodeMovesToNextNode(t *testing.T) {
	m := newTestModel(80, "one", "two")
	m.cursor = 0
	m.caret = 0
	if len(m.selectedVisualRows()) != 1 {
		t.Fatalf("short node should be one visual line")
	}
	m.press("down")
	if m.cursor != 1 {
		t.Fatalf("Down on a single-line node should move to next node, cursor=%d", m.cursor)
	}
}

// TestSelectedRowVisibleAtTinyHeight is the F12 regression: in a short terminal
// the row budget must follow the real height (height-2) rather than fall back to
// an 18-line budget that renders far more rows than fit and clips the selection
// off the top. The selected first row must stay on screen.
func TestSelectedRowVisibleAtTinyHeight(t *testing.T) {
	// width 24 -> maxLine 23; "aaaa bbbb cccc dddd" wraps to >1 visual line.
	m := newTestModel(24, "aaaa bbbb cccc dddd", "two", "three", "four", "five")
	m.height = 4
	m.cursor = 0

	if got := m.rowBudget(); got != 2 {
		t.Fatalf("rowBudget at height 4 = %d, want 2 (height-2)", got)
	}

	// the body lines (everything above the bottom bar) must fit the budget and
	// must include the selected row, marked by its red bullet.
	lines := strings.Split(m.View(), "\n")
	body := lines[:len(lines)-1] // last line is the bottom bar
	if len(body) > m.rowBudget() {
		t.Fatalf("body rendered %d lines, exceeds budget %d", len(body), m.rowBudget())
	}
	if !strings.Contains(strings.Join(body, "\n"), cRed) {
		t.Fatalf("selected row's red bullet is off-screen at 24x4")
	}
}

// TestViewNeverExceedsHeightAcrossResize is the F12 regression: the inline
// renderer cannot move the cursor into scrollback, so a frame taller than the
// terminal strands its top lines and they double the outline after a
// shrink-then-grow resize cycle. View must cap every frame — including the
// slash-menu overlay, which appends extra lines past the row budget — at the
// window height so each node renders exactly once.
func TestViewNeverExceedsHeightAcrossResize(t *testing.T) {
	names := make([]string, 12)
	for i := range names {
		names[i] = "node"
	}
	m := newTestModel(60, names...)
	m.mode = modeSlash // the overlay appends slash rows past the body budget

	cycle := []tea.WindowSizeMsg{
		{Width: 60, Height: 24},
		{Width: 10, Height: 4},
		{Width: 60, Height: 24},
	}
	for _, sz := range cycle {
		mm, _ := m.Update(sz)
		m = mm.(*Model)
		if n := strings.Count(m.View(), "\n") + 1; n > m.height {
			t.Fatalf("View rendered %d lines at %dx%d, exceeds height %d",
				n, m.width, m.height, m.height)
		}
	}
}

// TestViewClearsStaleCellsAfterResize is the F6 regression: the inline renderer
// rewrites lines in place without clearing, so growing a frame after a shrink
// (60->40->60) leaves the previous narrower line's trailing cells behind the new
// one — the 40-col and 60-col renders overlaid on the same row. Every emitted
// View line must lead with an erase-to-end-of-line so it clears the row before
// painting. The clear has to lead, not trail: the renderer truncates full-width
// rows to the terminal width and drops any escape bytes past the cut, so a
// trailing clear would be silently discarded on exactly the wide rows that
// overlap a narrower previous frame.
func TestViewClearsStaleCellsAfterResize(t *testing.T) {
	long := "this is a long node name that wraps differently at sixty columns than forty"
	m := newTestModel(60, long)

	cycle := []tea.WindowSizeMsg{
		{Width: 60, Height: 24},
		{Width: 40, Height: 24},
		{Width: 60, Height: 24},
	}
	for _, sz := range cycle {
		mm, _ := m.Update(sz)
		m = mm.(*Model)
	}

	lines := strings.Split(m.View(), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, cClearEOL) {
			t.Fatalf("View line %d does not lead with a clear-to-end-of-line after "+
				"60->40->60; stale cells from the narrower frame would survive the "+
				"renderer's width truncation: %q", i, l)
		}
	}
}

// TestNarrowWidthRendersDeepNodeText is the F13 regression: at width 10
// (maxLine 9) a depth-2 node's glyph prefix is 9 cols, leaving no room for
// text, which trips wrapLine's pathological-width guard. The node text must still render — it
// wraps to continuation lines rather than vanishing — and selected and
// unselected rows must show the same text. Previously the unselected row showed
// only the bullet and the selected row dropped the text to column 0.
func TestNarrowWidthRendersDeepNodeText(t *testing.T) {
	root := &item{}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{},
		externalNames: map[string]string{},
	}
	a := &item{name: "a", parent: root}
	b := &item{name: "b", parent: a}
	c := &item{name: "deep text here", parent: b}
	a.children = []*item{b}
	b.children = []*item{c}
	root.children = []*item{a}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 10, height: 24}
	m.refreshRows()

	if m.rows[2].depth != 2 {
		t.Fatalf("expected a depth-2 node, got depth %d", m.rows[2].depth)
	}

	render := func(cursor int) []string {
		m.cursor = cursor
		lines := m.viewOutline(m.width - 1)
		out := make([]string, 0, len(lines))
		for _, l := range lines {
			s := stripSGR(l)
			if strings.Contains(s, "untitled") {
				break // the status bar (which wraps) carries the cursor counter
			}
			out = append(out, s)
		}
		return out
	}

	unselected := render(0) // cursor on the depth-0 node
	selected := render(2)   // cursor on the depth-2 node

	for state, lines := range map[string][]string{"unselected": unselected, "selected": selected} {
		joined := strings.Join(lines, "\n")
		for _, word := range []string{"deep", "text", "here"} {
			if !strings.Contains(joined, word) {
				t.Errorf("%s render dropped %q at width 10:\n%s", state, word, joined)
			}
		}
		// the depth-2 glyph line must reserve the body column after the bullet
		// rather than collapse it away: the guard zeroes the continuation indent,
		// but the glyph's trailing space must survive so the body begins in a
		// consistent column instead of stranding the bullet alone.
		var glyphLine string
		for _, l := range lines {
			if strings.Contains(l, "○") && strings.Contains(l, "╰─") {
				glyphLine = l
			}
		}
		if glyphLine == "" {
			t.Fatalf("%s render: depth-2 glyph line missing:\n%s", state, joined)
		}
		if !strings.HasSuffix(glyphLine, "○ ") {
			t.Errorf("%s render: glyph line dropped the body column, = %q", state, glyphLine)
		}
	}

	// both states must render the node identically: selection never changes the
	// text or its indent, only the bullet colour (stripped here).
	if !reflect.DeepEqual(unselected, selected) {
		t.Errorf("selected and unselected differ:\n unsel=%q\n sel  =%q", unselected, selected)
	}
}

// TestRowBudgetFallbackBeforeWindowSize keeps the default budget for the window
// before the first WindowSizeMsg sets a real height.
func TestRowBudgetFallbackBeforeWindowSize(t *testing.T) {
	m := newTestModel(80, "one")
	m.height = 0
	if got := m.rowBudget(); got != 18 {
		t.Fatalf("rowBudget with unknown height = %d, want 18 fallback", got)
	}
}

// newTestModelWithChildren builds a single parent that has the given child
// names, so the ctrl+d delete confirm (which only triggers for nodes with
// children) can be exercised without a DB.
func newTestModelWithChildren(width int, parentName string, children ...string) *Model {
	root := &item{}
	t := &tree{root: root, byUUID: map[string]*item{}, externalNames: map[string]string{}}
	parent := &item{name: parentName, parent: root}
	for _, c := range children {
		parent.children = append(parent.children, &item{name: c, parent: parent})
	}
	root.children = append(root.children, parent)
	m := &Model{tree: t, viewStack: []*item{root}, width: width, height: 24}
	m.refreshRows()
	return m
}

// TestConfirmCancelKeepsStatusBarLast is the F6 regression: the inline renderer
// leaves a shrinking frame's old last line in place, so if the delete-confirm
// prompt were the frame's final line, ESC-canceling it (one line shorter) would
// strand the status bar blank until the next keypress. The bottom bar must stay
// every frame's last line, in the confirm and in the cancel that follows it.
func TestConfirmCancelKeepsStatusBarLast(t *testing.T) {
	m := newTestModelWithChildren(40, "parent", "child")
	m.cursor = 0

	m.press("ctrl+d")
	if m.mode != modeConfirm {
		t.Fatalf("ctrl+d on a node with children did not open the confirm")
	}
	last := func() string {
		lines := strings.Split(m.View(), "\n")
		return lines[len(lines)-1]
	}
	if got := last(); !strings.Contains(got, "1/2") || strings.Contains(got, "delete") {
		t.Fatalf("confirm frame's last line is not the status bar: %q", got)
	}

	m.press("esc")
	if m.mode != modeOutline {
		t.Fatalf("esc did not cancel the confirm")
	}
	// Back in outline mode the status bar is the divider between the notes and the
	// always-visible temp panel, so it sits mid-frame, not on the last line. The
	// regression guard is that it is still PRESENT and not stranded blank.
	if !frameHasStatusBar(m.View()) {
		t.Fatalf("status bar absent after ESC-cancel:\n%s", m.View())
	}
}

// frameHasStatusBar reports whether the rendered frame still shows the status bar
// (the "1/2" position counter the two-node test models produce), used by the
// dismiss-regression tests now that the bar renders mid-frame in outline mode.
func frameHasStatusBar(view string) bool {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "1/2") {
			return true
		}
	}
	return false
}

// TestConfirmElidesLongName is the F5 regression: a node name longer than the
// terminal width must not push the node count or the enter/esc hints off-screen.
// The confirm line reserves the suffix and elides the middle of the name so the
// count and the esc-keep hint always survive at a narrow width.
func TestConfirmElidesLongName(t *testing.T) {
	long := strings.Repeat("verylongname", 8) // ~96 cols, well past 60
	m := newTestModelWithChildren(60, long, "child")
	m.cursor = 0

	m.press("ctrl+d")
	if m.mode != modeConfirm {
		t.Fatalf("ctrl+d on a node with children did not open the confirm")
	}

	var confirm string
	for _, l := range strings.Split(m.View(), "\n") {
		if strings.Contains(l, "enter delete") {
			confirm = l
			break
		}
	}
	if confirm == "" {
		t.Fatalf("confirm line not found in view")
	}
	if !strings.Contains(confirm, "2 nodes") {
		t.Fatalf("node count clipped from confirm line: %q", confirm)
	}
	if !strings.Contains(confirm, "esc keep") {
		t.Fatalf("esc-keep hint clipped from confirm line: %q", confirm)
	}
	if !strings.Contains(confirm, "…") {
		t.Fatalf("long name was not elided: %q", confirm)
	}
}

// TestZoomMirrorShowsSourceChildren is the F15 regression: zooming into a mirror
// row pushed the mirror itself, whose children are empty in memory, so the view
// rendered blank. Zoom must resolve the mirror to its source and show the
// original's children.
func TestZoomMirrorShowsSourceChildren(t *testing.T) {
	root := &item{}
	src := &item{uuid: "src", name: "source", parent: root}
	src.children = []*item{
		{uuid: "k1", name: "kid one", parent: src},
		{uuid: "k2", name: "kid two", parent: src},
	}
	mir := &item{uuid: "mir", mirrorOf: "src", parent: root}
	root.children = []*item{src, mir}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"src": src, "k1": src.children[0], "k2": src.children[1], "mir": mir},
		externalNames: map[string]string{},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	// cursor on the mirror row, wherever it renders
	m.cursor = m.rowIndexOf(mir)
	if cur := m.cursorItem(); cur == nil || cur.mirrorOf == "" {
		t.Fatalf("cursor should be on the mirror, got %#v", cur)
	}

	m.press("alt+right")

	if root := m.viewRoot(); root != src {
		t.Fatalf("zoom into mirror should land on the source node, got %#v", root)
	}
	names := []string{}
	for _, r := range m.rows {
		names = append(names, m.tree.displayName(r.it))
	}
	if !reflect.DeepEqual(names, []string{"kid one", "kid two"}) {
		t.Fatalf("zoomed view should show the source children, got %v", names)
	}
}

// TestZoomMirrorMissingSourceIsNoop guards the F15 fix against a missing source:
// a mirror whose source is not in the tree must not push a bad view root.
func TestZoomMirrorMissingSourceIsNoop(t *testing.T) {
	root := &item{}
	mir := &item{uuid: "mir", mirrorOf: "gone", parent: root}
	root.children = []*item{mir}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"mir": mir},
		externalNames: map[string]string{"gone": "(missing)"},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.cursor = 0

	m.press("alt+right")

	if got := len(m.viewStack); got != 1 {
		t.Fatalf("zoom with missing source must not push a view, stack depth=%d", got)
	}
}

// TestDisplayNoteResolvesMirrorToSource is the round-4 note-staleness fix: a
// mirror must show its source's live in-memory note, including an unsaved edit,
// not a stale copy on the mirror row.
func TestDisplayNoteResolvesMirrorToSource(t *testing.T) {
	root := &item{}
	src := &item{uuid: "src", name: "source", note: "saved note", parent: root}
	mir := &item{uuid: "mir", mirrorOf: "src", note: "stale", parent: root}
	root.children = []*item{src, mir}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"src": src, "mir": mir},
		externalNames: map[string]string{},
	}

	if got := tr.displayNote(mir); got != "saved note" {
		t.Fatalf("mirror note should resolve to the source, got %q", got)
	}
	// an unsaved edit to the source must show through the mirror at once
	src.note = "edited but unsaved"
	if got := tr.displayNote(mir); got != "edited but unsaved" {
		t.Fatalf("mirror note should reflect the unsaved source edit, got %q", got)
	}
}

// TestNoteEditOnMirrorEditsSource is the round-4 fix: typing a note while the
// cursor is on a mirror edits the one real node, the source, not a divergent
// copy on the mirror row — same node everywhere.
func TestNoteEditOnMirrorEditsSource(t *testing.T) {
	root := &item{}
	src := &item{uuid: "src", name: "source", parent: root}
	mir := &item{uuid: "mir", mirrorOf: "src", parent: root}
	root.children = []*item{src, mir}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"src": src, "mir": mir},
		externalNames: map[string]string{},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.cursor = m.rowIndexOf(mir)
	m.mode = modeNote
	m.notePrev = m.tree.resolve(m.cursorItem()).note
	m.caret = 0

	m.handleNoteKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})

	if src.note != "hello" {
		t.Fatalf("source note should be edited through the mirror, got %q", src.note)
	}
	if mir.note != "" {
		t.Fatalf("mirror row must not hold a divergent note, got %q", mir.note)
	}
}

// mirrorTree builds root → [source(+two kids), mirror-of-source] for the
// show-through tests.
func mirrorTree() (*tree, *item, *item) {
	root := &item{}
	src := &item{uuid: "src", name: "source", parent: root}
	src.children = []*item{
		{uuid: "k1", name: "kid one", parent: src},
		{uuid: "k2", name: "kid two", parent: src},
	}
	mir := &item{uuid: "mir", mirrorOf: "src", parent: root}
	root.children = []*item{src, mir}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"src": src, "k1": src.children[0], "k2": src.children[1], "mir": mir},
		externalNames: map[string]string{},
	}
	return tr, src, mir
}

// TestMirrorShowsSourceChildrenThrough is the round-4 show-through fix: an
// expanded mirror renders the source's live children as mirrored ◆ rows below
// the mirror reference.
func TestMirrorShowsSourceChildrenThrough(t *testing.T) {
	tr, _, mir := mirrorTree()
	rows := tr.visibleRows(tr.root, false)
	mi := -1
	for i, r := range rows {
		if r.it == mir {
			mi = i
		}
	}
	if mi < 0 {
		t.Fatal("mirror reference row is missing")
	}
	var through []string
	for _, r := range rows[mi+1:] {
		if !r.mirrored {
			t.Fatalf("row shown through the mirror is not marked mirrored: %q", tr.displayName(r.it))
		}
		through = append(through, tr.displayName(r.it))
	}
	if !reflect.DeepEqual(through, []string{"kid one", "kid two"}) {
		t.Fatalf("mirror should show the source children through, got %v", through)
	}
}

// TestCollapsedMirrorHidesChildren is the round-4 rule: a mirror only shows its
// source's children through when it is not collapsed.
func TestCollapsedMirrorHidesChildren(t *testing.T) {
	tr, _, mir := mirrorTree()
	mir.collapsed = true
	rows := tr.visibleRows(tr.root, false)
	// source, kid one, kid two, mirror — nothing renders through the collapsed mirror
	if len(rows) != 4 {
		t.Fatalf("collapsed mirror should hide through-children, got %d rows", len(rows))
	}
	if rows[len(rows)-1].it != mir {
		t.Fatalf("collapsed mirror should be the last row with nothing after it")
	}
}

// TestIndentUnderMirrorAttachesToSource is the round-4 fix: indenting a node
// under a mirror gives the child to the one real node, the source.
func TestIndentUnderMirrorAttachesToSource(t *testing.T) {
	root := &item{}
	src := &item{uuid: "src", name: "source", parent: root}
	mir := &item{uuid: "mir", mirrorOf: "src", parent: root}
	nn := &item{uuid: "nn", name: "newbie", parent: root}
	root.children = []*item{src, mir, nn}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"src": src, "mir": mir, "nn": nn},
		externalNames: map[string]string{},
	}
	if !tr.indent(nn) {
		t.Fatalf("indent under the mirror should succeed")
	}
	if nn.parent != src {
		t.Fatalf("child should attach to the source, got parent %#v", nn.parent)
	}
	if len(src.children) != 1 || src.children[0] != nn {
		t.Fatalf("source should hold the new child, got %v", src.children)
	}
	if len(mir.children) != 0 {
		t.Fatalf("the mirror must not hold a real child, got %v", mir.children)
	}
}

// TestMirrorCycleDoesNotLoop guards the show-through walk: a mirror that points
// at an ancestor renders as a leaf instead of expanding forever.
func TestMirrorCycleDoesNotLoop(t *testing.T) {
	root := &item{}
	a := &item{uuid: "a", name: "a", parent: root}
	cyc := &item{uuid: "cyc", mirrorOf: "a", parent: a}
	a.children = []*item{cyc}
	root.children = []*item{a}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"a": a, "cyc": cyc},
		externalNames: map[string]string{},
	}
	if rows := tr.visibleRows(root, false); len(rows) > 8 {
		t.Fatalf("mirror cycle should terminate, produced %d rows", len(rows))
	}
	if rows := tr.allRows(); len(rows) > 8 {
		t.Fatalf("mirror cycle should terminate in allRows, produced %d rows", len(rows))
	}
}

// rowOf finds the row index showing it within mirror context ctx.
func rowOf(m *Model, it, ctx *item) int {
	for i, r := range m.rows {
		if r.it == it && r.ctx == ctx {
			return i
		}
	}
	return -1
}

// TestCursorStaysLocalIndentingIntoMirror is the round-4 locality fix: Tab-ing a
// node under a mirror gives the child to the source but leaves the cursor on the
// through-row inside the mirror, not on the original's copy.
func TestCursorStaysLocalIndentingIntoMirror(t *testing.T) {
	root := &item{}
	src := &item{uuid: "src", name: "source", parent: root}
	src.children = []*item{{uuid: "ka", name: "kidA", parent: src}}
	mir := &item{uuid: "mir", mirrorOf: "src", parent: root}
	empty := &item{uuid: "e", name: "", parent: root}
	root.children = []*item{src, mir, empty}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"src": src, "ka": src.children[0], "mir": mir, "e": empty},
		externalNames: map[string]string{},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.cursor = m.rowIndexOf(empty)

	m.press("tab")

	if empty.parent != src {
		t.Fatalf("indented node should attach to the source, parent=%#v", empty.parent)
	}
	r := m.rows[m.cursor]
	if r.it != empty || r.ctx != mir {
		t.Fatalf("cursor should stay local to the mirror, got it=%q ctx=%v", tr.displayName(r.it), r.ctx)
	}
}

// TestCursorStaysLocalOutdentingInMirror is the round-4 locality fix: Shift+Tab
// inside a mirror moves the node within the source subtree but keeps the cursor
// on the through-row, not on the original.
func TestCursorStaysLocalOutdentingInMirror(t *testing.T) {
	root := &item{}
	src := &item{uuid: "src", name: "source", parent: root}
	a := &item{uuid: "a", name: "a", parent: src}
	b := &item{uuid: "b", name: "b", parent: a}
	a.children = []*item{b}
	src.children = []*item{a}
	mir := &item{uuid: "mir", mirrorOf: "src", parent: root}
	root.children = []*item{src, mir}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"src": src, "a": a, "b": b, "mir": mir},
		externalNames: map[string]string{},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	if i := rowOf(m, b, mir); i >= 0 {
		m.cursor = i
	} else {
		t.Fatal("through-row of b under the mirror is missing")
	}

	m.press("shift+tab")

	if b.parent != src {
		t.Fatalf("b should outdent within the source, parent=%#v", b.parent)
	}
	r := m.rows[m.cursor]
	if r.it != b || r.ctx != mir {
		t.Fatalf("cursor should stay local to the mirror, got it=%q ctx=%v", tr.displayName(r.it), r.ctx)
	}
}

// TestOutdentBlockedAtMirrorRoot guards the local boundary: a direct child shown
// through a mirror cannot outdent past the mirror's source.
func TestOutdentBlockedAtMirrorRoot(t *testing.T) {
	root := &item{}
	src := &item{uuid: "src", name: "source", parent: root}
	a := &item{uuid: "a", name: "a", parent: src}
	src.children = []*item{a}
	mir := &item{uuid: "mir", mirrorOf: "src", parent: root}
	root.children = []*item{src, mir}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"src": src, "a": a, "mir": mir},
		externalNames: map[string]string{},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	if i := rowOf(m, a, mir); i >= 0 {
		m.cursor = i
	} else {
		t.Fatal("through-row of a under the mirror is missing")
	}

	m.press("shift+tab")

	if a.parent != src {
		t.Fatalf("outdent past the mirror root must be blocked, a.parent=%#v", a.parent)
	}
}

// TestDeleteEmptyLeafThroughMirrorNoPanic reproduces the index-out-of-range
// crash: deleting an empty leaf that is also shown through a mirror drops two
// rows at once, so the cursor nudge after the delete must reclamp or it indexes
// past the now-shorter row set.
func TestDeleteEmptyLeafThroughMirrorNoPanic(t *testing.T) {
	root := &item{}
	src := &item{uuid: "src", name: "ADR", parent: root}
	src.children = []*item{
		{uuid: "a", name: "a", parent: src},
		{uuid: "b", name: "b", parent: src},
		{uuid: "c", name: "c", parent: src},
		{uuid: "e", name: "", parent: src},
	}
	mir := &item{uuid: "mir", mirrorOf: "src", parent: root}
	root.children = []*item{mir, src} // mirror above, original below — as in the repro
	tr := &tree{
		root: root,
		byUUID: map[string]*item{
			"src": src, "a": src.children[0], "b": src.children[1],
			"c": src.children[2], "e": src.children[3], "mir": mir,
		},
		externalNames: map[string]string{},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	empty := src.children[3]
	if i := rowOf(m, empty, nil); i >= 0 { // the original copy, not the through one
		m.cursor = i
	} else {
		t.Fatal("original empty leaf row not found")
	}
	m.caret = 0

	m.press("backspace") // must not panic on the shrunken row set

	if m.cursor < 0 || m.cursor >= len(m.rows) {
		t.Fatalf("cursor out of range after delete: %d / %d", m.cursor, len(m.rows))
	}
	if indexOf(empty) != -1 {
		t.Fatalf("empty leaf should be removed from the tree")
	}
	for _, r := range m.rows {
		if r.it == empty {
			t.Fatalf("deleted node still rendered")
		}
	}
}

// siblingNames lists the names of the view root's direct children.
func siblingNames(m *Model) []string {
	var out []string
	for _, c := range m.tree.root.children {
		out = append(out, c.name)
	}
	return out
}

// TestEnterSplitsNodeAtCaret is the split fix: Enter mid-text keeps the part
// before the caret and moves the part after it into a new sibling.
func TestEnterSplitsNodeAtCaret(t *testing.T) {
	m := newTestModel(80, "asdasd")
	m.cursor = 0
	m.caret = 3 // asd|asd

	m.press("enter")

	if got := siblingNames(m); !reflect.DeepEqual(got, []string{"asd", "asd"}) {
		t.Fatalf("split = %#v, want [asd asd]", got)
	}
	if m.cursor != 1 || m.caret != 0 {
		t.Fatalf("caret should sit at the start of the new node, cursor=%d caret=%d", m.cursor, m.caret)
	}
}

// TestEnterAtStartPushesNodeDown: caret at column 0 pushes the node down intact
// and opens an empty node above it, with the cursor on the empty node.
func TestEnterAtStartPushesNodeDown(t *testing.T) {
	m := newTestModel(80, "asdasd")
	cur := m.tree.root.children[0]
	m.cursor = 0
	m.caret = 0

	m.press("enter")

	if got := siblingNames(m); !reflect.DeepEqual(got, []string{"", "asdasd"}) {
		t.Fatalf("split = %#v, want [\"\" asdasd]", got)
	}
	if m.cursorItem().name != "" {
		t.Fatalf("cursor should be on the new empty node, on %q", m.cursorItem().name)
	}
	if m.tree.root.children[1] != cur {
		t.Fatalf("the original node should be the one pushed down, same item")
	}
}

// TestEnterAtStartKeepsChildren: pushing a node down at caret 0 must take its
// whole subtree with it — the empty node above stays childless.
func TestEnterAtStartKeepsChildren(t *testing.T) {
	root := &item{}
	parent := &item{uuid: "p", name: "Test", parent: root}
	kid := &item{uuid: "k", name: "child", parent: parent}
	parent.children = []*item{kid}
	root.children = []*item{parent}
	tr := &tree{
		root:          root,
		byUUID:        map[string]*item{"p": parent, "k": kid},
		externalNames: map[string]string{},
	}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.cursor = 0 // on "Test"
	m.caret = 0

	m.press("enter")

	if got := siblingNames(m); !reflect.DeepEqual(got, []string{"", "Test"}) {
		t.Fatalf("siblings = %#v, want [\"\" Test]", got)
	}
	if len(parent.children) != 1 || parent.children[0] != kid {
		t.Fatalf("the node must keep its children, got %v", parent.children)
	}
	if m.tree.root.children[0].children != nil {
		t.Fatalf("the new empty node above must be childless")
	}
	if m.cursorItem().name != "" {
		t.Fatalf("cursor should be on the new empty node")
	}
}

// TestEnterAtEndCreatesEmptySibling: caret at the end opens a fresh empty node,
// the common "new line" case.
func TestEnterAtEndCreatesEmptySibling(t *testing.T) {
	m := newTestModel(80, "asdasd")
	m.cursor = 0
	m.caret = len([]rune("asdasd"))

	m.press("enter")

	if got := siblingNames(m); !reflect.DeepEqual(got, []string{"asdasd", ""}) {
		t.Fatalf("split = %#v, want [asdasd \"\"]", got)
	}
	if m.cursor != 1 || m.cursorItem().name != "" {
		t.Fatalf("cursor should be on the new empty node, cursor=%d", m.cursor)
	}
}

// TestSlashBackspaceDismissKeepsStatusBar is the F8 regression: the slash menu
// lists its commands above the status bar, never below it. The inline renderer
// skips repainting a last line that is unchanged from the previous frame, so if
// the bar were the final line with the menu below it, dismissing the menu with
// Backspace on an empty query would shrink the frame without moving the bar's
// row — the renderer would skip the bar, then erase below it, blanking the
// status line for that one frame. The menu sits above the bar so the bar stays
// every frame's last line, and dismiss still leaves a normal status bar.
func TestSlashBackspaceDismissKeepsStatusBar(t *testing.T) {
	m := newTestModel(60, "hello", "world")
	m.cursor = 0
	m.caret = len([]rune("hello"))

	last := func() string {
		lines := strings.Split(m.View(), "\n")
		return lines[len(lines)-1]
	}

	m.press("/")
	if m.mode != modeSlash {
		t.Fatalf("pressing / should open the slash menu, mode=%v", m.mode)
	}
	// even with the menu open the bar is the frame's last line — the menu is
	// listed above it, never below.
	if got := last(); !strings.Contains(got, "1/2") || strings.Contains(got, "/mirror") {
		t.Fatalf("slash frame's last line is not the status bar: %q", got)
	}

	// Backspace on an empty query dismisses the menu back to outline mode.
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	*m = *mm.(*Model)
	if m.mode != modeOutline {
		t.Fatalf("backspace on an empty query should dismiss the menu, mode=%v", m.mode)
	}
	// In outline mode the bar is mid-frame (divider above the temp panel); guard
	// that the dismiss leaves it present, not stranded blank.
	if !frameHasStatusBar(m.View()) {
		t.Fatalf("status bar absent after backspace dismiss:\n%s", m.View())
	}
}

// TestSlashEscapeRemovesTriggeringSlash is the F10 regression: opening the slash
// menu types a "/" into the editable node text, so escaping the menu must strip
// that triggering slash and leave the node name exactly as it was before. A
// literal slash word is only committed when Enter runs an unknown command.
func TestSlashEscapeKeepsTypedText(t *testing.T) {
	m := newTestModel(80, "hello")
	m.cursor = 0
	m.caret = len([]rune("hello"))

	// open the slash menu (inserts "/") and type a query
	m.press("/")
	if m.mode != modeSlash {
		t.Fatalf("pressing / should open the slash menu, mode=%v", m.mode)
	}
	m.press("p")
	m.press("a")

	// escape closes the menu but LEAVES the typed "/pa" as literal text, so you can
	// write things like "/pa" without the menu swallowing them.
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	*m = *mm.(*Model)

	if m.mode != modeOutline {
		t.Fatalf("escape should return to outline mode, mode=%v", m.mode)
	}
	if got := m.cursorItem().name; got != "hello/pa" {
		t.Fatalf("escape must keep the typed text, got %q", got)
	}
}

// TestSlashEscapeDismissKeepsStatusBar is the F9 regression: escaping the slash
// menu shares the one-frame blank-status-bar seam the Backspace dismiss had. The
// menu lists its commands above the bar so the bar stays the frame's last line;
// dismissing via Escape — even after moving the selection within the menu — must
// still leave a normal status bar on that last line, never a blank one.
func TestSlashEscapeDismissKeepsStatusBar(t *testing.T) {
	m := newTestModel(60, "hello", "world")
	m.cursor = 0
	m.caret = len([]rune("hello"))

	last := func() string {
		lines := strings.Split(m.View(), "\n")
		return lines[len(lines)-1]
	}

	m.press("/")
	if m.mode != modeSlash {
		t.Fatalf("pressing / should open the slash menu, mode=%v", m.mode)
	}
	// even with the menu open the bar is the frame's last line — the menu is
	// listed above it, never below.
	if got := last(); !strings.Contains(got, "1/2") || strings.Contains(got, "/mirror") {
		t.Fatalf("slash frame's last line is not the status bar: %q", got)
	}

	// move within the menu before escaping, matching the repro.
	md, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	*m = *md.(*Model)

	// escape dismisses the menu back to outline mode.
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	*m = *mm.(*Model)
	if m.mode != modeOutline {
		t.Fatalf("escape should dismiss the menu, mode=%v", m.mode)
	}
	// In outline mode the bar is mid-frame (divider above the temp panel); guard
	// that the dismiss leaves it present, not stranded blank.
	if !frameHasStatusBar(m.View()) {
		t.Fatalf("status bar absent after escape dismiss:\n%s", m.View())
	}
}

// mergeModel builds root → [A, B(child C)] with uuids, for the merge/undo tests.
func mergeModel() (*Model, *item, *item, *item) {
	root := &item{}
	a := &item{uuid: "a", name: "A", parent: root}
	b := &item{uuid: "b", name: "B", parent: root}
	c := &item{uuid: "c", name: "C", parent: b}
	b.children = []*item{c}
	root.children = []*item{a, b}
	tr := &tree{root: root, byUUID: map[string]*item{"a": a, "b": b, "c": c}, externalNames: map[string]string{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	return m, a, b, c
}

// TestLeftAtStartCrossesToPreviousNode is the round-5 fix: Left at the start of a
// node moves the caret to the end of the previous node.
func TestLeftAtStartCrossesToPreviousNode(t *testing.T) {
	m := newTestModel(80, "Word 1", "Word 2")
	m.cursor = 1
	m.caret = 0

	m.press("left")

	if m.cursor != 0 {
		t.Fatalf("Left at start should cross to the previous node, cursor=%d", m.cursor)
	}
	if want := len([]rune("Word 1")); m.caret != want {
		t.Fatalf("caret should be at the end of the previous node, caret=%d want %d", m.caret, want)
	}
}

// TestRightAtEndCrossesToNextNode: Right at the end of a node moves to the start
// of the next node.
func TestRightAtEndCrossesToNextNode(t *testing.T) {
	m := newTestModel(80, "Word 1", "Word 2")
	m.cursor = 0
	m.caret = len([]rune("Word 1"))

	m.press("right")

	if m.cursor != 1 || m.caret != 0 {
		t.Fatalf("Right at end should cross to the next node start, cursor=%d caret=%d", m.cursor, m.caret)
	}
}

// TestBackspaceMergesIntoPrevious is the round-5 fix: backspace at the start of a
// node merges it into the node above — text appends, children move up.
func TestBackspaceMergesIntoPrevious(t *testing.T) {
	m, a, _, c := mergeModel()
	m.cursor = m.rowIndexOf(a) + 1 // on B
	m.caret = 0

	m.press("backspace")

	if a.name != "AB" {
		t.Fatalf("the previous node should absorb the text, got %q", a.name)
	}
	if len(a.children) != 1 || a.children[0] != c || c.parent != a {
		t.Fatalf("the merged node's children should move to the previous node")
	}
	if cur := m.cursorItem(); cur != a {
		t.Fatalf("cursor should land on the previous node")
	}
	if m.caret != 1 {
		t.Fatalf("caret should sit at the merge point, got %d", m.caret)
	}
	if _, ok := m.tree.byUUID["b"]; ok {
		t.Fatalf("the merged node should be removed")
	}
}

// TestUndoReversesTyping: a run of typed characters is one undo step.
func TestUndoReversesTyping(t *testing.T) {
	m, a, _, _ := mergeModel()
	m.cursor = m.rowIndexOf(a)
	m.caret = len([]rune(a.name))

	for _, r := range "BC" {
		m.press(string(r))
	}
	if a.name != "ABC" {
		t.Fatalf("typing should extend the name, got %q", a.name)
	}

	m.undo()

	if got := m.tree.root.children[0].name; got != "A" {
		t.Fatalf("undo should revert the typed burst, got %q", got)
	}
}

// TestUndoReversesEnter: a structural action is reversible.
func TestUndoReversesEnter(t *testing.T) {
	m, a, _, _ := mergeModel()
	m.cursor = m.rowIndexOf(a)
	m.caret = len([]rune(a.name))
	before := len(m.tree.root.children)

	m.press("enter")
	if len(m.tree.root.children) != before+1 {
		t.Fatalf("enter should add a node, got %d", len(m.tree.root.children))
	}

	m.undo()
	if len(m.tree.root.children) != before {
		t.Fatalf("undo should remove the added node, got %d", len(m.tree.root.children))
	}
}

// TestCtrlArrowJumpsNodes is the round-6 rebind: ctrl+left/right jump between
// nodes (zoom stays on the alt chords).
func TestCtrlArrowJumpsWords(t *testing.T) {
	m := newTestModel(80, "alpha beta gamma", "next")
	m.cursor = 0
	m.caret = 0

	m.press("ctrl+right") // -> start of "beta"
	if want := len("alpha "); m.caret != want {
		t.Fatalf("ctrl+right should land at the start of the next word, caret=%d want %d", m.caret, want)
	}
	m.press("ctrl+right") // -> start of "gamma"
	if want := len("alpha beta "); m.caret != want {
		t.Fatalf("ctrl+right second jump, caret=%d want %d", m.caret, want)
	}
	m.press("ctrl+right") // -> end of text
	if want := len("alpha beta gamma"); m.caret != want {
		t.Fatalf("ctrl+right to end, caret=%d want %d", m.caret, want)
	}
	m.press("ctrl+right") // at end: cross to the next node
	if m.cursor != 1 || m.caret != 0 {
		t.Fatalf("ctrl+right at end should cross to the next node, cursor=%d caret=%d", m.cursor, m.caret)
	}

	m.press("ctrl+left") // at start of "next": cross back to the previous node end
	if m.cursor != 0 || m.caret != len("alpha beta gamma") {
		t.Fatalf("ctrl+left at start should cross to the previous node end, cursor=%d caret=%d", m.cursor, m.caret)
	}
	m.press("ctrl+left") // -> start of "gamma"
	if want := len("alpha beta "); m.caret != want {
		t.Fatalf("ctrl+left back one word, caret=%d want %d", m.caret, want)
	}

	if len(m.viewStack) != 1 {
		t.Fatalf("ctrl arrows must not zoom, viewStack depth=%d", len(m.viewStack))
	}
}

// dupModel builds root → [A, B(child C)] with uuids, for the /duplicate tests.
func dupModel() (*Model, *item, *item, *item) {
	root := &item{}
	a := &item{uuid: "a", name: "A", parent: root}
	b := &item{uuid: "b", name: "B", parent: root}
	c := &item{uuid: "c", name: "C", parent: b}
	b.children = []*item{c}
	root.children = []*item{a, b}
	tr := &tree{root: root, byUUID: map[string]*item{"a": a, "b": b, "c": c}, externalNames: map[string]string{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	return m, a, b, c
}

// TestDuplicateInsertsCopyNextToNode is the /duplicate contract: the cursor
// node and its whole subtree are copied in as the next sibling with fresh
// uuids, content preserved, and the cursor lands on the copy.
func TestDuplicateInsertsCopyNextToNode(t *testing.T) {
	m, _, b, c := dupModel()
	m.cursor = m.rowIndexOf(b)

	mm, _ := m.runSlash("/duplicate")
	*m = *mm.(*Model)

	kids := m.tree.root.children
	if len(kids) != 3 || kids[1] != b || kids[2].name != "B" {
		t.Fatalf("duplicate should insert the copy after the node, got %v", namesOf(kids))
	}
	clone := kids[2]
	if clone.uuid == "" || clone.uuid == b.uuid {
		t.Fatalf("clone needs a fresh uuid, got %q", clone.uuid)
	}
	if clone.parent != m.tree.root {
		t.Fatalf("clone parent should be root")
	}
	if !clone.isNew {
		t.Fatalf("clone must be marked new so it persists on save")
	}
	if len(clone.children) != 1 || clone.children[0].name != "C" || clone.children[0].uuid == c.uuid {
		t.Fatalf("clone's subtree must be copied with fresh uuids, got %v", namesOf(clone.children))
	}
	if clone.children[0].parent != clone {
		t.Fatalf("cloned child must reparent onto the clone")
	}
	if got := m.cursorItem(); got != clone {
		t.Fatalf("cursor should land on the duplicate, got %q", got.name)
	}
}

// TestDuplicateRootIsNoop: the root has no sibling slot, so duplicating it is
// refused without mutating the tree. (The view root is never a selectable row,
// so this exercises tree.duplicate directly -- the guard runSlash relies on.)
func TestDuplicateRootIsNoop(t *testing.T) {
	m, _, _, _ := dupModel()
	before := len(m.tree.root.children)

	clone, err := m.tree.duplicate(m.tree.root)
	if err == nil || clone != nil {
		t.Fatalf("duplicating the root must fail, got clone=%v err=%v", clone, err)
	}
	if len(m.tree.root.children) != before {
		t.Fatalf("duplicating the root must not add nodes, got %d", len(m.tree.root.children))
	}
}

// TestUndoRevertsDuplicate: a duplicate is a single undoable structural step.
func TestUndoRevertsDuplicate(t *testing.T) {
	m, _, b, _ := dupModel()
	m.cursor = m.rowIndexOf(b)
	before := len(m.tree.root.children)

	mm, _ := m.runSlash("/duplicate")
	*m = *mm.(*Model)
	if len(m.tree.root.children) != before+1 {
		t.Fatalf("duplicate should add a node, got %d", len(m.tree.root.children))
	}

	m.undo()
	if len(m.tree.root.children) != before {
		t.Fatalf("undo should remove the duplicate, got %d", len(m.tree.root.children))
	}
}

func namesOf(items []*item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.name)
	}
	return out
}

// TestAltEnterTogglesComplete: alt+enter is the /complete shortcut.
func TestAltEnterTogglesComplete(t *testing.T) {
	m := newTestModel(40, "task")
	m.cursor = 0
	if m.cursorItem().completedAt != 0 {
		t.Fatalf("fresh node should be incomplete, got completedAt=%d", m.cursorItem().completedAt)
	}

	m.press("alt+enter")
	if m.cursorItem().completedAt == 0 {
		t.Fatal("alt+enter should complete the cursor node")
	}
	if !m.unsaved {
		t.Fatal("completing should mark unsaved")
	}

	m.press("alt+enter")
	if m.cursorItem().completedAt != 0 {
		t.Fatalf("second alt+enter should uncomplete, got completedAt=%d", m.cursorItem().completedAt)
	}

	// one undo rewinds the uncomplete → node is completed again
	m.undo()
	if m.cursorItem().completedAt == 0 {
		t.Fatal("undo after uncomplete should restore completed")
	}
}

// TestFilterHidesCompleted: /hide:complete drops completed nodes from the outline.
func TestFilterHidesCompleted(t *testing.T) {
	m := newTestModel(40, "open", "done", "also open")
	m.tree.root.children[1].completedAt = 1
	m.refreshRows()
	if len(m.rows) != 3 {
		t.Fatalf("all three should show before filter, got %d", len(m.rows))
	}

	mm, _ := m.runSlash("/hide:complete")
	*m = *mm.(*Model)
	if !m.hideCompleted {
		t.Fatal("/hide:complete should set hideCompleted")
	}
	if len(m.rows) != 2 {
		t.Fatalf("filter should hide the completed node, got %d rows", len(m.rows))
	}
	for _, r := range m.rows {
		if r.it.completedAt > 0 {
			t.Fatalf("completed node still visible: %q", r.it.name)
		}
	}
	if m.flash != "hiding completed" {
		t.Fatalf("flash = %q", m.flash)
	}

	// toggle back
	mm, _ = m.runSlash("/hide:complete")
	*m = *mm.(*Model)
	if m.hideCompleted || len(m.rows) != 3 {
		t.Fatalf("second /hide:complete should restore all rows, hide=%v n=%d", m.hideCompleted, len(m.rows))
	}
}

// TestTodoRetypeTogglesToBullet: re-picking Todo on a Todo reverts to Bullet.
func TestTodoRetypeTogglesToBullet(t *testing.T) {
	m := newTestModel(40, "task")
	m.cursor = 0
	typeSource{}.onSelect(m, pickerItem{value: database.TypeTodo})
	if m.cursorItem().typ != database.TypeTodo {
		t.Fatalf("first pick should set todo, got %q", m.cursorItem().typ)
	}
	typeSource{}.onSelect(m, pickerItem{value: database.TypeTodo})
	if m.cursorItem().typ != database.TypeBullets {
		t.Fatalf("re-picking todo should toggle to bullets, got %q", m.cursorItem().typ)
	}
	// other types still just set
	typeSource{}.onSelect(m, pickerItem{value: database.TypeQuote})
	if m.cursorItem().typ != database.TypeQuote {
		t.Fatalf("quote should stick, got %q", m.cursorItem().typ)
	}
}
