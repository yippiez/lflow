package editor

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/zotero"
	"github.com/lflow/lflow/pkg/utils/browser"
)

// The zotero node type: one Zotero entry MIRRORED into the outline. Where the
// zotero chip (zotero.go) is a citation inside a sentence, this is the paper
// itself living in the tree — its tags on the title row, and its attachments,
// annotations and notes as children:
//
//	Z Vaswani et al. 2017 · Attention is all you need   #transformers #nlp
//	  · journalArticle · NeurIPS · June 12, 2017
//	  · doi.org/10.48550/arXiv.1706.03762
//	  ◆ paper.pdf
//	    ▍ "the encoder is composed of a stack of N = 6 layers"
//	      · check this against the diagram
//	    ▍ "we propose a new simple network architecture"
//	  ≡ reading notes · the residual stream framing…
//
// The whole subtree is Zotero's, not yours: every node is content-locked, and
// every node below the root is position-locked too, so the mirror can be
// collapsed and moved as a unit but never edited into. alt+r re-pulls and
// reconciles in place against the Zotero keys in zotero_nodes — a refresh
// updates what changed instead of duplicating the subtree.
//
// WARNING (invariant): a mirrored subtree is a projection of Zotero, never a
// destination. Nothing here writes to the Zotero database, and nothing in the
// outline may edit a mirrored node — an edit would be silently discarded by the
// next refresh, which is a lie. See zoteroMirrored / acceptsChildren.

// zoteroBindings is the whole outline's mirror map (node uuid → the Zotero
// object that node stands for), hydrated at editor start and kept current by
// every bind/release below. A package var, like tagColors and nodeSpans,
// because the glyph and render paths read it and stay Model-free.
var zoteroBindings = map[string]database.ZoteroBinding{}

// zoteroBindingFor returns the Zotero object a node mirrors, or ok=false for an
// ordinary node. Model-free: the render path calls it per row.
func zoteroBindingFor(it *item) (database.ZoteroBinding, bool) {
	if it == nil {
		return database.ZoteroBinding{}, false
	}
	b, ok := zoteroBindings[it.uuid]
	return b, ok
}

// zoteroGlyphs is the mark each mirrored node wears, by binding kind.
var zoteroGlyphs = map[string]string{
	database.ZoteroKindItem:       zoteroMark,
	database.ZoteroKindAttachment: "◆",
	database.ZoteroKindAnnotation: "▍",
	database.ZoteroKindNote:       "≡",
	database.ZoteroKindMeta:       "·",
}

// zoteroRootOf walks up to the mirror's item node — the refresh handle and the
// node every descendant belongs to. ok=false when it is outside any mirror.
func zoteroRootOf(it *item) (*item, bool) {
	for n := it; n != nil; n = n.parent {
		if b, ok := zoteroBindings[n.uuid]; ok && b.Kind == database.ZoteroKindItem {
			return n, true
		}
	}
	return nil, false
}

// zoteroMirrored reports whether a node is part of a mirrored item and so
// refuses every content edit — the guard the generic edit paths consult.
func zoteroMirrored(it *item) bool {
	_, ok := zoteroBindingFor(it)
	return ok
}

// zoteroGlyph paints the per-kind mark: the item's brand Z, an annotation's
// mark in the highlighter's own color, and dim marks for the rest.
func zoteroGlyph(it *item) (string, string) {
	b, ok := zoteroBindingFor(it)
	if !ok {
		return zoteroGlyphs[database.ZoteroKindItem], iconColorSGR(zoteroBrandColor())
	}
	glyph := zoteroGlyphs[b.Kind]
	if glyph == "" {
		glyph = glyphOpen
	}
	switch b.Kind {
	case database.ZoteroKindItem:
		return glyph, iconColorSGR(zoteroBrandColor())
	case database.ZoteroKindAnnotation:
		// the mark wears the node's own /color — the one carried over from the
		// highlighter — so a yellow highlight reads yellow in the outline
		if c := styleColor(it.style); c != "" {
			return glyph, styleColorCode[c]
		}
		return glyph, cYellow
	}
	return glyph, cDim
}

