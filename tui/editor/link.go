package editor

import (
	"regexp"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/utils/browser"
)

// A link chip points at a node or a website. Its target is stored in the chip
// value — "lflow://node/<uuid>" for a node, a URL otherwise — and its arbitrary
// display name in the chip label. Create one with "[[" (or /insert → Link),
// follow it with alt+g or alt+r. A target pointing at a known service (Google
// Sheets/Docs/…) renders as that service's branded chip; see service.go.
//
// A bare URL — or a pasted "lflow://node/<uuid>" link, as /link copies it —
// typed inline (no "[[" ) is never auto-chipped by typing: like a natural-
// language date phrase, it only offers itself in the status bar and converts on
// ctrl+t (see detectURLNear, keys.go's ctrl+t handler and the status-bar hint
// in view.go) — never as a side effect of typing a space.

const nodeLinkScheme = "lflow://node/"

// reURL matches a bare web address in free text: an explicit scheme
// ("https://…"), a "www." host, or a pasted "lflow://node/<uuid>" node link
// (matched case-sensitively — the editor always writes it lowercase, and a
// typed variant is not a node link), greedily up to the next whitespace. The
// `\b` anchor keeps it off a scheme glued to a preceding letter/digit; trailing
// sentence punctuation ("check https://x.com.") is trimmed in detectURLNear.
var reURL = regexp.MustCompile(`(?i)\b(?:https?://|www\.|(?-i:lflow://node/))\S+`)

