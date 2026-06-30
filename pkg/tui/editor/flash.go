package editor

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Flash is a flash.nvim-style "jump / act without moving the cursor": alt+s
// labels every visible row's available actions (jump there, run it, open its
// expanded view, fold/unfold), then the next keystrokes pick one. Labels are a
// for the common case, two letters (e.g. "hj") once a screen overflows the
// single-letter alphabet. Typing a label narrows live — the matched prefix grays
// out while the rest stays lit — and a completed label fires its action.
//
// It stays inside the outline: no alt-screen, no separate mode plumbing beyond
// modeFlash. The action set is read straight from the node-type registry (run /
// view / expand hooks), so a new runnable or expandable type gets flash actions
// for free, with no switch here to edit.

// flashKind is the action a label fires.
type flashKind int

const (
	flashJump   flashKind = iota // move the cursor onto the row
	flashRun                     // alt+r: run the node's action (bash/query/voice)
	flashExpand                  // alt+e: open the node's inline view / action (bash output, json, voice play)
	flashFold                    // toggle the node's collapsed state
)

// flashTarget is one labelled action sitting on a visible row.
type flashTarget struct {
	row   int       // index into m.rows
	kind  flashKind // what firing it does
	verb  string    // short word shown beside the label (jump / run / expand / fold)
	label string    // assigned key sequence, e.g. "a" or "hj"; "" = unlabelled (over capacity)
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
		// still offer that row's other actions (run/expand/fold).
		if i != m.cursor {
			ts = append(ts, flashTarget{row: i, kind: flashJump, verb: "jump"})
		}
		if typeOf(it.typ).run != nil {
			ts = append(ts, flashTarget{row: i, kind: flashRun, verb: "run"})
		}
		if nodeViewOf(it) != nil || typeOf(it.typ).expand != nil {
			ts = append(ts, flashTarget{row: i, kind: flashExpand, verb: "expand"})
		}
		if len(m.tree.childItems(it)) > 0 {
			verb := "fold"
			if it.collapsed {
				verb = "unfold"
			}
			ts = append(ts, flashTarget{row: i, kind: flashFold, verb: verb})
		}
	}
	if len(ts) == 0 {
		return
	}

	// Assign labels nearest-cursor-first so the closest targets earn the
	// single-keystroke labels; ties keep build order (jump before run/expand/fold).
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
// the chosen action there.
func (m *Model) fireFlash(t flashTarget) (tea.Model, tea.Cmd) {
	m.exitFlash()
	if t.row < 0 || t.row >= len(m.rows) {
		return m, nil
	}
	m.cursor = t.row
	cur := m.rows[t.row].it
	m.caret = len([]rune(m.tree.displayName(cur)))
	m.clampCaret()

	switch t.kind {
	case flashFold:
		if len(m.tree.childItems(cur)) > 0 {
			ctx := m.mirrorContext().ctx
			cur.collapsed = !cur.collapsed
			m.persistCollapsed(cur)
			m.refreshRows()
			m.cursor = m.findRow(cur, ctx)
		}
	case flashExpand:
		// mirror the alt+e handler: an inline view focuses, an action-only expand
		// (voice play) returns its command.
		if v := nodeViewOf(cur); v != nil {
			if v.Enter(m, cur) {
				m.focused = true
				m.focusScroll = 0
			}
			return m, nil
		}
		if e := typeOf(cur.typ).expand; e != nil {
			return m, e(m, cur)
		}
	case flashRun:
		if run := typeOf(cur.typ).run; run != nil {
			return m, run(m, cur)
		}
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

// flashChip styles one label+verb against the current typed prefix.
func flashChip(t flashTarget, input string) string {
	// no longer reachable from what's been typed: fade the whole chip.
	if input != "" && !strings.HasPrefix(t.label, input) {
		return cReset + cDim + t.label + " " + t.verb + cReset
	}
	rest := t.label[len(input):]
	s := cReset
	if input != "" {
		s += cDim + input + cReset // matched prefix is spent — grayed
	}
	s += bgPill + cBold + cFG + rest + cReset // the live, to-type tail
	s += cDim + " " + t.verb + cReset
	return s
}
