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

// Package editor implements the inline scrollback-mode outline editor:
// black background, muted gray ○/●/◆/□ glyphs and connectors plus 1/2/3
// heading digits, the selected row marked by its glyph turning red, a block
// cursor that inverts the cell beneath it, a minimal dim bottom bar, a
// type-to-filter slash menu under the bar, and a full-panel fuzzy finder for
// /mirror /mirror_to /move_to /go. It never enters the alternate screen.
package editor

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/cli/context"
	"github.com/lflow/lflow/pkg/cli/database"
	"github.com/mattn/go-runewidth"
	"github.com/pkg/errors"
)

type mode int

const (
	modeOutline mode = iota
	modeSlash
	modeFinder
	modeNote
	modeConfirm // inline delete confirmation for nodes with children
)

type finderAction int

const (
	actMirrorHere finderAction = iota
	actMirrorTo
	actMoveTo
	actGo
)

type slashCommand struct {
	name string
	desc string
}

var slashCommands = []slashCommand{
	{"/mirror", "mirror a node here via the fuzzy finder"},
	{"/mirror_to", "mirror THIS node somewhere else"},
	{"/copy_link", "copy this node's link — paste on another node to mirror"},
	{"/move_to", "move this node under another node"},
	{"/go", "jump the editor to another node"},
	{"/complete", "toggle done"},
	{"/h1", "make heading 1"},
	{"/h2", "make heading 2"},
	{"/h3", "make heading 3"},
	{"/todo", "make todo"},
	{"/code", "make code"},
	{"/quote", "make quote"},
	{"/bullet", "back to a plain bullet"},
	{"/note", "edit this node's note"},
}

// Model is the bubbletea model for the editor.
type Model struct {
	db   *database.DB
	tree *tree

	viewStack []*item // zoom stack; last is the current view root
	cursor    int     // index into visibleRows
	caret     int     // rune index in the edited field
	rows      []row   // cached visible rows

	width  int
	height int

	mode        mode
	slashQuery  string
	slashSel    int
	slashStart  int  // rune index of the "/" that opened the menu
	slashInline bool // the slash and query are typed into the node text
	finderQuery string
	finderSel   int
	finderHits  []database.Node
	finderAct   finderAction
	notePrev    string // note backup for esc in note mode

	escPending bool
	unsaved    bool
	quitting   bool
	flash      string // one-shot status for the bottom bar, cleared on keypress
	err        error

	saved struct {
		written int
	}

	// background workflowy-mirror scheduler (see sync.go)
	sched scheduler
}

func (m *Model) viewRoot() *item { return m.viewStack[len(m.viewStack)-1] }

func (m *Model) refreshRows() {
	m.rows = m.tree.visibleRows(m.viewRoot())
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// rowBudget is how many screen lines the outline body may occupy: the terminal
// height minus the two chrome lines (bottom bar plus its breathing room). When
// the height is known we honour it down to a single line so the selected row
// always stays on screen at tiny sizes; the default only covers the window
// before the first WindowSizeMsg sets a real height.
func (m *Model) rowBudget() int {
	if m.height <= 0 {
		return 18
	}
	return max(1, m.height-2)
}

// viewport returns the [start,end) slice of m.rows currently visible on
// screen. Rendering and the background scheduler share it so they agree on
// which anchors count as "visible".
func (m *Model) viewport() (start, end int) {
	maxRows := m.rowBudget()
	if m.cursor >= maxRows {
		start = m.cursor - maxRows + 1
	}
	end = start + maxRows
	if end > len(m.rows) {
		end = len(m.rows)
	}
	return start, end
}

func (m *Model) cursorItem() *item {
	if len(m.rows) == 0 {
		return nil
	}
	return m.rows[m.cursor].it
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return m.schedulerInit() }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case syncTickMsg:
		return m, m.onSyncTick(time.Time(msg))
	case syncDoneMsg:
		m.onSyncDone(msg)
		return m, nil
	}
	return m, nil
}

