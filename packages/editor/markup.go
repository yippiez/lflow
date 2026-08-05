package editor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/packages/database"
	"github.com/lflow/lflow/packages/nodes"
)

// The SVG and HTML nodes (database.TypeSVG / TypeHTML) are markup composed AS an
// outline, the way the Math node is an expression composed as an outline. There
// is no parser and no separate editor: a node's text is either an ELEMENT — a
// known tag name followed by its attributes — whose children are its child
// elements, or plain TEXT content. The outline structure IS the document tree.
//
//	○ svg viewBox="0 0 24 24"
//	├─ ○ circle cx=12 cy=12 r=10 fill=#569cd6
//	╰─ ○ text x=12 y=20
//	   ╰─ ○ hello
//
// Like Math, the type declares itself with ONE visual signal — a known tag name
// renders in the accent color and its attribute names dim, while the row stays
// fully inline-editable — and an element row carries a dim linear preview of its
// subtree so the shape reads without expanding.
//
// alt+r on an HTML node writes the serialized markup into its run band, exactly
// as alt+r on a Math node writes LaTeX. alt+r on an SVG node goes one further:
// it rasterizes the serialized document and stores the PNG as the node's blob,
// so the picture draws inline through the Image node's own half-block renderer
// (⌥e opens the scrollable view). Run it on any node to get just that subtree —
// an SVG node nested inside another renders only itself.
//
// This file is both types: the tag table that drives coloring AND serialization,
// the pure serializer/preview, and the two run hooks.
//
// The declarative shape (key, label, continueOnEnter, runInTail) lives in
// packages/nodes/svg.go and packages/nodes/html.go; this file keeps the
// Model-bound engine for both, attached here since none of it is pure over
// Ref/Theme.
func init() {
	attachCoreHooks(database.TypeSVG, coreHooks{
		onType:       markupOnType,
		run:          runSVGRender, // alt+r: rasterize the subtree into the node's picture
		view:         markupView{}, // alt+e: the picture once rendered, else the document
		flashActions: svgFlashActions,
		bands:        func(m *Model, r row, below bool, maxLine int) []string { return m.svgBandLines(r, below, maxLine) },
	})
	attachCoreHooks(database.TypeHTML, coreHooks{
		onType:       markupOnType,
		run:          runHTMLOut,   // alt+r: the serialized markup into the run band
		view:         markupView{}, // alt+e: the whole document the row shows the head of
		flashActions: htmlFlashActions,
	})
}

// ── the tag table: one source for coloring AND serialization ───────────────

// markupTagInfo is one entry of the tag table: whether the element is VOID (it
// closes itself and never takes children — <br>, <img>, <circle>) and which
// language it belongs to, so an SVG node does not tint HTML's tags and vice
// versa. A tag in both (a, title, style, script) is listed in both.
type markupTagInfo struct {
	void bool
	html bool
	svg  bool
}

// markupTags maps a lowercased tag name to its entry. A new element is one line
// here — it teaches the node to both color and serialize the tag.
var markupTags = buildMarkupTags()

func buildMarkupTags() map[string]markupTagInfo {
	m := map[string]markupTagInfo{}
	add := func(names string, info markupTagInfo) {
		for _, n := range strings.Fields(names) {
			cur := m[n]
			cur.void = cur.void || info.void
			cur.html = cur.html || info.html
			cur.svg = cur.svg || info.svg
			m[n] = cur
		}
	}
	html := markupTagInfo{html: true}
	htmlVoid := markupTagInfo{html: true, void: true}
	svg := markupTagInfo{svg: true}
	svgVoid := markupTagInfo{svg: true, void: true}

	// HTML: structure, sectioning, grouping
	add(`html head body header footer main section article aside nav
	     div p span h1 h2 h3 h4 h5 h6 ul ol li dl dt dd
	     blockquote pre code figure figcaption details summary dialog`, html)
	// HTML: text level
	add(`a em strong small s cite q abbr time var samp kbd sub sup
	     b i u mark ruby rt rp bdi bdo data`, html)
	// HTML: forms and tables
	add(`form label button select datalist optgroup option textarea output
	     progress meter fieldset legend table caption colgroup thead tbody
	     tfoot tr td th`, html)
	// HTML: embedded and metadata
	add(`title style script noscript iframe object video audio canvas map picture template slot`, html)
	add(`br hr img input col embed source track wbr area link meta base`, htmlVoid)

	// SVG: containers and shapes
	add(`svg g defs symbol marker pattern mask clipPath filter linearGradient
	     radialGradient text tspan textPath a title desc style script switch
	     foreignObject`, svg)
	add(`rect circle ellipse line polyline polygon path stop use image
	     animate animateTransform set feGaussianBlur feOffset feBlend
	     feColorMatrix feFlood feComposite feMerge feMergeNode`, svgVoid)
	return m
}

