package editor

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Flash is a flash.nvim-style "jump / act without moving the cursor": alt+s
// labels every visible row's available actions, then the next keystrokes pick
// one. Labels are a single letter for the common case, two letters (e.g. "hj")
// once a screen overflows the alphabet. Typing a label narrows live — the matched
// prefix grays out while the rest stays lit — and a completed label fires.
//
// It stays inside the outline: no alt-screen, no separate mode plumbing beyond
// modeFlash. Two actions are universal (jump onto a row; fold a row with
// children); inline affordances that normally run via alt+r (cmd chips and
// Workflowy handles) contribute actions from the row content; the rest
// are contributed BY THE NODE TYPE via the registry's flashActions hook — so a
// type declares its own labelled actions (verb, color, handler) in one place, and
// flash surfaces them with no switch here to edit. A type that sets no hook falls
// back to inference from its run / view / expand hooks (see flashActionsFor).

// flashAction is one action a node type contributes to flash: a short verb, a
// chip color (so like actions read alike on screen), and the handler run after
// the cursor moves onto the node.
type flashAction struct {
	verb  string
	color string
	do    func(m *Model, it *item) tea.Cmd
}

// flashTarget is one labelled action sitting on a visible row — a flashAction
// pinned to a row and assigned a key label. do == nil means "jump only" (moving
// the cursor onto the row is the whole action).
type flashTarget struct {
	row   int    // index into m.rows
	label string // assigned key sequence, e.g. "a" or "hj"; "" = unlabelled (over capacity)
	verb  string // short word shown beside the label (jump / run / expand / fold …)
	color string // chip color (SGR)
	do    func(m *Model, it *item) tea.Cmd
}

// flashActionsFor returns content-driven alt+r actions plus node-type-contributed
// actions for an item. A type with a flashActions hook controls only its
// type-specific list; cmd chips and Workflowy handles are cross-cutting
// row content and are still offered. Otherwise actions are inferred from run /
// view / expand hooks, so existing types need no changes. (jump and fold are
// added universally in enterFlash, not here.)
func (m *Model) flashActionsFor(it *item) []flashAction {
	nt := typeOf(it.typ)
	out := m.flashInlineRunActions(it)
	if nt.flashActions != nil {
		return append(out, nt.flashActions(m, it)...)
	}
	if nt.run != nil {
		out = append(out, flashAction{verb: "run", color: cGreen, do: nt.run})
	}
	if nt.view != nil || nt.expand != nil {
		out = append(out, flashAction{verb: "expand", color: cCyan, do: flashExpandDo})
	}
	// alt+r also refreshes any Workflowy handle row, not just /type workflowy roots.
	if nt.run == nil {
		if _, ok := m.wfMap[it.uuid]; ok {
			out = append(out, flashAction{verb: "run", color: cGreen, do: runWF})
		}
	}
	return out
}

// flashInlineRunActions mirrors the content-sensitive alt+r path for runnable
// command chips, which registry inference cannot see.
func (m *Model) flashInlineRunActions(it *item) []flashAction {
	if it == nil {
		return nil
	}
	var out []flashAction
	for _, sp := range anchorSpans([]rune(it.name)) {
		c, ok := m.chips[sp.id]
		if !ok || c.Kind != chipKindCmd {
			continue
		}
		chip := c
		out = append(out, flashAction{verb: "run $", color: cGreen, do: func(m *Model, it *item) tea.Cmd {
			return m.runCmdChip(chip)
		}})
	}
	return out
}

// flashExpandDo is the default "expand" handler: focus an inline view, else run
// an action-only expand (e.g. voice play). Mirrors the alt+e handler.
func flashExpandDo(m *Model, it *item) tea.Cmd {
	if v := nodeViewOf(it); v != nil {
		if v.Enter(m, it) {
			m.focused = true
			m.focusScroll = 0
		}
		return nil
	}
	if e := typeOf(it.typ).expand; e != nil {
		return e(m, it)
	}
	return nil
}