func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	m.flash = "" // one-shot: whatever this key does sets the next status

	// esc-esc quits from outline mode
	if m.mode == modeOutline && key == "esc" {
		if m.escPending {
			return m.quit()
		}
		m.escPending = true
		return m, nil
	}
	if key != "esc" {
		m.escPending = false
	}

	switch m.mode {
	case modeSlash:
		return m.handleSlashKey(k)
	case modeFinder:
		return m.handleFinderKey(k)
	case modeNote:
		return m.handleNoteKey(k)
	case modeConfirm:
		return m.handleConfirmKey(k)
	}

	switch key {
	case "ctrl+q", "ctrl+c":
		return m.quit()
	case "ctrl+s":
		written, err := m.tree.save()
		if err != nil {
			m.err = err
			return m.quit()
		}
		m.saved.written += written
		m.unsaved = false
		return m, nil
	case "enter":
		cur := m.cursorItem()
		var it *item
		var err error
		if cur == nil {
			it, err = m.tree.insertFirstChild(m.viewRoot())
		} else if cur.parent == m.viewRoot() || cur.parent != nil {
			it, err = m.tree.insertSiblingAfter(cur)
		}
		if err != nil {
			m.err = err
			return m.quit()
		}
		if it != nil {
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.rowIndexOf(it)
			m.caret = 0
		}
		return m, nil
	case "tab":
		if cur := m.cursorItem(); cur != nil && m.tree.indent(cur) {
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.rowIndexOf(cur)
		}
		return m, nil
	case "shift+tab":
		if cur := m.cursorItem(); cur != nil && m.tree.outdent(cur, m.viewRoot()) {
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.rowIndexOf(cur)
		}
		return m, nil
	case "ctrl+@", "ctrl+space":
		if cur := m.cursorItem(); cur != nil && len(cur.children) > 0 {
			cur.collapsed = !cur.collapsed
			m.refreshRows()
		}
		return m, nil
	// ctrl+backspace arrives as ctrl+h in most terminals
	case "ctrl+d", "ctrl+shift+backspace", "ctrl+backspace", "ctrl+h":
		if cur := m.cursorItem(); cur != nil {
			if len(cur.children) > 0 {
				// children go with the node: confirm inline first
				m.mode = modeConfirm
			} else {
				m.deleteNode(cur)
			}
		}
		return m, nil
	case "ctrl+t":
		// convert the detected time phrase into a date pill
		if cur := m.cursorItem(); cur != nil && cur.mirrorOf == "" {
			if d := detectDate(cur.name, m.caret, time.Now()); d != nil {
				runes := []rune(cur.name)
				pill := d.pill()
				cur.name = string(runes[:d.start]) + pill + string(runes[d.end:])
				m.caret = d.start + len([]rune(pill))
				m.unsaved = true
			}
		}
		return m, nil
	// every alt+arrow chord has a ctrl twin: terminals like windows
	// terminal grab alt+arrows for pane focus and never deliver them
	case "alt+shift+up", "ctrl+shift+up", "ctrl+alt+up":
		if cur := m.cursorItem(); cur != nil && m.tree.move(cur, -1) {
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.rowIndexOf(cur)
		}
		return m, nil
	case "alt+shift+down", "ctrl+shift+down", "ctrl+alt+down":
		if cur := m.cursorItem(); cur != nil && m.tree.move(cur, 1) {
			m.unsaved = true
			m.refreshRows()
			m.cursor = m.rowIndexOf(cur)
		}
		return m, nil
	case "alt+right", "ctrl+right":
		// zoom into the cursor node — leaves too: the view starts empty
		// and typing adds the first child
		if cur := m.cursorItem(); cur != nil {
			m.viewStack = append(m.viewStack, cur)
			m.cursor = 0
			m.caret = 0
			m.refreshRows()
		}
		return m, nil
	case "alt+left", "alt+backspace", "ctrl+left":
		// zoom back out
		if len(m.viewStack) > 1 {
			zoomed := m.viewRoot()
			m.viewStack = m.viewStack[:len(m.viewStack)-1]
			m.refreshRows()
			m.cursor = m.rowIndexOf(zoomed)
			m.caret = 0
		}
		return m, nil
	case "alt+up", "ctrl+up":
		// collapse the cursor node
		if cur := m.cursorItem(); cur != nil && len(cur.children) > 0 && !cur.collapsed {
			cur.collapsed = true
			m.refreshRows()
			m.cursor = m.rowIndexOf(cur)
		}
		return m, nil
	case "alt+down", "ctrl+down":
		// expand the cursor node
		if cur := m.cursorItem(); cur != nil && len(cur.children) > 0 && cur.collapsed {
			cur.collapsed = false
			m.refreshRows()
			m.cursor = m.rowIndexOf(cur)
		}
		return m, nil
	case "up":
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line > 0 {
			// walk up one visual line of the wrapped node first
			goal := m.caretColumn(starts, line)
			m.caret = m.caretAtColumn(starts, line-1, goal)
		} else if m.cursor > 0 {
			// from the first visual line, cross to the previous node and land
			// on its last visual line, keeping the horizontal column
			goal := m.caretColumn(starts, 0)
			m.cursor--
			prev := m.selectedVisualRows()
			m.caret = m.caretAtColumn(prev, len(prev)-1, goal)
			m.clampCaret()
		}
		return m, nil
	case "down":
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line < len(starts)-1 {
			// walk down one visual line of the wrapped node first
			goal := m.caretColumn(starts, line)
			m.caret = m.caretAtColumn(starts, line+1, goal)
		} else if m.cursor < len(m.rows)-1 {
			// from the last visual line, cross to the next node and land on its
			// first visual line, keeping the horizontal column
			goal := m.caretColumn(starts, line)
			m.cursor++
			m.caret = m.caretAtColumn(m.selectedVisualRows(), 0, goal)
			m.clampCaret()
		}
		return m, nil
	case "left":
		if m.caret > 0 {
			m.caret--
		}
		return m, nil
	case "right":
		if cur := m.cursorItem(); cur != nil && m.caret < len([]rune(cur.name)) {
			m.caret++
		}
		return m, nil
	case "home":
		// move to the first position of the current visual line, not the start
		// of the whole node: a wrapped node has several visual lines
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		m.caret = starts[line]
		return m, nil
	case "end":
		// move to the last position of the current visual line, not the end of
		// the whole node: a wrapped node has several visual lines. On the final
		// visual line this is the node end.
		cur := m.cursorItem()
		if cur == nil {
			return m, nil
		}
		runes := []rune(cur.name)
		starts := m.selectedVisualRows()
		line := caretVisualLine(starts, m.caret)
		if line+1 >= len(starts) {
			m.caret = len(runes)
			return m, nil
		}
		// stop before the next line's start; a space consumed by the wrap break
		// lands the caret just before it, mirroring the on-break-space render.
		end := starts[line+1]
		if end > 0 && end <= len(runes) && runes[end-1] == ' ' {
			end--
		}
		m.caret = end
		return m, nil
	case "backspace":
		cur := m.cursorItem()
		if cur == nil || cur.mirrorOf != "" {
			return m, nil
		}
		if m.caret > 0 {
			runes := []rune(cur.name)
			cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
			m.unsaved = true
		} else if cur.name == "" && len(cur.children) == 0 {
			// backspace on an empty leaf removes it
			idx := m.cursor
			m.tree.remove(cur)
			m.unsaved = true
			m.refreshRows()
			if idx > 0 {
				m.cursor = idx - 1
			}
			if c := m.cursorItem(); c != nil {
				m.caret = len([]rune(c.name))
			}
		}
		return m, nil
	}

	// printable input (space arrives as KeySpace, not KeyRunes)
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type = tea.KeyRunes
		k.Runes = []rune{' '}
	}
	if k.Type == tea.KeyRunes && len(k.Runes) > 0 && !k.Alt {
		cur := m.cursorItem()
		if cur == nil {
			// empty view: create the first node
			it, err := m.tree.insertFirstChild(m.viewRoot())
			if err != nil {
				m.err = err
				return m.quit()
			}
			m.refreshRows()
			m.cursor = m.rowIndexOf(it)
			m.caret = 0
			cur = it
		}

		// "/" opens the slash menu anywhere in the row. On editable rows it
		// is typed into the text and stripped when a command runs, so esc
		// leaves a literal slash behind.
		if string(k.Runes) == "/" && !k.Paste {
			m.mode = modeSlash
			m.slashQuery = ""
			m.slashSel = 0
			m.slashInline = cur.mirrorOf == ""
			if m.slashInline {
				runes := []rune(cur.name)
				cur.name = string(runes[:m.caret]) + "/" + string(runes[m.caret:])
				m.slashStart = m.caret
				m.caret++
				m.unsaved = true
			}
			return m, nil
		}

		if cur.mirrorOf != "" {
			return m, nil // mirrors are edited at their original
		}

		text := string(k.Runes)
		if k.Paste {
			if lines := pasteLines(text); len(lines) > 1 {
				return m.pasteFanOut(cur, lines)
			} else if len(lines) == 1 {
				text = lines[0]
			} else {
				text = ""
			}
		}

		runes := []rune(cur.name)
		ins := []rune(text)
		cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
		m.caret += len(ins)
		m.unsaved = true
		m.maybeLinkToMirror(cur)
		return m, nil
	}

	return m, nil
}

