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
// display name in the chip label. Its color, when it has been given one, lives
// beside it in link_colors. Create one with "[[" (or /insert → Link), edit all
// three with alt+e, follow it with alt+g or alt+r. A target pointing at a known service (Google
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

// ── manual per-link color ──────────────────────────────────────────────────

// Manual per-link colors: the ⌥e editor's third field assigns a color to THIS
// link chip. Default is no color — the link.color preference, or the service's
// own muted hue. Assignments live in the link_colors table and, like tagColors,
// hydrate into a package var so the hot render path stays Model-free.

// linkColors is link chip id → style color name.
var linkColors = map[string]string{}

// linkColorOptions is the color row's order: "default" (whatever the link would
// wear on its own) first, then the shared style palette — the same list a tag
// and a session chip offer.
func linkColorOptions() []string {
	return append([]string{"default"}, styleColorOrder...)
}

// linkChipColorSGR returns the SGR for a link chip's assigned color — the color
// as the foreground, still underlined so a link stays visually a link — or ""
// when the chip has none. Built from styleColorCode at call time so /theme
// switches carry over.
func linkChipColorSGR(id string) string {
	name, ok := linkColors[id]
	if !ok || name == "" {
		return ""
	}
	code, ok := styleColorCode[name]
	if !ok {
		return ""
	}
	return code + cUnderline
}

// setLinkColor records a chip's color ("" = back to the default) in both the
// package var and the store.
func (m *Model) setLinkColor(id, color string) {
	if color == "" {
		delete(linkColors, id)
	} else {
		linkColors[id] = color
	}
	if db := m.chipDB(); db != nil {
		_ = database.SetLinkColor(db, id, color)
	}
}

// ── alt+e link editor (modeLinkEdit) ───────────────────────────────────────

// the editor's fields, in tab order: name, target, color.
const (
	linkFieldName = iota
	linkFieldTarget
	linkFieldColor
	linkFieldCount
)

// openLinkEdit enters the three-field editor for a link chip's name, target and
// color.
func (m *Model) openLinkEdit(c database.Chip) {
	m.mode = modeLinkEdit
	m.linkEditID = c.ID
	m.linkEditName = c.Label
	m.linkEditTarget = c.Value
	m.linkEditField = linkFieldName
	m.linkEditCaret = len([]rune(c.Label))
	m.linkEditColor = 0
	for i, opt := range linkColorOptions() {
		if opt == linkColors[c.ID] {
			m.linkEditColor = i
		}
	}
}

// linkEditActive returns the active text field's content; set writes it back.
// The color row is not text — it reads as empty and swallows writes.
func (m *Model) linkEditActive() string {
	switch m.linkEditField {
	case linkFieldName:
		return m.linkEditName
	case linkFieldTarget:
		return m.linkEditTarget
	}
	return ""
}

func (m *Model) setLinkEditActive(s string) {
	switch m.linkEditField {
	case linkFieldName:
		m.linkEditName = s
	case linkFieldTarget:
		m.linkEditTarget = s
	}
}

// saveLinkEdit writes the edited name/target back to the chip store, and the
// picked color to the link color store.
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
	color := ""
	if opts := linkColorOptions(); m.linkEditColor > 0 && m.linkEditColor < len(opts) {
		color = opts[m.linkEditColor]
	}
	m.setLinkColor(c.ID, color)
	m.unsaved = true
}

// handleLinkEditKey edits the active field with the same caret vocabulary as
// the outline editor: ←/→, ctrl+←/→ word jumps, ctrl+backspace word delete,
// home/end — text editing is consistent everywhere. The color row is not text:
// there ←/→ cycle swatches instead, the way the session page's color row does.
func (m *Model) handleLinkEditKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeOutline
		return m, nil
	case "tab", "down":
		m.linkEditField = (m.linkEditField + 1) % linkFieldCount
		m.linkEditCaret = len([]rune(m.linkEditActive()))
		return m, nil
	case "shift+tab", "up":
		m.linkEditField = (m.linkEditField + linkFieldCount - 1) % linkFieldCount
		m.linkEditCaret = len([]rune(m.linkEditActive()))
		return m, nil
	case "enter":
		m.saveLinkEdit()
		m.mode = modeOutline
		m.refreshRows()
		return m, nil
	}
	if m.linkEditField == linkFieldColor {
		n := len(linkColorOptions())
		switch k.String() {
		case "left":
			m.linkEditColor = (m.linkEditColor + n - 1) % n
		case "right":
			m.linkEditColor = (m.linkEditColor + 1) % n
		}
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

// linkEditDefaultColor is what this link wears with no assignment — its
// service's hue, else the link.color preference. It is both the "default"
// swatch and the preview's color while "default" is picked. The target field is
// live here, so retargeting the chip at Google Sheets recolors the preview
// before the edit is even saved.
func (m *Model) linkEditDefaultColor() string {
	c := database.Chip{ID: m.linkEditID, Kind: chipKindLink, Value: strings.TrimSpace(m.linkEditTarget)}
	if svc, ok := linkService(c); ok {
		return serviceChipColor(svc)
	}
	return linkChipColorCode()
}

// linkEditPreviewColor is the SGR the previewed chip wears: the picked swatch,
// or — on "default" — whatever the link would wear in the outline right now.
func (m *Model) linkEditPreviewColor() string {
	opts := linkColorOptions()
	if m.linkEditColor > 0 && m.linkEditColor < len(opts) {
		if code, ok := styleColorCode[opts[m.linkEditColor]]; ok {
			return code + cUnderline
		}
	}
	return m.linkEditDefaultColor()
}

func (m *Model) viewLinkEdit(maxLine int) []string {
	name := m.linkEditName
	target := m.linkEditTarget
	nameLbl, targetLbl, colorLbl := cDim, cDim, cDim
	switch m.linkEditField {
	case linkFieldName:
		name = withCaret(name, m.linkEditCaret)
		nameLbl = cAccent
	case linkFieldTarget:
		target = withCaret(target, m.linkEditCaret)
		targetLbl = cAccent
	default:
		colorLbl = cAccent
	}

	// the preview is the chip itself, in the color it will wear — the same "→name"
	// the outline draws, not a swatch standing in for it
	previewName := strings.TrimSpace(m.linkEditName)
	if previewName == "" {
		previewName = strings.TrimSpace(m.linkEditTarget)
	}
	preview := m.linkEditPreviewColor() + nonBreaking("→"+clipStr(previewName, 40)) + cReset

	opts := linkColorOptions()
	var sw strings.Builder
	for i, opt := range opts {
		dot := styleColorCode[opt]
		if i == 0 {
			dot = m.linkEditDefaultColor() // "default" wears the link's own color
		}
		glyph := "·"
		if i == m.linkEditColor {
			glyph = "●"
		}
		sw.WriteString(dot + glyph + cReset + " ")
	}

	var lines []string
	lines = append(lines, clip(cDim+" edit link"+cReset, maxLine))
	lines = append(lines, clip(" "+preview, maxLine))
	lines = append(lines, "")
	lines = append(lines, clip(nameLbl+" name   "+cReset+cFG+name+cReset, maxLine))
	lines = append(lines, clip(targetLbl+" target "+cReset+cFG+target+cReset, maxLine))
	lines = append(lines, clip(colorLbl+" color  "+cReset+sw.String(), maxLine))
	lines = append(lines, "")
	lines = append(lines, clip(cDim+" tab switch field · ←→ color · enter save · esc cancel"+cReset, maxLine))
	m.pageRows = len(lines) // no status bar here — the whole frame is main region
	return lines
}