// trimURLPunct strips trailing characters that almost always read as sentence
// punctuation rather than URL syntax. A trailing ')' is only trimmed when it
// closes no '(' held earlier in the match, so a wiki-style
// "https://x.com/Foo_(bar)" survives whole.
func trimURLPunct(s string) string {
	for len(s) > 0 {
		last := s[len(s)-1]
		if last == ')' {
			if strings.Count(s, "(") >= strings.Count(s, ")") {
				break
			}
			s = s[:len(s)-1]
			continue
		}
		if strings.ContainsRune(".,;:!?'\"", rune(last)) {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// urlMatch is a bare URL span found near the caret, picked by detectURLNear.
type urlMatch struct {
	start, end int
	raw        string
}

// detectURLNear finds the bare URL — or pasted "lflow://node/<uuid>" link —
// phrase to act on: the one whose span contains caret, else the one nearest
// caret — mirroring detectDate's pick (date.go's pickByCaret) so ctrl+t and the
// bottom-bar hint act on the URL the user is looking at instead of always the
// leftmost one. caret is a rune offset.
func detectURLNear(name string, caret int) *urlMatch {
	runes := []rune(name)
	var best *urlMatch
	bestDist := -1
	for _, loc := range reURL.FindAllStringIndex(name, -1) {
		start, end := loc[0], loc[1]
		trimmed := trimURLPunct(name[start:end])
		end = start + len(trimmed)
		if end <= start {
			continue
		}
		rs, re := utf8.RuneCountInString(name[:start]), utf8.RuneCountInString(name[:end])
		dist := 0
		if caret < rs {
			dist = rs - caret
		} else if caret > re {
			dist = caret - re
		}
		if best == nil || dist < bestDist || (dist == bestDist && rs < best.start) {
			best = &urlMatch{start: rs, end: re, raw: string(runes[rs:re])}
			bestDist = dist
		}
	}
	return best
}

// urlChipLabel is a URL's default chip label: a known service names its
// resource when its path has one ("huggingface/turkish-parliament-speech"),
// otherwise it names itself ("Sheets"), and everything else falls back to its
// host.
func urlChipLabel(url string) string {
	if name := database.LinkName(url); name != "" {
		return name
	}
	return browser.Host(url)
}

// nodeLinkURI builds a node link target from a uuid.
func nodeLinkURI(uuid string) string { return nodeLinkScheme + uuid }

// nodeLinkLabel names a link chip converted from a pasted "lflow://node/<uuid>"
// link: the target node's own name (anchors resolved to display text, so a
// chipped title never leaks a raw anchor into the label), else the URI itself
// when the target is gone.
func (m *Model) nodeLinkLabel(uuid string) string {
	for _, name := range []string{m.treeName(uuid), m.dbName(uuid)} {
		if label := displayAnchors(name, m.chips); label != "" {
			return label
		}
	}
	return nodeLinkURI(uuid)
}

// treeName returns a loaded node's name (fresh — the in-memory edit may not
// have flushed yet).
func (m *Model) treeName(uuid string) string {
	if it, ok := m.tree.byUUID[uuid]; ok {
		return it.name
	}
	return ""
}

// dbName returns a saved node's name from the database.
func (m *Model) dbName(uuid string) string {
	if m.db == nil {
		return ""
	}
	n, err := database.GetNode(m.db, uuid)
	if err != nil {
		return ""
	}
	return n.Name
}

// nodeLinkUUID returns the uuid a node-link target points at, or ok=false for a
// URL target.
func nodeLinkUUID(value string) (string, bool) {
	if strings.HasPrefix(value, nodeLinkScheme) {
		return strings.TrimPrefix(value, nodeLinkScheme), true
	}
	return "", false
}

// linkChipAtCaret returns the link chip the caret sits on (its anchor begins at
// the caret, or ends exactly at it), or ok=false.
func (m *Model) linkChipAtCaret(cur *item) (database.Chip, bool) {
	return m.chipAtCaret(cur, chipKindLink)
}

// followLink acts on a link chip: a node target jumps the editor there, a URL
// opens in the browser.
func (m *Model) followLink(c database.Chip) (tea.Model, tea.Cmd) {
	if uuid, ok := nodeLinkUUID(c.Value); ok {
		n, err := database.GetNode(m.db, uuid)
		if err != nil {
			m.errorFlash("link target missing")
			return m, nil
		}
		if _, err := m.saveAll(); err != nil {
			m.err = err
			return m.quit()
		}
		t, err := loadTree(m.db, n.UUID)
		if err != nil {
			m.err = err
			return m.quit()
		}
		m.tree = t
		m.viewStack = []*item{t.root}
		m.undoStack = nil
		m.redoStack = nil
		m.refreshAncestors()
		m.cursor = 0
		m.caret = 0
		m.unsaved = false
		m.refreshRows()
		m.flash = "→ " + clipStr(n.Name, 24)
		return m, nil
	}
	if err := browser.Open(c.Value); err != nil {
		m.errorFlash("open failed: " + err.Error())
	} else {
		m.flash = "opened " + clipStr(c.Value, 32)
	}
	return m, nil
}

// insertLinkChip splices a new link chip (target + name) in at the caret and
// returns its chip id ("" when nothing was inserted) — the /insert service flow
// reopens the fresh chip in the link editor.
func (m *Model) insertLinkChip(value, label string) string {
	cur := m.cursorItem()
	if cur == nil {
		return ""
	}
	m.pushUndo("")
	anchor := m.createLabeledChip(chipKindLink, value, label)
	if anchor == "" {
		return ""
	}
	m.insertLiteralAt(cur, m.caret, anchor)
	return strings.Trim(anchor, string(chipSentinel))
}

// insertURLLink inserts a link chip pointing at a typed/pasted URL, its name
// defaulting to the host. Called from the [[ finder when the query is a URL.
func (m *Model) insertURLLink(raw string) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	url := browser.Normalize(raw)
	m.insertLinkChip(url, urlChipLabel(url))
	m.flash = "linked → " + clipStr(url, 32)
	m.finishRichPicker()
	m.refreshRows()
	return m, nil
}

// linkFromPaste parses a pasted run as a single link target: an
// lflow://node/<uuid> node link (canonical as-is) or a URL (normalized, so a
// bare "www." gains "https://"). Trailing whitespace and sentence punctuation
// trim, like detectURLNear. ok=false when the run is not a link.
func linkFromPaste(s string) (value string, ok bool) {
	s = trimURLPunct(strings.TrimSpace(s))
	if s == "" {
		return "", false
	}
	if _, isNode := nodeLinkUUID(s); isNode {
		return s, true
	}
	if browser.IsURL(s) {
		return browser.Normalize(s), true
	}
	return "", false
}

// pasteLinkOverSelection turns a paste over a selected run into a link chip
// whose name is the selected text and whose target is the pasted link: select
// a phrase, paste a URL — or an lflow://node/ link copied with /link — and the
// run becomes "→phrase" with no ctrl+t. A paste that is not a link leaves the
// selection to the plain replace path. Returns true when the paste was consumed.
func (m *Model) pasteLinkOverSelection(k tea.KeyMsg) bool {
	if k.Type != tea.KeyRunes || len(k.Runes) == 0 || k.Alt {
		return false
	}
	cur, lo, hi, ok := m.textSelection()
	if !ok {
		return false
	}
	value, ok := linkFromPaste(string(k.Runes))
	if !ok {
		return false
	}
	// the note is edited on the resolved node; a name goes through editTarget so
	// a mirror links from its source
	target := cur
	if !m.richNoteActive() {
		if target = m.editTargetOf(cur); target == nil {
			return false
		}
	}
	if !chipsEnabled(target) {
		return false
	}
	m.pushUndo("")
	runes := []rune(m.richText(target))
	if hi > len(runes) {
		hi = len(runes)
	}
	if lo < 0 || lo >= hi {
		return false
	}
	// the label is the selected run, anchors resolved to display text so a
	// chipped phrase never leaks a raw anchor into the link's name
	label := displayAnchors(string(runes[lo:hi]), m.chips)
	if label == "" {
		if uuid, isNode := nodeLinkUUID(value); isNode {
			label = m.nodeLinkLabel(uuid)
		} else {
			label = urlChipLabel(value)
		}
	}
	anchor := m.createLabeledChip(chipKindLink, value, label)
	if anchor == "" {
		return false
	}
	// a chip inside the run goes with it — same rule as deleteTextSelection
	for _, sp := range anchorSpans(runes) {
		if sp.start >= lo && sp.end <= hi {
			m.deleteChipID(sp.id)
		}
	}
	id := m.richSpanUUID(target)
	m.setRichText(target, string(runes[:lo])+anchor+string(runes[hi:]))
	shiftSpans(id, lo, len([]rune(anchor))-(hi-lo))
	m.persistSpans(id)
	m.caret = lo + len([]rune(anchor))
	m.clearTextSel()
	m.flash = "linked → " + trimFlash(label, 24)
	return true
}

// ── alt+e link editor (linkEditView, a band beneath the row) ───────────────

// openLinkEdit focuses the two-field editor for a link chip's name and
// target — the same focusChip/focused mechanism a bash chip's run output uses,
// so it renders as a band under the row instead of taking the whole screen.
func (m *Model) openLinkEdit(c database.Chip) {
	m.focusChip = c.ID
	m.focused = true
	m.linkEditID = c.ID
	m.linkEditName = c.Label
	m.linkEditTarget = c.Value
	m.linkEditField = 0
	m.linkEditCaret = len([]rune(c.Label))
}

// linkEditActive returns the active field's text; set writes it back.
func (m *Model) linkEditActive() string {
	if m.linkEditField == 0 {
		return m.linkEditName
	}
	return m.linkEditTarget
}

func (m *Model) setLinkEditActive(s string) {
	if m.linkEditField == 0 {
		m.linkEditName = s
	} else {
		m.linkEditTarget = s
	}
}

// saveLinkEdit writes the edited name/target back to the chip store.
func (m *Model) saveLinkEdit() {
	c, ok := m.chips[m.linkEditID]
	if !ok {
		return
	}
	c.Label = m.linkEditName
	c.Value = strings.TrimSpace(m.linkEditTarget)
	// canonicalize a bare URL target ("www.x" → "https://www.x"); leave node links
	// and already-schemed URLs alone
	if _, isNode := nodeLinkUUID(c.Value); !isNode && browser.IsURL(c.Value) {
		c.Value = browser.Normalize(c.Value)
	}
	m.chips[c.ID] = c
	if m.ctx.DB != nil {
		_ = database.UpsertChip(m.ctx.DB, c)
	}
	m.unsaved = true
}

// handleLinkEditKey edits the active field with the same caret vocabulary as
// the outline editor: ←/→, ctrl+←/→ word jumps, ctrl+backspace word delete,
// home/end — text editing is consistent everywhere.
func (m *Model) handleLinkEditKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.focusChip = ""
		m.focused = false
		return m, nil
	case "tab", "shift+tab", "up", "down":
		m.linkEditField = 1 - m.linkEditField
		m.linkEditCaret = len([]rune(m.linkEditActive()))
		return m, nil
	case "enter":
		m.saveLinkEdit()
		m.focusChip = ""
		m.focused = false
		m.refreshRows()
		return m, nil
	}
	// within-a-field editing is the same primitive as the /note editor: wrap
	// the active field in a textField, apply the shared caret keys, sync back.
	f := textField{value: m.linkEditActive(), caret: m.linkEditCaret}
	if f.handleKey(k) {
		m.setLinkEditActive(f.value)
		m.linkEditCaret = f.caret
	}
	return m, nil
}