// pasteLines normalizes pasted text into one line per logical row. tmux ONLCR
// rewrites \r\n into \r\r\n, so a naive \r\n then \r replacement would yield
// blank rows; instead we strip every \r before splitting on \n so any CR/LF run
// collapses to a single break, then drop the trailing blank from a final
// newline. Empty interior lines are preserved as the source intended.
func pasteLines(text string) []string {
	text = newlineRunRe.ReplaceAllString(text, "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = sanitizeName(lines[i])
	}
	return lines
}

// newlineRunRe matches any run of CR/LF as a single line break, so tmux ONLCR's
// \r\r\n collapses to one break instead of spawning empty ghost rows.
var newlineRunRe = regexp.MustCompile(`[\r\n]+`)

// bracketedPasteRe matches the bracketed-paste markers a terminal wraps around
// pasted text. A paste that itself contains the literal start/end sequences
// would otherwise smuggle them into a node name and toggle paste mode on render.
var bracketedPasteRe = regexp.MustCompile(`\x1b\[20[01]~`)

// sanitizeName makes pasted or inserted text safe to store as a node name and
// echo back to the terminal. It drops bracketed-paste markers and every C0
// control byte (0x00-0x1F) plus DEL (0x7F), so an embedded ESC[H/ESC[2J never
// executes as a cursor-home or clear-screen when the outline is rendered.
// Newlines are the paste fan-out separator and are handled before this step;
// tabs are normalized on the F3 path, so no control bytes should survive here.
func sanitizeName(text string) string {
	text = bracketedPasteRe.ReplaceAllString(text, "")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, text)
}

// pasteFanOut spreads a multiline paste over the outline: the first line
// continues the current row at the caret, every following line becomes a new
// sibling below it. Lines are already sanitized by pasteLines; a line that
// sanitized to empty (only C0/DEL bytes) creates no sibling so the paste never
// leaves a ghost empty-named node between two real lines.
func (m *Model) pasteFanOut(cur *item, lines []string) (tea.Model, tea.Cmd) {
	runes := []rune(cur.name)
	cur.name = string(runes[:m.caret]) + lines[0] + string(runes[m.caret:])

	last := cur
	for _, l := range lines[1:] {
		if l == "" {
			continue
		}
		it, err := m.tree.insertSiblingAfter(last)
		if err != nil {
			m.err = err
			return m.quit()
		}
		it.name = l
		last = it
	}

	m.unsaved = true
	m.refreshRows()
	m.cursor = m.rowIndexOf(last)
	m.caret = len([]rune(last.name))
	m.maybeLinkToMirror(last)
	return m, nil
}

