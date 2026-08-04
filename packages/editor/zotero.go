package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/utils/browser"
	"github.com/lflow/lflow/packages/zotero"
)

// A zotero chip is an inline CITATION backed by an entry in the local Zotero
// library. Its value is Zotero's own "zotero://select/…" URI — so the stored
// value IS the local-open target — and its label is the compact author-year
// form the outline shows ("Z Smith et al. 2020").
//
// Two destinations, both a keystroke (or a terminal click) away:
//
//	alt+g   the destination the "zotero.open" setting names — LOCAL, the entry
//	        selected in the Zotero desktop app, or CLOUD, the entry in the
//	        zotero.org web library. The same target backs the chip's OSC 8
//	        hyperlink, so ctrl+clicking it in a modern terminal goes there too.
//	alt+o   the other destination, always — the escape hatch when the machine
//	        you are on is not the machine Zotero is installed on.
//
// Create one with "@@" — the ONE path to a citation chip. The full paper, by
// contrast, is a zotero NODE (zoteroitem.go), made by /type → Zotero or
// /insert → Zotero, both of which open the same picker immediately.

// zoteroPickerLimit caps how many library entries the picker ranks per
// keystroke; the list itself only ever shows pickerMaxRows of them.
const zoteroPickerLimit = 60

// zoteroMark is the brand mark: the ":zotero" icon from the icon catalog, worn
// by a citation chip and by a mirrored entry's title row alike, so Zotero looks
// the same everywhere in the outline.
const zoteroMark = "Z"

// zoteroOpenMode is the /settings "zotero.open" preference ("local" or
// "cloud"): which destination alt+g and the chip's terminal hyperlink go to.
// A package var, like linkColorMode, because the render path is Model-free.
var zoteroOpenMode = "local"

// zoteroAccount is the zotero.org account name the personal web-library URL is
// built from, cached at the package level for the same reason. It is refreshed
// whenever the library is read (see zoteroLibrary).
var zoteroAccount = ""

// zoteroLibrary returns the library already in hand. It never reads the disk:
// the picker calls it on every keystroke, and a Zotero library is a file big
// enough that re-reading it per keystroke is not a thing to do. zoteroRefresh
// is what does the reading, at the moments a user asks for something.
func (m *Model) zoteroLibrary() (*zotero.Library, bool) {
	return m.zoteroLib, m.zoteroLib != nil
}

// zoteroLoadedMsg lands a finished library read back on the update goroutine.
type zoteroLoadedMsg struct {
	lib *zotero.Library
	err string
}

// zoteroEnsure returns the command that puts a current library in hand, or nil
// when one already is. The read happens OFF the update goroutine — a library is
// a database, and reading it where bubbletea reads keys freezes the editor for
// as long as it takes. Callers open their picker first and let the rows arrive.
//
// A read already in flight returns nil rather than starting a second one, so
// holding @@ down cannot stack library reads.
func (m *Model) zoteroEnsure() tea.Cmd {
	if m.zoteroFill.running() {
		return nil
	}
	if m.zoteroLib != nil && !m.zoteroLib.Stale() {
		return nil
	}
	m.zoteroFill.begin()
	return func() tea.Msg {
		lib, err := zotero.Load("")
		msg := zoteroLoadedMsg{lib: lib}
		if err != nil {
			msg.err = err.Error()
		}
		return msg
	}
}

// handleZoteroLoaded takes the library the read produced. A failure is
// remembered in zoteroErr so the picker can explain itself, and the next
// explicit refresh tries again — the answer changes when Zotero gets installed.
// Any mirror that asked for its entry while the read was still running is
// started now that there is a library to read it from.
func (m *Model) handleZoteroLoaded(msg zoteroLoadedMsg) tea.Cmd {
	m.zoteroFill.done, m.zoteroFill.err = true, msg.err
	waiting := m.zoteroWaiting
	m.zoteroWaiting = nil

	if msg.err != "" {
		m.zoteroErr = msg.err
		return nil // a library already in hand still beats nothing
	}
	m.zoteroLib, m.zoteroErr = msg.lib, ""
	m.zoteroFill.n = len(msg.lib.Items)
	zoteroAccount = m.zoteroUsername()

	var cmds []tea.Cmd
	for _, uuid := range waiting {
		if root := m.tree.byUUID[uuid]; root != nil {
			if c := m.zoteroPull(root); c != nil {
				cmds = append(cmds, c)
			}
		}
	}
	return tea.Batch(cmds...)
}

