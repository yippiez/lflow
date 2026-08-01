package editor

import (
	"regexp"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/chiptext"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/utils/browser"
)

// A link chip points at a node or a website. Its target is stored in the chip
// value — "lflow://node/<uuid>" for a node, a URL otherwise — and its arbitrary
// display name in the chip label. Create one with "[[" (or /link), follow it with
// alt+g or alt+r. A target pointing at a known service (Google Sheets/Docs/…)
// renders as that service's branded chip; see service.go.
//
// A bare URL typed inline (no "[[") is never auto-chipped by typing: like a
// natural-language date phrase, it only offers itself in the status bar and
// converts on ctrl+t (see detectURLNear, keys.go's ctrl+t handler and the
// status-bar hint in view.go) — never as a side effect of typing a space.

const nodeLinkScheme = "lflow://node/"

// reURL matches a bare web address in free text: an explicit scheme
// ("https://…") or a "www." host, greedily up to the next whitespace. The `\b`
// anchor keeps it off a scheme glued to a preceding letter/digit; trailing
// sentence punctuation ("check https://x.com.") is trimmed in detectURLNear.
var reURL = regexp.MustCompile(`(?i)\b(?:https?://|www\.)\S+`)

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

// detectURLNear finds the bare URL phrase to act on: the one whose span
// contains caret, else the one nearest caret — mirroring detectDate's pick
// (date.go's pickByCaret) so ctrl+t and the bottom-bar hint act on the URL the
// user is looking at instead of always the leftmost one. caret is a rune offset.
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

// urlChipLabel is a URL's default chip label: a known service names itself
// ("Sheets"), everything else falls back to its host.
func urlChipLabel(url string) string {
	if svc, ok := chiptext.ServiceFor(url); ok {
		return svc.Label
	}
	return browser.Host(url)
}

// nodeLinkURI builds a node link target from a uuid.
func nodeLinkURI(uuid string) string { return nodeLinkScheme + uuid }

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
	if cur == nil {
		return database.Chip{}, false
	}
	spans := anchorSpans([]rune(cur.name))
	for _, sp := range []*anchorSpan{spanStartingAt(spans, m.caret), spanEndingAt(spans, m.caret)} {
		if sp == nil {
			continue
		}
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindLink {
			return c, true
		}
	}
	return database.Chip{}, false
}

// followLink acts on a link chip: a node target jumps the editor there, a URL
// opens in the browser.
func (m *Model) followLink(c database.Chip) (tea.Model, tea.Cmd) {
	if uuid, ok := nodeLinkUUID(c.Value); ok {
		n, err := database.GetNode(m.db, uuid)
		if err != nil {
			m.flash = "link target missing"
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
		m.refreshAncestors()
		m.cursor = 0
		m.caret = 0
		m.unsaved = false
		m.refreshRows()
		m.flash = "→ " + clipStr(n.Name, 24)
		return m, nil
	}
	if err := browser.Open(c.Value); err != nil {
		m.flash = "open failed: " + err.Error()
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
	runes := []rune(cur.name)
	m.boundCaret(len(runes))
	cur.name = string(runes[:m.caret]) + anchor + string(runes[m.caret:])
	m.caret += len([]rune(anchor))
	m.unsaved = true
	return strings.Trim(anchor, string(chipSentinel))
}

// insertURLLink inserts a link chip pointing at a typed/pasted URL, its name
// defaulting to the host. Called from the [[ finder when the query is a URL.
func (m *Model) insertURLLink(raw string) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	url := browser.Normalize(raw)
	m.insertLinkChip(url, urlChipLabel(url))
	m.flash = "linked → " + clipStr(url, 32)
	m.refreshRows()
	return m, nil
}

// ── alt+e link editor (modeLinkEdit) ───────────────────────────────────────

// openLinkEdit enters the two-field editor for a link chip's name and target.
func (m *Model) openLinkEdit(c database.Chip) {
	m.mode = modeLinkEdit
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
		m.mode = modeOutline
		return m, nil
	case "tab", "shift+tab", "up", "down":
		m.linkEditField = 1 - m.linkEditField
		m.linkEditCaret = len([]rune(m.linkEditActive()))
		return m, nil
	case "enter":
		m.saveLinkEdit()
		m.mode = modeOutline
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

func (m *Model) viewLinkEdit(maxLine int) []string {
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
	var lines []string
	lines = append(lines, clip(cDim+" edit link"+cReset, maxLine))
	lines = append(lines, clip(nameLbl+" name   "+cReset+cFG+name+cReset, maxLine))
	lines = append(lines, clip(targetLbl+" target "+cReset+cFG+target+cReset, maxLine))
	lines = append(lines, "")
	lines = append(lines, clip(cDim+" tab switch field · enter save · esc cancel"+cReset, maxLine))
	m.pageRows = len(lines) // no status bar here — the whole frame is main region
	return lines
}