var mirrorLinkRe = regexp.MustCompile(`^lflow://node/([0-9a-fA-F-]{6,})$`)

// maybeLinkToMirror turns a row whose whole text is a node link into a
// mirror of that node: paste a copied link, get a mirror.
func (m *Model) maybeLinkToMirror(it *item) {
	trimmed := strings.TrimSpace(it.name)
	if !strings.HasPrefix(trimmed, "lflow://") {
		return
	}
	match := mirrorLinkRe.FindStringSubmatch(trimmed)
	if match == nil {
		return
	}
	uuid := match[1]
	if uuid == it.uuid {
		m.flash = "a node cannot mirror itself"
		return
	}
	target, err := database.GetNode(m.db, uuid)
	if err != nil {
		m.flash = "link points at no node"
		return
	}

	target = m.resolveSourceNode(target)
	it.name = ""
	it.mirrorOf = target.UUID
	m.caret = 0
	if _, inTree := m.tree.byUUID[target.UUID]; !inTree {
		m.tree.externalNames[target.UUID] = target.Name
	}
	m.unsaved = true
	m.flash = fmt.Sprintf("mirrored %q", target.Name)
}

// resolveSourceNode follows a node's mirror chain to its ultimate
// non-mirror original, so a new mirror points at the real node and shows
// its name. A node that is not a mirror is returned unchanged.
func (m *Model) resolveSourceNode(n database.Node) database.Node {
	seen := map[string]bool{}
	for n.MirrorOf != "" && !seen[n.UUID] {
		seen[n.UUID] = true
		orig, err := database.GetNode(m.db, n.MirrorOf)
		if err != nil {
			break
		}
		n = orig
	}
	return n
}

// copyToClipboard puts s on the system clipboard via OSC 52, written to
// stderr so it bypasses the bubbletea renderer owning stdout.
func copyToClipboard(s string) {
	seq := osc52.New(s)
	if os.Getenv("TMUX") != "" {
		seq = seq.Tmux()
	}
	_, _ = seq.WriteTo(os.Stderr)
}

// deleteNode removes the node and its subtree from the tree.
func (m *Model) deleteNode(it *item) {
	m.tree.remove(it)
	m.unsaved = true
	m.refreshRows()
	m.caret = 0
}

// subtreeSize counts the node and everything below it.
func subtreeSize(it *item) int {
	n := 1
	for _, c := range it.children {
		n += subtreeSize(c)
	}
	return n
}

// handleConfirmKey answers the inline delete confirmation.
func (m *Model) handleConfirmKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter", "y":
		m.mode = modeOutline
		if cur := m.cursorItem(); cur != nil {
			m.deleteNode(cur)
		}
	case "esc", "n":
		m.mode = modeOutline
	}
	return m, nil
}

// selectedVisualRows returns the rune offsets at which each soft-wrapped
// visual line of the selected node begins, measured with the same width and
// hanging indent the renderer wraps the row at. A node that fits on one line
// returns a single-element slice, so Up/Down can tell when the caret is on the
// first or last visual line and only then cross to another node.
func (m *Model) selectedVisualRows() []int {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return []int{0}
	}
	r := m.rows[m.cursor]
	glyph, _ := glyphFor(r.it)
	name := m.tree.displayName(r.it)
	maxLine := m.width - 1
	firstCol := visibleWidth(" " + connector(r) + glyph + " ")
	below := m.cursor+1 < len(m.rows) && m.rows[m.cursor+1].depth > r.depth
	hang := visibleWidth(continuationPrefix(r, below))
	return visualRows(name, maxLine, firstCol, hang)
}

// caretColumn returns the caret's display column within its visual line: the
// width of the runes between the line's start offset and the caret.
func (m *Model) caretColumn(starts []int, line int) int {
	cur := m.cursorItem()
	if cur == nil || line < 0 || line >= len(starts) {
		return 0
	}
	runes := []rune(m.tree.displayName(cur))
	start := starts[line]
	if m.caret < start {
		return 0
	}
	end := m.caret
	if end > len(runes) {
		end = len(runes)
	}
	return visibleWidth(string(runes[start:end]))
}

// caretAtColumn returns the caret index on the given visual line nearest the
// target display column, clamped to that line's runes. It is the inverse of
// caretColumn and keeps vertical movement on a stable horizontal column.
func (m *Model) caretAtColumn(starts []int, line, col int) int {
	cur := m.cursorItem()
	if cur == nil || len(starts) == 0 {
		return m.caret
	}
	if line < 0 {
		line = 0
	}
	if line >= len(starts) {
		line = len(starts) - 1
	}
	runes := []rune(m.tree.displayName(cur))
	start := starts[line]
	end := len(runes)
	if line+1 < len(starts) {
		// stop before the next line's start; the trailing space that wrapped
		// is consumed by the break, so land on the last rune of this line
		end = starts[line+1]
	}
	w := 0
	for i := start; i < end; i++ {
		rw := runewidth.RuneWidth(runes[i])
		if w+rw > col {
			return i
		}
		w += rw
	}
	return end
}