// zoteroUsername is the zotero.org account the personal web-library URL is
// built from: the one the synced library records about itself, falling back to
// credentials.json for a library that has never signed in locally.
func (m *Model) zoteroUsername() string {
	if m.zoteroLib != nil && m.zoteroLib.Username != "" {
		return m.zoteroLib.Username
	}
	if m.ctx.Paths.Config == "" {
		return ""
	}
	return zotero.LoadUsername(m.ctx.Paths.Config)
}

// ── the chip ───────────────────────────────────────────────────────────────

// zoteroChipAtCaret returns the zotero chip the caret sits on (its anchor
// begins at the caret, or ends exactly at it), or ok=false. It mirrors
// linkChipAtCaret — the caret vocabulary for chips is the same everywhere.
func (m *Model) zoteroChipAtCaret(cur *item) (database.Chip, bool) {
	return m.chipAtCaret(cur, chipKindZotero)
}

// insertZoteroCite splices a citation chip for one library entry in at the caret.
func (m *Model) insertZoteroCite(it zotero.Item) {
	cur := m.cursorItem()
	if cur == nil {
		return
	}
	m.pushUndo("")
	anchor := m.createLabeledChip(chipKindZotero, zotero.RefOf(it).LocalURI(), it.Label())
	if anchor == "" {
		return
	}
	m.insertLiteralAt(cur, m.caret, anchor)
}

// ── following a citation ───────────────────────────────────────────────────

// zoteroTargetLocal reports whether the "zotero.open" preference points at the
// local Zotero app (the default) rather than the web library.
func zoteroTargetLocal() bool { return zoteroOpenMode != "cloud" }

// zoteroWebURL resolves a citation's cloud address: the zotero.org web library
// when the account is known, and otherwise the entry's DOI — a paper's other
// public home, and the only thing a library with no signed-in account can
// offer. Returns "" plus the reason when neither is available.
func (m *Model) zoteroWebURL(ref zotero.Ref) (string, string) {
	// whatever library is already in hand — following a citation must not wait on
	// a database read, and the account name has a second home in credentials.json
	// (see zoteroUsername) for the cold case where none is loaded yet
	lib, loaded := m.zoteroLibrary()
	if url, ok := ref.WebURL(m.zoteroUsername()); ok {
		return url, ""
	}
	// no username: fall back to the entry's own public identifier
	if loaded {
		if it, found := lib.ByKey(ref.Key); found {
			if url := zotero.DOIURL(it.DOI); url != "" {
				return url, ""
			}
			if it.URL != "" {
				return it.URL, ""
			}
		}
	}
	return "", `zotero · no account name — add {"zotero":{"username":"…"}} to ~/.config/lflow/credentials.json`
}

// openZoteroChip follows a citation. local selects the entry in the Zotero
// desktop app through its registered URI scheme (which is how this works on
// Windows, and from WSL onto the Windows-side Zotero); otherwise it opens the
// cloud address in the browser.
func (m *Model) openZoteroChip(c database.Chip, local bool) (tea.Model, tea.Cmd) {
	ref, ok := zotero.ParseRef(c.Value)
	if !ok {
		m.flash = "zotero · this citation has no library reference"
		return m, nil
	}
	label := clipStr(zoteroChipLabel(c), 28)
	if local {
		if err := browser.OpenApp(ref.LocalURI()); err != nil {
			m.flash = "zotero · " + err.Error()
		} else {
			m.flash = "zotero · opened " + label + " in Zotero"
		}
		return m, nil
	}
	url, why := m.zoteroWebURL(ref)
	if url == "" {
		m.flash = why
		return m, nil
	}
	if err := browser.Open(url); err != nil {
		m.flash = "zotero · " + err.Error()
	} else {
		m.flash = "zotero · opened " + label + " on the web"
	}
	return m, nil
}

// zoteroChipLabel is a citation's display text, falling back to its reference
// so a chip is never blank.
func zoteroChipLabel(c database.Chip) string {
	if c.Label != "" {
		return c.Label
	}
	if ref, ok := zotero.ParseRef(c.Value); ok {
		return ref.Key
	}
	return c.Value
}

// zoteroLinkTarget is the hyperlink target a citation chip advertises to the
// terminal, so a ctrl+click goes where alt+g would. Terminals follow what they
// can hand to the desktop: the local target is Zotero's own scheme (which the
// desktop resolves), the cloud target an ordinary https URL. "" = no
// hyperlink — a cloud target with no account name known yet.
func zoteroLinkTarget(c database.Chip) string {
	ref, ok := zotero.ParseRef(c.Value)
	if !ok {
		return ""
	}
	if zoteroTargetLocal() {
		return ref.LocalURI()
	}
	url, _ := ref.WebURL(zoteroAccount)
	return url
}

// ── the cite picker (modeCite) ─────────────────────────────────────────────