// flashZoom zooms the view into a node — the alt+right action, mirror-aware.
// fireFlash has already moved the cursor onto the row, so it is the current
// node. A mirror carries no children in memory; zoom into its source so the
// original's children render (see the alt+right handler in keys.go).
func flashZoom(m *Model, it *item) tea.Cmd {
	cur := it
	if cur.mirrorOf != "" {
		src, ok := m.tree.byUUID[m.tree.sourceUUID(cur)]
		if !ok {
			return nil
		}
		cur = src
	}
	m.viewStack = append(m.viewStack, cur)
	m.cursor = 0
	m.caret = 0
	m.refreshRows()
	return nil
}

// flashFold toggles a node's collapsed state, keeping the cursor on it.
// fireFlash has already moved the cursor onto the row, so the same one-cycle-
// level-at-a-time steps as alt+up/down apply. It is the universal fold handler.
func flashFold(m *Model, it *item) tea.Cmd {
	if len(m.tree.childItems(it)) == 0 || m.cursor >= len(m.rows) {
		return nil
	}
	if it.collapsed || m.rows[m.cursor].cycled {
		m.expandStep()
	} else {
		m.collapseStep()
	}
	return nil
}

// flashAlphabet is the label key pool, home-row first so the common (single
// letter) labels fall under the strongest fingers.
var flashAlphabet = []rune("asdfghjklqwertyuiopzxcvbnm")

// flashLabels returns n prefix-free labels: single letters until the alphabet
// runs out, then two-letter labels under the remaining letters. It keeps as many
// single-letter labels as possible (fewest keystrokes) while staying prefix-free,
// so no single label is also the start of a two-letter one.
func flashLabels(n int) []string {
	if n <= 0 {
		return nil
	}
	k := len(flashAlphabet)
	if n <= k {
		out := make([]string, n)
		for i := 0; i < n; i++ {
			out[i] = string(flashAlphabet[i])
		}
		return out
	}
	// capacity(s) = s + (k-s)*k, decreasing in s. Pick the largest s (most single
	// letters) whose capacity still covers n. s letters stay single; the rest
	// become two-letter prefixes, each pairing with the whole alphabet.
	s := (k*k - n) / (k - 1)
	if s < 0 {
		s = 0
	}
	if s > k {
		s = k
	}
	out := make([]string, 0, n)
	for i := 0; i < s && len(out) < n; i++ {
		out = append(out, string(flashAlphabet[i]))
	}
	for pi := s; pi < k && len(out) < n; pi++ {
		for ci := 0; ci < k && len(out) < n; ci++ {
			out = append(out, string(flashAlphabet[pi])+string(flashAlphabet[ci]))
		}
	}
	return out
}

// enterFlash builds the labelled targets for every visible row and switches into
// modeFlash. A node with no rows, or while a view is focused, is a no-op.
func (m *Model) enterFlash() {
	if m.focused || len(m.rows) == 0 {
		return
	}
	var ts []flashTarget
	for i, r := range m.rows {
		it := r.it
		// jumping onto the row the cursor already sits on is a no-op — skip it, but
		// still offer that row's other actions.
		if i != m.cursor {
			ts = append(ts, flashTarget{row: i, verb: "jump", color: cAccent})
		}
		// node-type-contributed actions (run / expand / play / …), from the registry
		for _, a := range m.flashActionsFor(it) {
			ts = append(ts, flashTarget{row: i, verb: a.verb, color: a.color, do: a.do})
		}
		// zoom into the row is universal (alt+right), offered on every row
		// including the one under the cursor.
		ts = append(ts, flashTarget{row: i, verb: "zoom", color: cYellow, do: flashZoom})
		if len(m.tree.childItems(it)) > 0 {
			verb := "fold"
			if it.collapsed || r.cycled {
				verb = "unfold"
			}
			ts = append(ts, flashTarget{row: i, verb: verb, color: cMagenta, do: flashFold})
		}
	}
	if len(ts) == 0 {
		return
	}

	// Assign labels nearest-cursor-first so the closest targets earn the
	// single-keystroke labels; ties keep build order (jump before the rest).
	order := make([]int, len(ts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return flashDist(ts[order[a]].row, m.cursor) < flashDist(ts[order[b]].row, m.cursor)
	})
	labels := flashLabels(len(ts))
	for rank, idx := range order {
		if rank < len(labels) {
			ts[idx].label = labels[rank]
		}
	}

	m.flashTargets = ts
	m.flashInput = ""
	m.mode = modeFlash
}