func (m *Model) clampCaret() {
	if cur := m.cursorItem(); cur != nil {
		if n := len([]rune(cur.name)); m.caret > n {
			m.caret = n
		}
	}
}

func (m *Model) rowIndexOf(it *item) int {
	for i, r := range m.rows {
		if r.it == it {
			return i
		}
	}
	return 0
}

func (m *Model) filteredSlash() []slashCommand {
	if m.slashQuery == "" {
		return slashCommands
	}
	var ret []slashCommand
	for _, c := range slashCommands {
		if strings.Contains(c.name, strings.ToLower(m.slashQuery)) {
			ret = append(ret, c)
		}
	}
	return ret
}

// stripSlashText removes the typed "/query" from the node text and parks the
// caret where the slash was. Called before a slash command runs.
func (m *Model) stripSlashText() {
	if !m.slashInline {
		return
	}
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	runes := []rune(cur.name)
	end := m.slashStart + 1 + len([]rune(m.slashQuery))
	if end > len(runes) {
		end = len(runes)
	}
	cur.name = string(runes[:m.slashStart]) + string(runes[end:])
	m.caret = m.slashStart
}

func (m *Model) handleSlashKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()

	switch k.String() {
	case "esc":
		// keep the typed text: this is how a literal slash is written
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.slashSel > 0 {
			m.slashSel--
		}
		return m, nil
	case "down":
		if m.slashSel < len(m.filteredSlash())-1 {
			m.slashSel++
		}
		return m, nil
	case "backspace":
		if qr := []rune(m.slashQuery); len(qr) > 0 {
			m.slashQuery = string(qr[:len(qr)-1])
			m.slashSel = 0
			if m.slashInline && cur != nil && m.caret > 0 {
				runes := []rune(cur.name)
				cur.name = string(runes[:m.caret-1]) + string(runes[m.caret:])
				m.caret--
			}
		} else {
			if m.slashInline && cur != nil {
				m.stripSlashText()
			}
			m.mode = modeOutline
		}
		return m, nil
	case "enter":
		cmds := m.filteredSlash()
		if m.slashSel < len(cmds) {
			m.stripSlashText()
			return m.runSlash(cmds[m.slashSel].name)
		}
		m.mode = modeOutline
		return m, nil
	}

	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		m.slashQuery += string(k.Runes)
		m.slashSel = 0
		if m.slashInline && cur != nil {
			runes := []rune(cur.name)
			ins := []rune(string(k.Runes))
			cur.name = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
			m.caret += len(ins)
		}
		// nothing matches anymore: it was ordinary text, keep it as typed
		if len(m.filteredSlash()) == 0 {
			m.mode = modeOutline
		}
	}
	return m, nil
}

func (m *Model) runSlash(name string) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}

	setLayout := func(layout string) {
		cur.layout = layout
		m.unsaved = true
	}

	switch name {
	case "/h1":
		setLayout(database.LayoutH1)
	case "/h2":
		setLayout(database.LayoutH2)
	case "/h3":
		setLayout(database.LayoutH3)
	case "/todo":
		setLayout(database.LayoutTodo)
	case "/code":
		setLayout(database.LayoutCode)
	case "/quote":
		setLayout(database.LayoutQuote)
	case "/bullet":
		setLayout(database.LayoutBullets)
	case "/complete":
		if cur.completedAt > 0 {
			cur.completedAt = 0
		} else {
			cur.completedAt = time.Now().Unix()
		}
		m.unsaved = true
	case "/note":
		m.mode = modeNote
		m.notePrev = cur.note
		m.caret = len([]rune(cur.note))
	case "/mirror":
		m.openFinder(actMirrorHere)
	case "/mirror_to":
		m.openFinder(actMirrorTo)
	case "/copy_link":
		// a mirror's link points at the original: same node everywhere
		target := cur.uuid
		if cur.mirrorOf != "" {
			target = cur.mirrorOf
		}
		copyToClipboard("lflow://node/" + target)
		m.flash = "link copied — paste it on another node to mirror"
	case "/move_to":
		m.openFinder(actMoveTo)
	case "/go":
		m.openFinder(actGo)
	}
	return m, nil
}

func (m *Model) handleNoteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()
	if cur == nil {
		m.mode = modeOutline
		return m, nil
	}
	switch k.String() {
	case "esc":
		cur.note = m.notePrev
		m.mode = modeOutline
		m.caret = len([]rune(cur.name))
		return m, nil
	case "enter":
		m.mode = modeOutline
		m.unsaved = true
		m.caret = len([]rune(cur.name))
		return m, nil
	case "backspace":
		runes := []rune(cur.note)
		if m.caret > 0 && m.caret <= len(runes) {
			cur.note = string(runes[:m.caret-1]) + string(runes[m.caret:])
			m.caret--
		}
		return m, nil
	case "left":
		if m.caret > 0 {
			m.caret--
		}
		return m, nil
	case "right":
		if m.caret < len([]rune(cur.note)) {
			m.caret++
		}
		return m, nil
	}
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		runes := []rune(cur.note)
		ins := []rune(string(k.Runes))
		cur.note = string(runes[:m.caret]) + string(ins) + string(runes[m.caret:])
		m.caret += len(ins)
	}
	return m, nil
}