// citeAction is what picking a library entry does: splice a citation chip at
// the caret, or turn the node into a mirror of that entry (see zoteroitem.go).
// One picker, two destinations — the library search is identical either way.
type citeAction int

const (
	citeChip   citeAction = iota // "@@" and /insert → Zotero — a citation chip at the caret
	citeMirror                   // /type → Zotero, and alt+r on an unbound zotero node
)

// openCitePicker opens the library picker at the caret. The picker is on screen
// before the library is read, and fills when the read lands — you can start
// typing a search into an empty list and the entries appear under it. A node
// that cannot take the chosen action refuses, the same way /insert does.
func (m *Model) openCitePicker(act citeAction) (tea.Model, tea.Cmd) {
	cur := m.cursorItem()
	if cur == nil {
		return m, nil
	}
	if !m.mirrorContext().editable || cur.readonly {
		m.flash = "node is not editable"
		return m, nil
	}
	if act == citeChip {
		if !typeOf(cur.typ).inlineEditable {
			m.flash = "node is not editable"
			return m, nil
		}
		if !chipsEnabled(cur) {
			m.flash = "chips are disabled for this node type"
			return m, nil
		}
	} else if len(cur.children) > 0 || strings.TrimSpace(cur.name) != "" {
		// mirroring OWNS the node: it takes over the row's text and its whole
		// child list, so it only ever lands on an empty one
		m.flash = "zotero · mirror an entry into an empty node"
		return m, nil
	}
	m.citeAct = act
	m.mode = modeCite
	m.list.open(m, zoteroSource{}, true)
	return m, m.zoteroEnsure()
}

// zoteroSource is the Group-A picker over the Zotero library: type to search
// authors, titles, years and journals; Enter cites the highlighted entry.
type zoteroSource struct{}

func (zoteroSource) items(m *Model, q string) []pickerItem {
	lib, ok := m.zoteroLibrary()
	if !ok {
		return nil
	}
	var out []pickerItem
	for _, it := range lib.Search(q, zoteroPickerLimit) {
		it := it
		out = append(out, pickerItem{value: it.Key, render: func(bool) string {
			return zoteroRowRender(it)
		}})
	}
	return out
}

// zoteroRowRender is one picker row: the brand mark, the author-year label the
// chip will carry, then the title and venue in dim text.
func zoteroRowRender(it zotero.Item) string {
	label, detail := zotero.Format(it)
	brand := iconColorSGR(zoteroBrandColor())
	pad := 22 - runewidth.StringWidth(label)
	if pad < 1 {
		pad = 1
	}
	row := brand + zoteroMark + cReset + " " + cFG + label + cReset + strings.Repeat(" ", pad)
	if detail != "" {
		row += cDim + detail + cReset
	}
	return row
}

// zoteroBrandColor is the ":zotero" icon's catalog color, so the citation chip
// and the icon wear the same brand paint.
func zoteroBrandColor() string {
	if e, ok := iconByShortcode("zotero"); ok {
		return e.color
	}
	return "red"
}

func (zoteroSource) header(m *Model, p *listPicker) string {
	label := "cite: "
	if m.citeAct == citeMirror {
		label = "zotero: "
	}
	if _, ok := m.zoteroLibrary(); !ok && !m.zoteroFill.running() {
		return " " + cDim + label + cReset + cRed + m.zoteroErr + cReset
	}
	query := cDim + "search your Zotero library" + cReset
	if p.query != "" {
		query = cFG + p.query + cReset
	}
	// while the read is in flight the header carries the spinner, so an empty
	// list reads as "still coming" rather than as "nothing here"
	return " " + cDim + label + cReset + query + fillMark(&m.zoteroFill, "entries")
}

func (zoteroSource) initialSel(*Model) int { return 0 }

func (zoteroSource) onSelect(m *Model, it pickerItem) (tea.Model, tea.Cmd) {
	m.mode = modeOutline
	if it.value == "" {
		m.finishRichPicker()
		return m, nil
	}
	lib, ok := m.zoteroLibrary()
	if !ok {
		m.finishRichPicker()
		return m, nil
	}
	entry, found := lib.ByKey(it.value)
	if !found {
		m.finishRichPicker()
		return m, nil
	}
	if m.citeAct == citeMirror {
		cmd := m.mirrorZoteroItem(entry)
		m.flash = "zotero · mirroring " + clipStr(entry.Label(), 28)
		return m, cmd
	}
	m.insertZoteroCite(entry)
	m.flash = "cited · " + clipStr(entry.Label(), 28)
	m.finishRichPicker()
	m.refreshRows()
	return m, nil
}