// markupElement is one row read as markup: either an element with attributes, or
// the row's own words as text content.
type markupElement struct {
	tag   string
	void  bool
	attrs []markupAttr
	text  string // set when the row is text content rather than an element
}

type markupAttr struct{ name, value string }

// elementChipLabel is how an element chip reads on the row: the spec inside
// angle brackets, so the reserved head looks like the markup it becomes.
func elementChipLabel(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "<>"
	}
	return "<" + spec + ">"
}

// markupElementChip returns the element chip reserving the head of a markup row,
// if it has one.
func (m *Model) markupElementChip(it *item) (database.Chip, bool) {
	if it == nil || !markupType(it.typ) {
		return database.Chip{}, false
	}
	for _, sp := range anchorSpans([]rune(it.name)) {
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindElement {
			return c, true
		}
	}
	return database.Chip{}, false
}

func markupType(typ string) bool {
	return typ == database.TypeSVG || typ == database.TypeHTML
}

// markupTextOf is a row's own text: everything that is not the element chip.
func (m *Model) markupTextOf(it *item) string {
	if it == nil {
		return ""
	}
	runes := []rune(it.name)
	var b strings.Builder
	i := 0
	for _, sp := range anchorSpans(runes) {
		c, ok := m.chips[sp.id]
		if ok && c.Kind == chipKindElement {
			b.WriteString(string(runes[i:sp.start]))
			i = sp.end
			continue
		}
	}
	b.WriteString(string(runes[i:]))
	return strings.TrimSpace(b.String())
}

// markupParse reads a row through its RESERVED element chip. Nothing is guessed
// from the words: a row with an element chip is that element, and a row without
// one is text. Half of HTML's tag names are ordinary English words — "a
// headline", "code review", "time to ship" — so reading the first word as a tag
// turned prose into markup at random. The reserved slot is what removes the
// guess, the way a Line node reserves its character.
func (m *Model) markupParse(it *item) markupElement {
	if it == nil {
		return markupElement{}
	}
	text := m.markupTextOf(it)
	c, ok := m.markupElementChip(it)
	if !ok {
		return markupElement{text: text}
	}
	head, rest := splitFirstWord(strings.TrimSpace(c.Value))
	if head == "" {
		return markupElement{text: text}
	}
	info := markupTags[head]
	return markupElement{tag: head, void: info.void && text == "", attrs: parseMarkupAttrs(rest), text: text}
}

func splitFirstWord(s string) (head, rest string) {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

// parseMarkupAttrs reads `key=value` pairs, honoring quotes so a value may hold
// spaces (viewBox="0 0 24 24"). A bare word with no "=" is a valueless attribute,
// which is how HTML writes disabled/checked/hidden.
func parseMarkupAttrs(s string) []markupAttr {
	var out []markupAttr
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
		start := i
		for i < len(runes) && runes[i] != '=' && runes[i] != ' ' && runes[i] != '\t' {
			i++
		}
		if i == start {
			break
		}
		name := string(runes[start:i])
		if i >= len(runes) || runes[i] != '=' {
			out = append(out, markupAttr{name: name})
			continue
		}
		i++ // the '='
		var value string
		if i < len(runes) && (runes[i] == '"' || runes[i] == '\'') {
			quote := runes[i]
			i++
			vs := i
			for i < len(runes) && runes[i] != quote {
				i++
			}
			value = string(runes[vs:i])
			if i < len(runes) {
				i++ // the closing quote
			}
		} else {
			vs := i
			for i < len(runes) && runes[i] != ' ' && runes[i] != '\t' {
				i++
			}
			value = string(runes[vs:i])
		}
		out = append(out, markupAttr{name: name, value: value})
	}
	return out
}

// ── coloring ───────────────────────────────────────────────────────────────

