package editor

import (
	"sort"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/tui/database"
)

// Flash is flash.nvim-style jump: alt+s, then type. Each keystroke extends a
// search over the visible outline; matches highlight in place and each one gets
// a label. A key that still matches text extends the query; a key that does not
// (and is a label) jumps the cursor onto that match. Esc cancels, backspace
// un-types, enter jumps to the nearest remaining match.
//
// Labels are drawn from the alphabet minus the next character of every current
// match, so typing the rest of a word never collides with a label. It stays
// inside the outline: no alt-screen, no per-type action menu.

// flashTarget is one labelled match of the current query on a visible row.
type flashTarget struct {
	row   int // index into m.rows
	start int // rune offset in the row's visible name
	end   int
	label string // assigned key sequence, e.g. "a" or "hj"; "" = unlabelled
}

// flashAlphabet is the label key pool, home-row first so the common (single
// letter) labels fall under the strongest fingers.
var flashAlphabet = []rune("asdfghjklqwertyuiopzxcvbnm")

// flashLabels returns n prefix-free labels from the default alphabet.
func flashLabels(n int) []string { return flashLabelsFrom(n, flashAlphabet) }

// flashLabelsFrom returns n prefix-free labels from alphabet: single letters
// until it runs out, then two-letter labels under the remaining letters. It
// keeps as many single-letter labels as possible while staying prefix-free.
func flashLabelsFrom(n int, alphabet []rune) []string {
	if n <= 0 || len(alphabet) == 0 {
		return nil
	}
	k := len(alphabet)
	if n <= k {
		out := make([]string, n)
		for i := 0; i < n; i++ {
			out[i] = string(alphabet[i])
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
		out = append(out, string(alphabet[i]))
	}
	for pi := s; pi < k && len(out) < n; pi++ {
		for ci := 0; ci < k && len(out) < n; ci++ {
			out = append(out, string(alphabet[pi])+string(alphabet[ci]))
		}
	}
	return out
}

// enterFlash switches into modeFlash and waits for the first search character.
// A node with no rows, or while a view is focused, is a no-op.
func (m *Model) enterFlash() {
	if m.focused || len(m.rows) == 0 {
		return
	}
	m.flashQuery = ""
	m.flashInput = ""
	m.flashTargets = nil
	m.mode = modeFlash
}

func flashDist(row, cursor int) int {
	if row < cursor {
		return cursor - row
	}
	return row - cursor
}

// exitFlash drops back to normal outline editing, clearing the search.
func (m *Model) exitFlash() {
	m.mode = modeOutline
	m.flashTargets = nil
	m.flashQuery = ""
	m.flashInput = ""
}

// flashText is the on-screen name a row is searched against.
func (m *Model) flashText(it *item) string {
	if it == nil {
		return ""
	}
	return stripControlBytes(displayAnchors(m.tree.displayName(it), m.chips))
}

// flashFind returns non-overlapping, case-insensitive substring hits of query
// in text, as rune ranges.
func flashFind(text, query string) []queryRange {
	if query == "" || text == "" {
		return nil
	}
	lower := []rune(strings.ToLower(text))
	q := []rune(strings.ToLower(query))
	var out []queryRange
	for i := 0; i+len(q) <= len(lower); i++ {
		if string(lower[i:i+len(q)]) == string(q) {
			out = append(out, queryRange{i, i + len(q)})
			i += len(q) - 1
		}
	}
	return out
}

// collectFlashTargets walks visible rows for query matches, without labels.
func (m *Model) collectFlashTargets(query string) []flashTarget {
	if query == "" {
		return nil
	}
	var ts []flashTarget
	for i, r := range m.rows {
		for _, hit := range flashFind(m.flashText(r.it), query) {
			ts = append(ts, flashTarget{row: i, start: hit.start, end: hit.end})
		}
	}
	return ts
}

// rebuildFlashTargets assigns nearest-cursor-first labels to the current query's
// matches. Next-characters of matches are kept out of the label pool so typing
// the rest of a word extends the search instead of jumping.
func (m *Model) rebuildFlashTargets() {
	m.flashInput = ""
	ts := m.collectFlashTargets(m.flashQuery)
	if len(ts) == 0 {
		m.flashTargets = nil
		return
	}
	exclude := map[rune]bool{}
	for _, t := range ts {
		runes := []rune(strings.ToLower(m.flashText(m.rows[t.row].it)))
		if t.end < len(runes) {
			exclude[runes[t.end]] = true
		}
	}
	var pool []rune
	for _, r := range flashAlphabet {
		if !exclude[r] {
			pool = append(pool, r)
		}
	}
	if len(pool) < 2 {
		pool = flashAlphabet
	}

	order := make([]int, len(ts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return flashDist(ts[order[a]].row, m.cursor) < flashDist(ts[order[b]].row, m.cursor)
	})
	labels := flashLabelsFrom(len(ts), pool)
	for rank, idx := range order {
		if rank < len(labels) {
			ts[idx].label = labels[rank]
		}
	}
	m.flashTargets = ts
}

// handleFlashKey consumes a keystroke while flash is open: esc cancels,
// backspace un-types, enter jumps to the nearest match, and a letter either
// extends the search or picks a label.
func (m *Model) handleFlashKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "ctrl+c":
		m.exitFlash()
		return m, nil
	case "enter":
		if t, ok := m.nearestFlash(); ok {
			return m.fireFlash(t)
		}
		return m, nil
	case "backspace":
		if n := len(m.flashInput); n > 0 {
			m.flashInput = m.flashInput[:n-1]
			return m, nil
		}
		if q := []rune(m.flashQuery); len(q) > 0 {
			m.flashQuery = string(q[:len(q)-1])
			m.rebuildFlashTargets()
		}
		return m, nil
	}
	if k.Type != tea.KeyRunes || len(k.Runes) != 1 || k.Alt {
		return m, nil
	}
	ch := strings.ToLower(string(k.Runes))

	// Prefer extending the search when the extra character still hits text —
	// that's how you type a word instead of jumping to a same-letter label.
	if next := m.collectFlashTargets(m.flashQuery + ch); len(next) > 0 {
		m.flashQuery += ch
		m.rebuildFlashTargets()
		return m, nil
	}

	cand := m.flashInput + ch
	prefixed := false
	for i := range m.flashTargets {
		switch label := m.flashTargets[i].label; {
		case label == cand:
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

// nearestFlash is the closest labelled match still reachable from flashInput.
func (m *Model) nearestFlash() (flashTarget, bool) {
	var best flashTarget
	bestDist := -1
	found := false
	for _, t := range m.flashTargets {
		if t.label == "" || !strings.HasPrefix(t.label, m.flashInput) {
			continue
		}
		d := flashDist(t.row, m.cursor)
		if !found || d < bestDist {
			best, bestDist, found = t, d, true
		}
	}
	return best, found
}

// fireFlash leaves flash mode, moves the cursor onto the match, and parks the
// caret at the match so the next edit starts there.
func (m *Model) fireFlash(t flashTarget) (tea.Model, tea.Cmd) {
	m.exitFlash()
	if t.row < 0 || t.row >= len(m.rows) {
		return m, nil
	}
	m.cursor = t.row
	cur := m.rows[t.row].it
	m.caret = visibleToNameOffset(m.tree.displayName(cur), m.chips, t.start)
	m.clampCaret()
	return m, nil
}

// visibleToNameOffset maps a rune offset in the chip-expanded visible name back
// to a caret offset in the raw name (an offset inside a chip lands on it).
func visibleToNameOffset(name string, chips map[string]database.Chip, vis int) int {
	if vis <= 0 || !hasAnchor(name) {
		if vis < 0 {
			return 0
		}
		return vis
	}
	runes := []rune(name)
	spans := anchorSpans(runes)
	v := 0
	for i := 0; i < len(runes); {
		if sp := spanStartingAt(spans, i); sp != nil {
			disp := utf8.RuneCountInString(dispByID(sp.id, chips))
			if vis < v+disp {
				return sp.start
			}
			v += disp
			i = sp.end
			continue
		}
		if v == vis {
			return i
		}
		v++
		i++
	}
	return len(runes)
}

// flashTargetsFor returns the labelled matches sitting on row i.
func (m *Model) flashTargetsFor(i int) []flashTarget {
	var out []flashTarget
	for _, t := range m.flashTargets {
		if t.row == i {
			out = append(out, t)
		}
	}
	return out
}

const (
	cBlack       = "\x1b[38;2;0;0;0m"
	bgFlashHit   = "\x1b[48;2;255;255;255m" // typed match: black on white
	bgFlashLabel = "\x1b[48;2;244;71;71m"   // jump label: black on red
)

func flashRestoreBG() string {
	if bgPage != "" {
		return bgPage
	}
	return "\x1b[49m"
}

// flashPaintBody grays the row and overlays still-reachable matches: the typed
// query is black-on-white, and the remaining label letters sit black-on-red on
// top of existing cells so the line does not grow.
func flashPaintBody(body, name string, ts []flashTarget, input string, chips map[string]database.Chip) string {
	plain := stripSGR(body)
	if plain == "" {
		return body
	}
	visibleName := stripControlBytes(displayAnchors(name, chips))
	styled := flashStyleVisible(visibleName, ts, input)
	if visibleName == "" || !strings.Contains(plain, visibleName) {
		return cDim + plain + cReset
	}
	return cDim + strings.Replace(plain, visibleName, cReset+styled+cDim, 1) + cReset
}

const (
	flashCellDim   = 0
	flashCellHit   = 1
	flashCellLabel = 2
)

// flashStyleVisible paints visible-name runes in place: dim, black-on-white
// for the typed hit, black-on-red for the label overlaid on the cells after
// (or at the tail of) the match. The visible rune count never changes.
func flashStyleVisible(visible string, ts []flashTarget, input string) string {
	if visible == "" {
		return cDim
	}
	runes := []rune(visible)
	n := len(runes)
	kind := make([]byte, n)
	show := append([]rune(nil), runes...)

	type mark struct {
		start, end int
		label      string
	}
	var marks []mark
	for _, t := range ts {
		if t.start < 0 || t.end <= t.start || t.end > n {
			continue
		}
		if t.label != "" && input != "" && !strings.HasPrefix(t.label, input) {
			continue
		}
		marks = append(marks, mark{t.start, t.end, flashLabelTail(t, input)})
	}
	sort.SliceStable(marks, func(a, b int) bool { return marks[a].start < marks[b].start })

	for _, mk := range marks {
		for i := mk.start; i < mk.end; i++ {
			kind[i] = flashCellHit
		}
		lab := []rune(mk.label)
		if len(lab) == 0 {
			continue
		}
		at := mk.end
		if at+len(lab) > n {
			at = mk.end - len(lab)
			if at < 0 {
				at = 0
			}
		}
		for i, r := range lab {
			if at+i >= n {
				break
			}
			show[at+i] = r
			kind[at+i] = flashCellLabel
		}
	}

	restore := flashRestoreBG()
	var b strings.Builder
	prev := byte(255)
	for i, r := range show {
		if kind[i] != prev {
			switch kind[i] {
			case flashCellHit:
				b.WriteString(cReset + cBlack + cBold + bgFlashHit)
			case flashCellLabel:
				b.WriteString(cReset + cBlack + cBold + bgFlashLabel)
			default:
				b.WriteString(cReset + restore + cDim)
			}
			prev = kind[i]
		}
		b.WriteRune(r)
	}
	b.WriteString(cReset + restore)
	return b.String()
}

func flashLabelTail(t flashTarget, input string) string {
	if t.label == "" || (input != "" && !strings.HasPrefix(t.label, input)) {
		return ""
	}
	return t.label[len(input):]
}

// flashZoom zooms the view into a node — the alt+right action, mirror-aware.
// Used by the glyph click zone, not by flash itself. A mirror carries no
// children in memory; zoom into its source so the original's children render.
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