// ── pulling one entry in ───────────────────────────────────────────────────

// zoteroPullMsg lands a finished read (or its error) back in the update loop.
type zoteroPullMsg struct {
	uuid    string // the mirror root's node uuid
	details *zotero.Details
	err     error
}

// mirrorZoteroItem turns the cursor node into a mirror of one library entry and
// starts the pull. The node keeps its place in the outline; everything under it
// becomes Zotero's.
func (m *Model) mirrorZoteroItem(it zotero.Item) tea.Cmd {
	cur := m.cursorItem()
	if cur == nil {
		return nil
	}
	m.pushUndo("")
	cur.typ = database.TypeZotero
	cur.name = zoteroRootName(it)
	cur.readonly = true
	m.bindZotero(cur, database.ZoteroBinding{
		Key: it.Key, Kind: database.ZoteroKindItem, GroupID: it.GroupID,
	})
	m.unsaved = true
	m.refreshRows()
	return m.zoteroPull(cur)
}

// runZoteroPull is alt+r on any node of a mirror: refresh the whole item from
// Zotero. Never auto-runs, like every runnable type.
func runZoteroPull(m *Model, it *item) tea.Cmd {
	root, ok := zoteroRootOf(it)
	if !ok {
		m.flash = "zotero · /type → Zotero item picks the entry to mirror"
		return nil
	}
	return m.zoteroPull(root)
}

// zoteroPull reads the bound entry's details off the local library and hands
// them to the update loop. The read is a snapshot copy, so it runs off the UI
// goroutine like the Workflowy pull does.
func (m *Model) zoteroPull(root *item) tea.Cmd {
	b, ok := zoteroBindingFor(root)
	if !ok || b.Key == "" {
		m.flash = "zotero · this node is not bound to a library entry"
		return nil
	}
	if m.zoteroBusy == nil {
		m.zoteroBusy = map[string]bool{}
	}
	if m.zoteroBusy[root.uuid] {
		return nil
	}
	// the library is resolved HERE, on the update goroutine, and only the loaded
	// value travels into the command — a tea.Cmd runs concurrently with Update,
	// so it must not touch the Model at all
	lib, ok := m.zoteroRefresh()
	if !ok {
		m.flash = "zotero · " + firstNonEmptyStr(m.zoteroErr, "no Zotero library found")
		return nil
	}
	m.zoteroBusy[root.uuid] = true
	m.flash = "zotero · reading the library…"
	uuid, key := root.uuid, b.Key
	return func() tea.Msg {
		d, err := lib.Details(key)
		return zoteroPullMsg{uuid: uuid, details: d, err: err}
	}
}

// firstNonEmptyStr returns a unless it is empty.
func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// handleZoteroPull reconciles a finished read into the outline.
func (m *Model) handleZoteroPull(msg zoteroPullMsg) {
	delete(m.zoteroBusy, msg.uuid)
	root := m.tree.byUUID[msg.uuid]
	if root == nil {
		return // the mirror was deleted mid-read
	}
	if msg.err != nil {
		m.flash = "zotero · " + msg.err.Error()
		return
	}
	n := m.reconcileZotero(root, msg.details)
	m.zoteroAttachmentPaths(root, msg.details)
	m.unsaved = true
	m.refreshRows()
	m.flash = fmt.Sprintf("zotero · mirrored %s", nodeNoun(n))
}

// ── the mirrored shape ─────────────────────────────────────────────────────

// zoteroChild is one node the mirror wants to exist, in order: its binding, the
// text it shows, its style, and its own children. Every mirrored node wears the
// zotero TYPE — that is what gives each one its per-kind mark (the ▍ of a
// highlight in the highlighter's own color) and its own alt+g.
type zoteroChild struct {
	binding  database.ZoteroBinding
	name     string
	style    string
	children []zoteroChild
}