func (m *Model) openFinder(act finderAction) {
	m.mode = modeFinder
	m.finderAct = act
	m.finderQuery = ""
	m.finderSel = 0
	m.finderHits = nil
	m.refreshFinder()
}

func (m *Model) refreshFinder() {
	// an empty query matches everything, recent first: the picker starts
	// full and narrows as you type
	var hits []database.Node
	var err error
	if strings.TrimSpace(m.finderQuery) == "" {
		hits, err = database.RecentNodes(m.db, 100)
	} else {
		hits, err = database.SearchNodes(m.db, m.finderQuery, true)
	}
	if err != nil {
		m.finderHits = nil
		return
	}
	// the node being acted on is never a valid target
	cur := m.cursorItem()
	var filtered []database.Node
	for _, h := range hits {
		if cur != nil && h.UUID == cur.uuid {
			continue
		}
		filtered = append(filtered, h)
	}
	m.finderHits = filtered
	if m.finderSel >= len(m.finderHits) {
		m.finderSel = 0
	}
}

func (m *Model) handleFinderKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "up":
		if m.finderSel > 0 {
			m.finderSel--
		}
		return m, nil
	case "down":
		if m.finderSel < len(m.finderHits)-1 {
			m.finderSel++
		}
		return m, nil
	case "backspace":
		if len(m.finderQuery) > 0 {
			m.finderQuery = m.finderQuery[:len(m.finderQuery)-1]
			m.refreshFinder()
		}
		return m, nil
	case "enter":
		if m.finderSel < len(m.finderHits) {
			return m.runFinder(m.finderHits[m.finderSel])
		}
		m.mode = modeOutline
		return m, nil
	}
	if k.Type == tea.KeySpace && !k.Alt {
		k.Type, k.Runes = tea.KeyRunes, []rune{' '}
	}
	if k.Type == tea.KeyRunes && !k.Alt {
		m.finderQuery += string(k.Runes)
		m.refreshFinder()
	}
	return m, nil
}

func (m *Model) runFinder(target database.Node) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}

	switch m.finderAct {
	case actMirrorHere:
		target = m.resolveSourceNode(target)
		if cur.name == "" && cur.mirrorOf == "" && len(cur.children) == 0 {
			// the empty node where "/" was typed becomes the mirror
			cur.mirrorOf = target.UUID
		} else {
			it, err := m.tree.insertSiblingAfter(cur)
			if err != nil {
				m.err = err
				return m.quit()
			}
			it.mirrorOf = target.UUID
			m.refreshRows()
			m.cursor = m.rowIndexOf(it)
		}
		if _, inTree := m.tree.byUUID[target.UUID]; !inTree {
			m.tree.externalNames[target.UUID] = target.Name
		}
		m.unsaved = true
	case actMirrorTo:
		// the mirror instance lives outside this subtree: write it directly
		if err := m.mirrorToDB(cur, target); err != nil {
			m.err = err
			return m.quit()
		}
	case actMoveTo:
		if targetItem, inTree := m.tree.byUUID[target.UUID]; inTree {
			if m.tree.reparent(cur, targetItem) {
				m.unsaved = true
				m.refreshRows()
				m.cursor = m.rowIndexOf(cur)
			}
		} else {
			// moving out of the open subtree: persist everything, then move in db
			if err := m.moveToDB(cur, target); err != nil {
				m.err = err
				return m.quit()
			}
		}
	case actGo:
		// save, then reopen on the target
		if _, err := m.tree.save(); err != nil {
			m.err = err
			return m.quit()
		}
		t, err := loadTree(m.db, target.UUID)
		if err != nil {
			m.err = err
			return m.quit()
		}
		m.tree = t
		m.viewStack = []*item{t.root}
		m.cursor = 0
		m.caret = 0
		m.unsaved = false
	}

	m.refreshRows()
	return m, nil
}

func (m *Model) mirrorToDB(cur *item, target database.Node) error {
	if _, err := m.tree.save(); err != nil {
		return err
	}
	m.unsaved = false

	newIt, err := m.tree.newItem()
	if err != nil {
		return err
	}
	delete(m.tree.byUUID, newIt.uuid) // not part of this tree

	rank, err := database.NextRank(m.db, target.UUID)
	if err != nil {
		return err
	}
	n := database.Node{
		UUID:       newIt.uuid,
		ParentUUID: target.UUID,
		Rank:       rank,
		Layout:     database.LayoutBullets,
		MirrorOf:   m.tree.sourceUUID(cur),
		Dirty:      true,
	}
	return n.Insert(m.db)
}

func (m *Model) moveToDB(cur *item, target database.Node) error {
	if _, err := m.tree.save(); err != nil {
		return err
	}
	m.unsaved = false

	rank, err := database.NextRank(m.db, target.UUID)
	if err != nil {
		return err
	}
	if _, err := m.db.Exec("UPDATE nodes SET parent_uuid = ?, rank = ?, dirty = 1 WHERE uuid = ?",
		target.UUID, rank, cur.uuid); err != nil {
		return errors.Wrap(err, "moving node")
	}

	// detach from the in-memory tree without tombstoning
	if idx := indexOf(cur); idx >= 0 {
		cur.parent.children = append(cur.parent.children[:idx], cur.parent.children[idx+1:]...)
	}
	m.refreshRows()
	return nil
}

