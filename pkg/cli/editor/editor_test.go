/* Copyright 2025 Lflow Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package editor

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
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
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = stripSGR(l)
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
	if got := last(); !strings.Contains(got, "1/2") {
		t.Fatalf("status bar absent on the last line after ESC-cancel: %q", got)
	}
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