// zoteroShape lays out what an entry looks like as an outline. Pure: it maps
// the details onto nodes and decides nothing about the tree, so the shape is
// readable in one place and testable without a Model.
func zoteroShape(d *zotero.Details) []zoteroChild {
	var out []zoteroChild
	meta := func(text string) {
		if text != "" {
			out = append(out, zoteroChild{
				binding: database.ZoteroBinding{Kind: database.ZoteroKindMeta},
				name:    text,
			})
		}
	}

	// the fields worth a row of their own: what kind of thing it is, where it
	// appeared, when, and how to find it
	where := []string{d.Item.Type}
	if d.Item.Journal != "" {
		where = append(where, d.Item.Journal)
	}
	if d.Date != "" {
		where = append(where, d.Date)
	}
	meta(strings.Join(where, " · "))
	if url := zotero.DOIURL(d.Item.DOI); url != "" {
		meta(url)
	} else if d.Item.URL != "" {
		meta(d.Item.URL)
	}
	if d.Abstract != "" {
		out = append(out, zoteroChild{
			binding: database.ZoteroBinding{Kind: database.ZoteroKindMeta},
			name:    d.Abstract,
		})
	}

	for _, at := range d.Attachments {
		child := zoteroChild{
			binding: database.ZoteroBinding{Key: at.Key, Kind: database.ZoteroKindAttachment, GroupID: d.Item.GroupID},
			name:    at.Title,
		}
		for _, an := range at.Annotations {
			child.children = append(child.children, zoteroAnnotation(an, d.Item.GroupID))
		}
		out = append(out, child)
	}

	for _, n := range d.Notes {
		out = append(out, zoteroChild{
			binding: database.ZoteroBinding{Key: n.Key, Kind: database.ZoteroKindNote, GroupID: d.Item.GroupID},
			name:    n.Text,
		})
	}
	return out
}

// zoteroAnnotation maps one mark onto a node: the highlighted text as a quote
// in the highlighter's color, with your comment as its child. A mark with no
// text (an image or ink region) shows its comment, or says what it is.
func zoteroAnnotation(an zotero.Annotation, groupID string) zoteroChild {
	name := an.Text
	if name == "" {
		name = an.Comment
	}
	if name == "" {
		name = an.Kind
	}
	if an.Page != "" {
		name += "  p." + an.Page
	}
	child := zoteroChild{
		binding: database.ZoteroBinding{Key: an.Key, Kind: database.ZoteroKindAnnotation, GroupID: groupID},
		name:    name,
		style:   styleSetColor("", zoteroNearestColor(an.Color)),
	}
	// the comment only earns its own row when the highlight itself is showing
	if an.Comment != "" && an.Text != "" {
		child.children = append(child.children, zoteroChild{
			binding: database.ZoteroBinding{Kind: database.ZoteroKindMeta},
			name:    an.Comment,
		})
	}
	return child
}

// zoteroRootName is the mirror root's row: the compact citation, the title, and
// nothing else — tags are spliced on as chips by reconcile, which owns the chip
// store.
func zoteroRootName(it zotero.Item) string {
	label := it.Label()
	if it.Title == "" || it.Title == label {
		return label
	}
	return label + " · " + it.Title
}

// ── reconciling ────────────────────────────────────────────────────────────

// reconcileZotero applies a finished read to the mirror: nodes are matched by
// their Zotero key, updated in place, created when new, and tombstoned when
// they are gone from Zotero. Returns how many nodes the mirror now holds.
func (m *Model) reconcileZotero(root *item, d *zotero.Details) int {
	m.setZoteroRootName(root, d)
	root.readonly = true
	root.typ = database.TypeZotero
	m.bindZotero(root, database.ZoteroBinding{
		Key: d.Item.Key, Kind: database.ZoteroKindItem, GroupID: d.Item.GroupID,
	})
	root.note = d.Item.Citation() // the full reference, one keystroke away
	return 1 + m.reconcileZoteroChildren(root, zoteroShape(d))
}