func (m *Model) quit() (tea.Model, tea.Cmd) {
	if m.err == nil {
		written, err := m.tree.save()
		if err != nil {
			m.err = err
		} else {
			m.saved.written += written
		}
	}
	m.quitting = true
	return m, tea.Quit
}

// View implements tea.Model.
func (m *Model) View() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	maxLine := width - 1 // never touch the last column: deferred-wrap desync

	if m.quitting {
		if m.err != nil {
			return ""
		}
		// the final frame is what the terminal scrollback keeps: the whole
		// outline, fully expanded, styled exactly like the live editor. The
		// trailing newline matters: the renderer erases the last line of the
		// final frame on shutdown, so give it an empty one to eat.
		return strings.Join(m.finalView(maxLine), "\n") + "\n"
	}

	var lines []string

	if m.mode == modeFinder {
		lines = m.viewFinder(maxLine)
	} else {
		lines = m.viewOutline(maxLine)
	}

	// The inline renderer (no alt screen) can only move the cursor back over the
	// lines of the previous frame — it cannot reach into scrollback. A frame
	// taller than the terminal therefore strands its top lines: on the next
	// flush the renderer clears only what it last rendered, leaving the overflow
	// behind, which is what doubles the outline across a shrink-then-grow resize.
	// Cap every frame at the window height so each node renders exactly once.
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}

	return strings.Join(lines, "\n")
}

// finalView renders the complete tree with glyphs and connectors but no
// cursor, caret or bottom bar. Long rows wrap.
func (m *Model) finalView(maxLine int) []string {
	var lines []string
	allRows := m.tree.allRows()
	for i, r := range allRows {
		glyph, glyphColor := glyphFor(r.it)
		name := m.tree.displayName(r.it)
		line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + renderBody(r.it, name, -1, false) + m.layoutSuffix(r.it)
		below := i+1 < len(allRows) && allRows[i+1].depth > r.depth
		lines = append(lines, wrapLine(line, maxLine, continuationPrefix(r, below))...)
	}
	return lines
}

func (m *Model) viewOutline(maxLine int) []string {
	var lines []string

	rows := m.rows
	if len(rows) == 0 {
		lines = append(lines, cDim+" empty — type to add a node"+cReset)
	}

	// render every row to its wrapped lines first: the viewport then works
	// in screen lines, so wrapped rows never push the cursor off screen
	groups := make([][]string, len(rows))
	for i, r := range rows {
		it := r.it
		selected := i == m.cursor

		glyph, glyphColor := glyphFor(it)
		if selected {
			glyphColor = cRed
		}
		name := m.tree.displayName(it)

		caret := -1
		if selected && m.mode != modeNote && it.mirrorOf == "" {
			caret = m.caret
		}
		body := renderBody(it, name, caret, selected)

		line := " " + cDim + connector(r) + glyphColor + glyph + cReset + " " + body + m.layoutSuffix(it)

		if selected && m.mode == modeNote {
			line += cDim + "  note: " + cReset + cFG + withCaret(it.note, m.caret) + cReset
		}

		below := i+1 < len(rows) && rows[i+1].depth > r.depth
		groups[i] = wrapLine(line, maxLine, continuationPrefix(r, below))
	}

	maxRows := m.rowBudget()
	cursorStart, cursorEnd := 0, 0
	var flat []string
	for i, g := range groups {
		if i == m.cursor {
			cursorStart = len(flat)
			cursorEnd = len(flat) + len(g) - 1
		}
		flat = append(flat, g...)
	}
	start := 0
	if cursorEnd >= maxRows {
		start = cursorEnd - maxRows + 1
	}
	if cursorStart < start {
		start = cursorStart
	}
	end := start + maxRows
	if end > len(flat) {
		end = len(flat)
	}
	lines = append(lines, flat[start:end]...)

	// The delete confirm sits above the status line, not below it: the inline
	// renderer leaves a shrinking frame's old last line in place, so if the
	// confirm prompt were the final line, canceling it (one line shorter) would
	// strand the status bar blank until the next keypress repainted. Keeping the
	// bottomBar as every frame's last line means ESC-cancel restores it at once.
	if m.mode == modeConfirm {
		if cur := m.cursorItem(); cur != nil {
			// Build suffix-first: the count and keybind hints must never be clipped,
			// so reserve their width plus the fixed " delete " prefix and quotes,
			// then elide the middle of the name to fit whatever room is left.
			prefix := " " + cRed + "delete " + cReset
			suffix := cDim + fmt.Sprintf(" · %s · enter delete · esc keep", nodeNoun(subtreeSize(cur))) + cReset
			room := maxLine - visibleWidth(prefix) - visibleWidth(suffix) - 2 // 2 for the quotes
			name := elideMiddle(m.tree.displayName(cur), room)
			line := prefix + cYellow + fmt.Sprintf("%q", name) + cReset + suffix
			lines = append(lines, clip(line, maxLine))
		}
	}

	lines = append(lines, m.bottomBar(maxLine))

	if m.mode == modeSlash {
		for i, c := range m.filteredSlash() {
			mark := "  "
			if i == m.slashSel {
				mark = cAccent + "▸ " + cReset
			}
			line := " " + mark + cFG + fmt.Sprintf("%-11s", c.name) + cDim + " " + c.desc + cReset
			lines = append(lines, clip(line, maxLine))
		}
	}

	return lines
}