func flashDist(row, cursor int) int {
	if row < cursor {
		return cursor - row
	}
	return row - cursor
}

// exitFlash drops back to normal outline editing, clearing the labels.
func (m *Model) exitFlash() {
	m.mode = modeOutline
	m.flashTargets = nil
	m.flashInput = ""
}

// handleFlashKey consumes a keystroke while labels are showing: esc cancels,
// backspace un-types a letter, and a label letter narrows or fires. A keystroke
// that leads to no label is ignored, leaving the current narrowing in place.
func (m *Model) handleFlashKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "ctrl+c":
		m.exitFlash()
		return m, nil
	case "backspace":
		if n := len(m.flashInput); n > 0 {
			m.flashInput = m.flashInput[:n-1]
		}
		return m, nil
	}
	if k.Type != tea.KeyRunes || len(k.Runes) != 1 || k.Alt {
		return m, nil
	}
	cand := m.flashInput + strings.ToLower(string(k.Runes))
	prefixed := false
	for i := range m.flashTargets {
		switch label := m.flashTargets[i].label; {
		case label == cand:
			// labels are prefix-free, so an exact hit is unambiguous — fire it.
			return m.fireFlash(m.flashTargets[i])
		case strings.HasPrefix(label, cand):
			prefixed = true
		}
	}
	if prefixed {
		m.flashInput = cand
	}
	return m, nil
}

// fireFlash leaves flash mode, moves the cursor onto the target row, and performs
// the chosen action there (do == nil is a plain jump).
func (m *Model) fireFlash(t flashTarget) (tea.Model, tea.Cmd) {
	m.exitFlash()
	if t.row < 0 || t.row >= len(m.rows) {
		return m, nil
	}
	m.cursor = t.row
	cur := m.rows[t.row].it
	m.caret = len([]rune(m.tree.displayName(cur)))
	m.clampCaret()
	if t.do != nil {
		return m, t.do(m, cur)
	}
	return m, nil
}

// flashRowSuffix renders the labelled actions hanging off the end of row i. Each
// chip shows its label then its verb; a label still matching the typed prefix
// lights the to-type tail (the matched prefix grays), one that no longer matches
// fades out whole — the flash.nvim narrowing read.
func (m *Model) flashRowSuffix(i int) string {
	var b strings.Builder
	for j := range m.flashTargets {
		t := m.flashTargets[j]
		if t.row != i || t.label == "" {
			continue
		}
		b.WriteString(" " + flashChip(t, m.flashInput))
	}
	return b.String()
}

// flashChip styles one label+verb against the current typed prefix. The whole
// outline renders gray in flash mode, so these colored chips are the only
// highlights on screen; the color is the action's, kept consistent per action.
func flashChip(t flashTarget, input string) string {
	// no longer reachable from what's been typed: fade the whole chip to gray.
	if input != "" && !strings.HasPrefix(t.label, input) {
		return cReset + cDim + t.label + " " + t.verb + cReset
	}
	tail := t.label[len(input):]
	s := cReset
	if input != "" {
		s += cDim + input + cReset // matched prefix is spent — grayed
	}
	// the live, to-type tail is a solid block in the action's color (the highlight);
	// the verb trails in the same color so the chip reads as one unit.
	s += t.color + cInvert + cBold + tail + cReset
	s += t.color + " " + t.verb + cReset
	return s
}