// reconcileZoteroChildren rebuilds one level of the mirror. Matching is on
// (kind, key) — a meta row has no key of its own, so those match by position
// among the meta rows, which is what keeps a changed venue from orphaning a row.
func (m *Model) reconcileZoteroChildren(parent *item, want []zoteroChild) int {
	existing := map[string]*item{}
	metaRows := []*item{}
	for _, c := range parent.children {
		b, ok := zoteroBindings[c.uuid]
		if !ok {
			continue
		}
		if b.Kind == database.ZoteroKindMeta {
			metaRows = append(metaRows, c)
			continue
		}
		existing[b.Kind+"\x00"+b.Key] = c
	}

	count := 0
	metaUsed := 0
	kids := make([]*item, 0, len(want))
	for _, w := range want {
		var node *item
		if w.binding.Kind == database.ZoteroKindMeta {
			if metaUsed < len(metaRows) {
				node = metaRows[metaUsed]
			}
			metaUsed++
		} else {
			key := w.binding.Kind + "\x00" + w.binding.Key
			if n, ok := existing[key]; ok {
				node = n
				delete(existing, key)
			}
		}
		if node == nil {
			n, err := m.tree.newItem()
			if err != nil {
				break
			}
			node = n
			node.parent = parent
		}
		m.applyZoteroChild(node, w)
		kids = append(kids, node)
		count += 1 + m.reconcileZoteroChildren(node, w.children)
	}

	// rows Zotero no longer has: the annotation you deleted, the note you moved
	for _, gone := range existing {
		m.dropZoteroSubtree(gone)
	}
	for _, gone := range metaRows[min(metaUsed, len(metaRows)):] {
		m.dropZoteroSubtree(gone)
	}
	parent.children = kids
	return count
}

// applyZoteroChild writes one desired child onto its node and locks it. Every
// node below the mirror root is position-locked as well as content-locked: the
// subtree is Zotero's shape, so nothing may be reordered into or out of it.
func (m *Model) applyZoteroChild(it *item, w zoteroChild) {
	it.name = w.name
	it.typ = database.TypeZotero
	it.style = w.style
	it.readonly = true
	it.structureLocked = true
	m.bindZotero(it, w.binding)
}

// setZoteroRootName writes the title row, with the entry's tags as chips after
// it. The name is left alone when it already reads the same, so a refresh does
// not churn the chip store on every pull.
func (m *Model) setZoteroRootName(root *item, d *zotero.Details) {
	title := zoteroRootName(d.Item)
	var tags []string
	for _, t := range d.Tags {
		tags = append(tags, "#"+t.Name)
	}
	want := strings.TrimSpace(title + " " + strings.Join(tags, " "))
	if displayAnchors(root.name, m.chips) == want {
		m.applyZoteroTagColors(d.Tags)
		return
	}
	name := title
	for _, t := range d.Tags {
		if anchor := m.createChip(chipKindTag, t.Name); anchor != "" {
			name += " " + anchor
		}
	}
	root.name = name
	m.applyZoteroTagColors(d.Tags)
}

// applyZoteroTagColors carries Zotero's own tag colors into the outline's tag
// pills, mapped to the nearest color in the live palette. A tag the user has
// already colored here is left alone — their choice outranks the import.
func (m *Model) applyZoteroTagColors(tags []zotero.Tag) {
	for _, t := range tags {
		if t.Color == "" {
			continue
		}
		word := strings.ToLower(t.Name)
		if _, taken := tagColors[word]; taken {
			continue
		}
		name := zoteroNearestColor(t.Color)
		if name == "" {
			continue
		}
		tagColors[word] = name
		if m.db != nil {
			_ = database.SetTagColor(m.db, word, name)
		}
	}
}