// markupOnType runs when /type makes a node SVG or HTML: it reserves the row's
// element slot and opens it straight away, because a markup node with no element
// is not yet anything. The same field reopens with ⌥i, which is already "edit the
// structured thing inside this row" for a $ chip.
func markupOnType(m *Model, it *item) {
	c, ok := m.markupElementChip(it)
	if !ok {
		id := m.createLabeledChip(chipKindElement, "", elementChipLabel(""))
		if id == "" {
			return
		}
		// the element is the row's HEAD: it goes in front of whatever was typed
		it.name = id + strings.TrimSpace(it.name)
		m.unsaved = true
		if c, ok = m.markupElementChip(it); !ok {
			return
		}
	}
	m.openCmdEdit(c)
}

// ── serialization: subtree → markup ────────────────────────────────────────

// markupSerialize renders a node's subtree as markup, indented. Pure and
// recursive, so running it on any node yields exactly that node's document —
// the same property that lets alt+r on a Math sub-node export its sub-expression.
func (m *Model) markupSerialize(it *item, indent int) string {
	var b strings.Builder
	m.writeMarkup(&b, it, indent)
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) writeMarkup(b *strings.Builder, it *item, depth int) {
	if it == nil {
		return
	}
	pad := strings.Repeat("  ", depth)
	el := m.markupParse(it)
	kids := markupKids(it)

	if el.tag == "" {
		// a text row: its words, escaped. Children of a text row still belong to
		// the document — they nest under whatever contains it — so they follow.
		if el.text != "" {
			b.WriteString(pad + escapeMarkupText(el.text) + "\n")
		}
		for _, c := range kids {
			m.writeMarkup(b, c, depth)
		}
		return
	}

	open := "<" + el.tag
	for _, a := range el.attrs {
		if a.value == "" {
			open += " " + a.name
			continue
		}
		open += " " + a.name + `="` + escapeMarkupAttr(a.value) + `"`
	}
	if len(kids) == 0 && el.text == "" {
		if el.void {
			b.WriteString(pad + open + "/>\n")
		} else {
			b.WriteString(pad + open + "></" + el.tag + ">\n")
		}
		return
	}
	b.WriteString(pad + open + ">\n")
	if el.text != "" {
		b.WriteString(pad + "  " + escapeMarkupText(el.text) + "\n")
	}
	for _, c := range kids {
		m.writeMarkup(b, c, depth+1)
	}
	b.WriteString(pad + "</" + el.tag + ">\n")
}

// markupKids is the children a markup node serializes: the real ones, resolved
// through any mirror, and never a divider — a rule is outline furniture, not
// content.
func markupKids(it *item) []*item {
	var out []*item
	for _, c := range it.children {
		if c.typ == database.TypeDivider {
			continue
		}
		out = append(out, c)
	}
	return out
}

func escapeMarkupText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func escapeMarkupAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;")
	return r.Replace(s)
}

// ── preview ────────────────────────────────────────────────────────────────

// markupMark is the sign a markup row wears at its end so the type is legible
// without reading the tag: HTML's own "</>", and the same brackets around a
// shape for SVG. It sits AFTER the text rather than in front of it because the
// row's first word is already the tag — the mark says which language that tag
// belongs to, which is the question the row leaves open.
func markupMark(typ string) string {
	switch typ {
	case database.TypeHTML:
		return "</>"
	case database.TypeSVG:
		return "<◇>"
	}
	return ""
}

// markupTailCap is how much of the document a row shows before "…". A row is a
// glance; ⌥e is the reading.
const markupTailCap = 44

// markupBodyTail is what a markup row carries after its own text: the type's
// mark, then the document it composes, truncated — "→ result …", the same shape
// a Bash row's output tail has. The whole thing is under ⌥e.
// markupPreview flattens a subtree into one compact line: nested tags as
// <tag>…</tag> with their text inline, so the document's shape reads at a glance.
func (m *Model) markupPreview(it *item) string {
	if it == nil {
		return ""
	}
	el := m.markupParse(it)
	kids := markupKids(it)
	if el.tag == "" {
		parts := make([]string, 0, len(kids)+1)
		if el.text != "" {
			parts = append(parts, el.text)
		}
		for _, c := range kids {
			parts = append(parts, m.markupPreview(c))
		}
		return strings.Join(parts, " ")
	}
	if len(kids) == 0 && el.text == "" {
		return "<" + el.tag + "/>"
	}
	inner := make([]string, 0, len(kids)+1)
	if el.text != "" {
		inner = append(inner, el.text)
	}
	for _, c := range kids {
		inner = append(inner, m.markupPreview(c))
	}
	return "<" + el.tag + ">" + strings.Join(inner, " ") + "</" + el.tag + ">"
}

