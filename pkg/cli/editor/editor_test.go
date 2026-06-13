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
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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

// TestRowBudgetFallbackBeforeWindowSize keeps the default budget for the window
// before the first WindowSizeMsg sets a real height.
func TestRowBudgetFallbackBeforeWindowSize(t *testing.T) {
	m := newTestModel(80, "one")
	m.height = 0
	if got := m.rowBudget(); got != 18 {
		t.Fatalf("rowBudget with unknown height = %d, want 18 fallback", got)
	}
}