// bindZotero records a node's binding in memory and in the DB.
func (m *Model) bindZotero(it *item, b database.ZoteroBinding) {
	zoteroBindings[it.uuid] = b
	if m.db != nil {
		_ = database.UpsertZoteroNode(m.db, it.uuid, b, time.Now().UnixNano())
	}
}

// dropZoteroSubtree tombstones a mirrored node that Zotero no longer has, and
// releases every binding underneath it. The structure lock is lifted first —
// it exists to stop the USER reshaping the mirror, not the mirror itself.
func (m *Model) dropZoteroSubtree(it *item) {
	var release func(x *item)
	release = func(x *item) {
		x.structureLocked = false
		delete(zoteroBindings, x.uuid)
		if m.db != nil {
			_ = database.DeleteZoteroNode(m.db, x.uuid)
		}
		for _, c := range x.children {
			release(c)
		}
	}
	release(it)
	m.tombstoneItem(it)
}

// ── opening a mirrored node ────────────────────────────────────────────────

// zoteroOpenNode is alt+g on a node of a mirror; other=true is alt+o, the other
// destination. What "the paper" means depends on which node you are standing
// on: an attachment opens in Zotero's PDF reader, an annotation opens at the
// mark itself, and everything else opens the entry.
func (m *Model) zoteroOpenNode(it *item, other bool) (tea.Model, tea.Cmd) {
	b, ok := zoteroBindingFor(it)
	if !ok {
		return m, nil
	}
	root, _ := zoteroRootOf(it)
	item, _ := zoteroBindingFor(root)

	switch b.Kind {
	case database.ZoteroKindAttachment:
		if other {
			return m.zoteroOpenFile(it)
		}
		return m.zoteroOpenURI(zotero.Ref{Key: b.Key, GroupID: b.GroupID}.PDFURI(""), "the reader")
	case database.ZoteroKindAnnotation:
		if other {
			return m.openZoteroEntry(item, true)
		}
		// the mark lives in its attachment: open that document, at this mark
		if parent, ok := zoteroBindingFor(it.parent); ok && parent.Kind == database.ZoteroKindAttachment {
			ref := zotero.Ref{Key: parent.Key, GroupID: parent.GroupID}
			return m.zoteroOpenURI(ref.PDFURI(b.Key), "the highlight")
		}
	}
	return m.openZoteroEntry(item, other)
}

// openZoteroEntry opens the mirrored ENTRY: local or cloud per the zotero.open
// setting, and the other one when other is true — the same pair the citation
// chip offers.
func (m *Model) openZoteroEntry(b database.ZoteroBinding, other bool) (tea.Model, tea.Cmd) {
	if b.Key == "" {
		return m, nil
	}
	local := zoteroTargetLocal()
	if other {
		local = !local
	}
	ref := zotero.Ref{Key: b.Key, GroupID: b.GroupID}
	return m.openZoteroChip(database.Chip{
		Kind: chipKindZotero, Value: ref.LocalURI(), Label: b.Key,
	}, local)
}

// zoteroOpenURI hands a zotero:// URI to the desktop, the same hop the citation
// chip's local target takes (Windows-first under WSL).
func (m *Model) zoteroOpenURI(uri, what string) (tea.Model, tea.Cmd) {
	if err := browser.OpenApp(uri); err != nil {
		m.flash = "zotero · " + err.Error()
	} else {
		m.flash = "zotero · opened " + what
	}
	return m, nil
}

// zoteroOpenFile hands the attachment's actual file to the host's own viewer —
// the escape hatch when you want the PDF in something other than Zotero.
func (m *Model) zoteroOpenFile(it *item) (tea.Model, tea.Cmd) {
	path := m.zoteroPaths[it.uuid]
	if path == "" {
		m.flash = "zotero · this attachment has no file on this machine"
		return m, nil
	}
	if err := browser.OpenApp(path); err != nil {
		m.flash = "zotero · " + err.Error()
	} else {
		m.flash = "zotero · opened " + clipStr(path, 40)
	}
	return m, nil
}