// ── alt+r ──────────────────────────────────────────────────────────────────

// runHTMLOut writes the serialized document into the node's ephemeral run band,
// exactly as alt+r on a Math node writes its LaTeX.
func runHTMLOut(m *Model, it *item) tea.Cmd {
	doc := m.markupSerialize(it, 0)
	r := m.ensureRun(it.uuid)
	r.out = nil
	for _, line := range strings.Split(doc, "\n") {
		r.out = append(r.out, outLine{text: line})
	}
	r.dropped = 0
	m.persistRunOut(it.uuid)
	m.flash = "HTML → output"
	m.refreshRows()
	return nil
}

// runSVGRender serializes the subtree, rasterizes it, and keeps the PNG as the
// node's blob — the same blob the Image node draws from, so the picture appears
// inline under the row and ⌥e opens the scrollable view. The document is written
// whole every time: the picture is a VIEW of the outline, never a copy of it
// that could drift.
func runSVGRender(m *Model, it *item) tea.Cmd {
	doc := m.svgDocument(it)
	png, err := rasterizeSVG(doc)
	if err != nil {
		m.errorFlash("svg: " + err.Error())
		return nil
	}
	if m.db == nil {
		m.flash = "svg rendered · no database to keep it in"
		return nil
	}
	if err := database.PutBlob(m.db, database.Blob{UUID: it.uuid, Mime: "image/png", Bytes: png}); err != nil {
		m.errorFlash("svg: " + err.Error())
		return nil
	}
	m.imageInvalidate(it.uuid)
	m.flash = "svg rendered"
	m.refreshRows()
	return nil
}

// svgDocument is the subtree as a standalone SVG document. A subtree whose own
// root is not an <svg> is wrapped in one, so alt+r on an inner shape still
// renders something rather than markup no renderer will accept.
func (m *Model) svgDocument(it *item) string {
	doc := m.markupSerialize(it, 0)
	el := m.markupParse(it)
	if el.tag == "svg" {
		if !strings.Contains(doc, "xmlns=") {
			doc = strings.Replace(doc, "<svg", `<svg xmlns="http://www.w3.org/2000/svg"`, 1)
		}
		return doc
	}
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg">` + "\n")
	for _, line := range strings.Split(doc, "\n") {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("</svg>")
	return b.String()
}

// svgRasterizers are the tools tried, in order of how faithfully each renders
// SVG. librsvg and resvg are real SVG renderers; ImageMagick delegates to one
// when it has one and falls back to its own partial renderer when it does not,
// which is why it is last rather than absent.
var svgRasterizers = []struct {
	bin  string
	args func(in, out string) []string
}{
	{"rsvg-convert", func(in, out string) []string { return []string{"-o", out, in} }},
	{"resvg", func(in, out string) []string { return []string{in, out} }},
	{"magick", func(in, out string) []string { return []string{"-background", "none", in, out} }},
	{"convert", func(in, out string) []string { return []string{"-background", "none", in, out} }},
}

// rasterizeSVG renders a document to PNG bytes with whichever rasterizer the
// machine has. Nothing is left on disk: the temp pair is removed either way.
func rasterizeSVG(doc string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "lflow-svg")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.svg")
	out := filepath.Join(dir, "out.png")
	if err := os.WriteFile(in, []byte(doc), 0o600); err != nil {
		return nil, err
	}

	var tried []string
	for _, r := range svgRasterizers {
		path, lookErr := exec.LookPath(r.bin)
		if lookErr != nil {
			continue
		}
		tried = append(tried, r.bin)
		var stderr bytes.Buffer
		cmd := exec.Command(path, r.args(in, out)...)
		cmd.Stderr = &stderr
		if runErr := cmd.Run(); runErr != nil {
			continue
		}
		png, readErr := os.ReadFile(out)
		if readErr != nil || len(png) == 0 {
			continue
		}
		return png, nil
	}
	if len(tried) == 0 {
		return nil, fmt.Errorf("no rasterizer — install one of %s", svgRasterizerNames())
	}
	return nil, fmt.Errorf("%s could not render it", strings.Join(tried, "/"))
}