func (m *Model) bottomBar(maxLine int) string {
	total := len(m.rows)
	pos := m.cursor + 1
	if len(m.rows) == 0 {
		pos = 0
	}
	state := ""
	if m.unsaved {
		state = " · unsaved"
	}
	if m.sched.inFlight {
		// state, not a countdown: only shown while a sync is actually running
		state += " · syncing"
	}
	if m.flash != "" {
		state += " · " + m.flash
	}
	// offer the date-pill conversion while a time phrase sits under the cursor
	if m.mode == modeOutline {
		if cur := m.cursorItem(); cur != nil && cur.mirrorOf == "" {
			if d := detectDate(cur.name, m.caret, time.Now()); d != nil {
				state += fmt.Sprintf(" · ctrl+t %q → %s", d.phrase, d.display())
			}
		}
	}
	title := m.tree.displayName(m.viewRoot())
	if title == "" {
		title = "untitled"
	}
	bar := fmt.Sprintf(" %s · %d/%d%s", title, pos, total, state)
	return clip(cDim+bar+cReset, maxLine)
}

// finderRowName resolves the name shown for a finder row. A mirror node
// carries an empty name in the database, so follow its mirror_of chain to
// the source node and show that name, suffixed to mark it a mirror. resolve
// looks up a node by uuid; a missing source falls back to a placeholder.
func finderRowName(n database.Node, resolve func(string) (database.Node, bool)) string {
	if n.MirrorOf == "" {
		return n.Name
	}
	seen := map[string]bool{n.UUID: true}
	cur := n.MirrorOf
	for {
		src, ok := resolve(cur)
		if !ok {
			return "(missing) · mirror"
		}
		if src.MirrorOf == "" || seen[cur] {
			return src.Name + " · mirror"
		}
		seen[cur] = true
		cur = src.MirrorOf
	}
}

func (m *Model) viewFinder(maxLine int) []string {
	var lines []string

	labels := map[finderAction]string{
		actMirrorHere: "/mirror",
		actMirrorTo:   "/mirror_to",
		actMoveTo:     "/move_to",
		actGo:         "/go",
	}
	hints := map[finderAction]string{
		actMirrorHere: "enter mirror at cursor",
		actMirrorTo:   "enter mirror this node there",
		actMoveTo:     "enter move this node there",
		actGo:         "enter open node",
	}

	query := cDim + " " + labels[m.finderAct] + " " + cFG + withCaret(m.finderQuery, len([]rune(m.finderQuery))) + cReset
	lines = append(lines, clip(query, maxLine))

	maxResults := m.height - 4
	if maxResults < 3 {
		maxResults = 8
	}
	shown := m.finderHits
	overflow := 0
	if len(shown) > maxResults {
		overflow = len(shown) - maxResults
		shown = shown[:maxResults]
	}

	for i, h := range shown {
		mark := "   "
		if i == m.finderSel {
			mark = cAccent + " ▸ " + cReset
		}
		count, err := database.CountSubtree(m.db, h.UUID)
		if err != nil {
			count = 1
		}
		name := finderRowName(h, func(uuid string) (database.Node, bool) {
			n, err := database.GetNode(m.db, uuid)
			return n, err == nil
		})
		line := mark + cFG + fmt.Sprintf("%-28s", name) + cDim + fmt.Sprintf(" %d nodes", count) + cReset
		lines = append(lines, clip(line, maxLine))
	}
	if overflow > 0 {
		lines = append(lines, clip(cDim+fmt.Sprintf("   … %d more", overflow)+cReset, maxLine))
	}
	if len(shown) == 0 {
		lines = append(lines, cDim+"   no matches"+cReset)
	}

	lines = append(lines, "")
	lines = append(lines, clip(cDim+" "+hints[m.finderAct]+" · esc back to outline"+cReset, maxLine))
	lines = append(lines, m.bottomBar(maxLine))

	return lines
}

// Run opens the inline node editor on the given node.
func Run(ctx context.DnoteCtx, nodeUUID string) error {
	t, err := loadTree(ctx.DB, nodeUUID)
	if err != nil {
		return errors.Wrap(err, "loading node tree")
	}

	m := &Model{
		db:        ctx.DB,
		tree:      t,
		viewStack: []*item{t.root},
	}
	m.initScheduler(ctx)
	m.refreshRows()

	p := tea.NewProgram(m) // inline: no alt screen
	final, err := p.Run()
	if err != nil {
		return errors.Wrap(err, "running editor")
	}

	fm, ok := final.(*Model)
	if !ok {
		fm = m
	}
	if fm.err != nil {
		return fm.err
	}

	total, _ := fm.tree.stats()
	name := fm.tree.displayName(fm.tree.root)
	// muted gray throughout, only the node name in yellow
	fmt.Printf("%s→ saved %s%q%s · %s · %s written%s\n",
		cDim, cYellow, name, cDim,
		nodeNoun(total), nodeNoun(fm.saved.written), cReset)

	return nil
}

func nodeNoun(n int) string {
	if n == 1 {
		return "1 node"
	}
	return fmt.Sprintf("%d nodes", n)
}