// ── color matching ─────────────────────────────────────────────────────────

// zoteroNearestColor maps a Zotero "#rrggbb" to the nearest color in the LIVE
// palette, so an imported highlight or tag sits inside the active theme instead
// of next to it. "" for an unparseable or absent color.
func zoteroNearestColor(hex string) string {
	r, g, b, ok := parseHexColor(hex)
	if !ok {
		return ""
	}
	best, bestDist := "", 1<<30
	for _, name := range styleColorOrder {
		pr, pg, pb, ok := sgrRGB(styleColorCode[name])
		if !ok {
			continue
		}
		// plain squared distance in RGB: the palette is eight well-separated
		// hues, so nothing subtler earns its keep here
		d := (r-pr)*(r-pr) + (g-pg)*(g-pg) + (b-pb)*(b-pb)
		if d < bestDist {
			best, bestDist = name, d
		}
	}
	return best
}

// parseHexColor reads "#rrggbb" (or "rrggbb").
func parseHexColor(s string) (r, g, b int, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v>>16) & 0xff, int(v>>8) & 0xff, int(v) & 0xff, true
}

// sgrRGB pulls the r,g,b back out of a truecolor foreground SGR
// ("\x1b[38;2;r;g;bm"), so matching follows whatever the theme currently is.
func sgrRGB(sgr string) (r, g, b int, ok bool) {
	i := strings.Index(sgr, "38;2;")
	if i < 0 {
		return 0, 0, 0, false
	}
	rest := strings.TrimSuffix(sgr[i+len("38;2;"):], "m")
	parts := strings.Split(rest, ";")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	vals := make([]int, 3)
	for k, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, 0, 0, false
		}
		vals[k] = n
	}
	return vals[0], vals[1], vals[2], true
}

// ── context + flash ────────────────────────────────────────────────────────

// zoteroToContext gives a mirrored node a typed element, so an outline handed
// to a generator reads as a citation tree rather than anonymous bullets.
func zoteroToContext(m *Model, it *item) contextXML {
	b, ok := zoteroBindingFor(it)
	if !ok {
		return contextXML{tag: "zotero"}
	}
	attrs := ""
	if b.Key != "" {
		attrs = `key="` + b.Key + `"`
	}
	return contextXML{tag: "zotero-" + b.Kind, attrs: attrs}
}

// zoteroFlashActions names the mirror's verbs in the flash menu.
func zoteroFlashActions(m *Model, it *item) []flashAction {
	b, _ := zoteroBindingFor(it)
	open := "paper"
	if b.Kind == database.ZoteroKindAttachment {
		open = "pdf"
	} else if b.Kind == database.ZoteroKindAnnotation {
		open = "mark"
	}
	return []flashAction{
		{verb: open, color: cRed, do: func(m *Model, it *item) tea.Cmd {
			m.zoteroOpenNode(it, false)
			return nil
		}},
		{verb: "refresh", color: cYellow, do: func(m *Model, it *item) tea.Cmd {
			return runZoteroPull(m, it)
		}},
	}
}

// zoteroAttachmentPaths records where each mirrored attachment's file lives on
// this machine — session state, never persisted: a path is machine-specific and
// the next pull recomputes it.
func (m *Model) zoteroAttachmentPaths(root *item, d *zotero.Details) {
	if m.zoteroPaths == nil {
		m.zoteroPaths = map[string]string{}
	}
	byKey := map[string]string{}
	for _, at := range d.Attachments {
		byKey[at.Key] = at.Path
	}
	var walk func(*item)
	walk = func(x *item) {
		if b, ok := zoteroBindings[x.uuid]; ok && b.Kind == database.ZoteroKindAttachment {
			m.zoteroPaths[x.uuid] = byKey[b.Key]
		}
		for _, c := range x.children {
			walk(c)
		}
	}
	walk(root)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