func svgRasterizerNames() string {
	names := make([]string, len(svgRasterizers))
	for i, r := range svgRasterizers {
		names[i] = r.bin
	}
	return strings.Join(names, ", ")
}

// ── ⌥e: the whole document, or the picture it renders to ───────────────────

// markupView is what ⌥e opens on a markup node: the picture, when an SVG has
// been rendered, and otherwise the document itself — the full markup the row
// could only show the head of. Read-only both ways: the document is composed in
// the outline above, which is the whole point of the type, so there is nothing
// to type into down here.
type markupView struct{}

// picture reports whether this node has a rendered image to show instead of its
// source — only an SVG ever does, and only after ⌥r.
func (markupView) picture(m *Model, it *item) bool {
	if it == nil || it.typ != database.TypeSVG {
		return false
	}
	_, ok := m.imageLoad(it.uuid)
	return ok
}

func (v markupView) enter(m *Model, it *item) bool {
	if v.picture(m, it) {
		return (imageView{}).enter(m, it)
	}
	return it != nil
}

func (v markupView) leave(m *Model, it *item) {}

func (v markupView) lines(m *Model, it *item, width int) int {
	if v.picture(m, it) {
		return (imageView{}).lines(m, it, width)
	}
	return len(m.markupViewContent(it, "", width))
}

func (v markupView) key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	if v.picture(m, it) {
		return (imageView{}).key(m, it, k)
	}
	if k.Type == tea.KeyRunes {
		return nil, true // the document is composed in the outline, not in here
	}
	return nil, false
}

func (v markupView) bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	if v.picture(m, it) {
		return (imageView{}).bands(m, it, rail, width, scroll, winH, focused)
	}
	return nodes.WindowBands(m.markupViewContent(it, rail, width), scroll, winH)
}

// markupViewContent is the serialized document, one line per line, tinted the
// same way the rows above are so the two readings match.
func (m *Model) markupViewContent(it *item, rail string, width int) []string {
	if it == nil {
		return nil
	}
	doc := m.markupSerialize(it, 0)
	if it.typ == database.TypeSVG {
		doc = m.svgDocument(it)
	}
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		out = append(out, clip(rail+"  "+cFG+line+cReset, width))
	}
	hint := "↑↓ read · esc close"
	if it.typ == database.TypeSVG {
		hint = "↑↓ read · ⌥r renders · esc close"
	}
	return append(out, clip(rail+"  "+cDim+hint+cReset, width))
}

// svgBandLines hangs the rendered picture under the SVG row, like a note band.
// Unlike the Image node's thumbnail this is not behind a preview setting: the
// markup on the row is already the "compact" reading of the node, so the picture
// is the only thing the band has to add — a picture you rendered on purpose and
// then could not see would be a strange thing to ship.
func (m *Model) svgBandLines(r row, subtreeBelow bool, maxLine int) []string {
	info, ok := m.imageLoad(r.it.uuid)
	if !ok {
		return nil
	}
	rail := continuationPrefix(r, subtreeBelow)
	avail := maxLine - visibleWidth(rail) - 2
	if avail < 4 {
		return nil
	}
	cols, rows := halfBlockBox(info.img, min(avail, thumbMaxCols), thumbMaxRows)
	out := make([]string, 0, rows)
	for _, l := range halfBlockRender(info.img, cols, rows) {
		out = append(out, clip(rail+cReset+"  "+l, maxLine))
	}
	return out
}

// svgFlashActions / htmlFlashActions name the alt+r action in the flash bar.
func svgFlashActions(m *Model, it *item) []flashAction {
	return []flashAction{
		{verb: "render", color: cGreen, do: runSVGRender},
		{verb: "expand", color: cCyan, do: markupExpandDo},
	}
}

func markupExpandDo(m *Model, it *item) tea.Cmd {
	if (markupView{}).enter(m, it) {
		m.focused = true
		m.focusScroll = 0
	}
	return nil
}

func htmlFlashActions(m *Model, it *item) []flashAction {
	return []flashAction{
		{verb: "markup", color: cGreen, do: runHTMLOut},
		{verb: "expand", color: cCyan, do: markupExpandDo},
	}
}