// linkEditView is the alt+e link editor's inline view: name and target fields
// render as a band beneath the chip's own row, the same surface a bash chip's
// run output (bashChipView) uses — never a separate full-screen page. Stateless
// like every nodeView; the working copy lives in m.linkEdit*, focused via
// m.focusChip exactly like bashChipView.
type linkEditView struct{}

func (linkEditView) enter(m *Model, it *item) bool { return m.focusChip != "" }

// leave only drops focus — esc is a cancel, not a save (enter is the only save
// path; see handleLinkEditKey).
func (linkEditView) leave(m *Model, it *item) { m.focusChip = "" }

func (linkEditView) lines(m *Model, it *item, width int) int { return 4 }

// Key delegates to the field-editing vocabulary shared with every alt+e
// editor; it always swallows (the editor owns every key while it is up, same
// as before this became a band).
func (linkEditView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	_, cmd := m.handleLinkEditKey(k)
	return cmd, true
}

// Bands renders the two-field editor, self-windowed to [scroll, scroll+winH)
// like every other band even though its 4 lines never scroll in practice.
func (linkEditView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	name := m.linkEditName
	target := m.linkEditTarget
	nameLbl, targetLbl := cDim, cDim
	if m.linkEditField == 0 {
		name = withCaret(name, m.linkEditCaret)
		nameLbl = cAccent
	} else {
		target = withCaret(target, m.linkEditCaret)
		targetLbl = cAccent
	}
	pad := rail + cReset + "  "
	content := []string{
		clip(pad+cDim+"edit link"+cReset, width),
		clip(pad+nameLbl+"name   "+cReset+cFG+name+cReset, width),
		clip(pad+targetLbl+"target "+cReset+cFG+target+cReset, width),
		clip(pad+cDim+"tab switch field · enter save · esc cancel"+cReset, width),
	}
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}
