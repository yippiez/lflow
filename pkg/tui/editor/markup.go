package editor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
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

// markupTagFor returns the tag entry for a word IN THE GIVEN LANGUAGE. A word
// the language does not name is not an element, which is what makes plain text a
// text node without any escaping gesture: write the words and they are the words.
func markupTagFor(typ, word string) (markupTagInfo, bool) {
	info, ok := markupTags[word]
	if !ok {
		return markupTagInfo{}, false
	}
	if typ == database.TypeSVG && !info.svg {
		return markupTagInfo{}, false
	}
	if typ == database.TypeHTML && !info.html {
		return markupTagInfo{}, false
	}
	return info, true
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

// markupParse reads a row. The first word decides: a tag the language knows opens
// an element and everything after it is attributes; anything else is text.
func markupParse(typ, name string) markupElement {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return markupElement{}
	}
	head, rest := splitFirstWord(trimmed)
	info, ok := markupTagFor(typ, head)
	if !ok {
		return markupElement{text: trimmed}
	}
	return markupElement{tag: head, void: info.void, attrs: parseMarkupAttrs(rest)}
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

// markupSpanColor tints a row the way mathSpanColor tints operators: the tag
// name in the accent color when the language knows it, and each attribute NAME
// dim so the values — the part you actually read — stay in the foreground. An
// unknown first word gets no tint at all, which is the row telling you it will
// serialize as text rather than as an element.
func markupSpanColor(it *item, runes []rune) map[int]string {
	if it == nil {
		return nil
	}
	s := string(runes)
	lead := 0
	for lead < len(runes) && (runes[lead] == ' ' || runes[lead] == '\t') {
		lead++
	}
	head, _ := splitFirstWord(strings.TrimSpace(s))
	if _, ok := markupTagFor(it.typ, head); !ok {
		return nil
	}
	out := make(map[int]string, len(runes))
	for i := lead; i < lead+len([]rune(head)) && i < len(runes); i++ {
		out[i] = cAccent
	}
	// attribute names: from the character after a space up to its "="
	i := lead + len([]rune(head))
	for i < len(runes) {
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
		start := i
		for i < len(runes) && runes[i] != '=' && runes[i] != ' ' && runes[i] != '\t' {
			i++
		}
		if i < len(runes) && runes[i] == '=' {
			for j := start; j <= i; j++ {
				out[j] = cDim
			}
			i++
			i = skipMarkupValue(runes, i)
			continue
		}
		for j := start; j < i; j++ {
			out[j] = cDim // a valueless attribute is still an attribute
		}
	}
	return out
}

// skipMarkupValue advances past an attribute value, quoted or bare.
func skipMarkupValue(runes []rune, i int) int {
	if i < len(runes) && (runes[i] == '"' || runes[i] == '\'') {
		quote := runes[i]
		i++
		for i < len(runes) && runes[i] != quote {
			i++
		}
		if i < len(runes) {
			i++
		}
		return i
	}
	for i < len(runes) && runes[i] != ' ' && runes[i] != '\t' {
		i++
	}
	return i
}

// ── serialization: subtree → markup ────────────────────────────────────────

// markupSerialize renders a node's subtree as markup, indented. Pure and
// recursive, so running it on any node yields exactly that node's document —
// the same property that lets alt+r on a Math sub-node export its sub-expression.
func markupSerialize(it *item, indent int) string {
	var b strings.Builder
	writeMarkup(&b, it, indent)
	return strings.TrimRight(b.String(), "\n")
}

func writeMarkup(b *strings.Builder, it *item, depth int) {
	if it == nil {
		return
	}
	pad := strings.Repeat("  ", depth)
	el := markupParse(it.typ, it.name)
	kids := markupKids(it)

	if el.tag == "" {
		// a text row: its words, escaped. Children of a text row still belong to
		// the document — they nest under whatever contains it — so they follow.
		if el.text != "" {
			b.WriteString(pad + escapeMarkupText(el.text) + "\n")
		}
		for _, c := range kids {
			writeMarkup(b, c, depth)
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
	if len(kids) == 0 {
		if el.void {
			b.WriteString(pad + open + "/>\n")
		} else {
			b.WriteString(pad + open + "></" + el.tag + ">\n")
		}
		return
	}
	b.WriteString(pad + open + ">\n")
	for _, c := range kids {
		writeMarkup(b, c, depth+1)
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

// markupBodyTail is the dim linear preview of an element's subtree, shown after
// the row's own text. A leaf is already the whole element inline, so it gets no
// tail — "simple stays inline", as in Math.
func markupBodyTail(it *item, _ map[string]database.Chip) string {
	if it == nil || len(markupKids(it)) == 0 {
		return ""
	}
	p := markupPreview(it)
	if p == "" {
		return ""
	}
	return cDim + " " + p + cReset
}

// markupPreview flattens a subtree into one compact line: nested tags as
// <tag>…</tag> with their text inline, so the document's shape reads at a glance.
func markupPreview(it *item) string {
	if it == nil {
		return ""
	}
	el := markupParse(it.typ, it.name)
	kids := markupKids(it)
	if el.tag == "" {
		parts := make([]string, 0, len(kids)+1)
		if el.text != "" {
			parts = append(parts, el.text)
		}
		for _, c := range kids {
			parts = append(parts, markupPreview(c))
		}
		return strings.Join(parts, " ")
	}
	if len(kids) == 0 {
		return "<" + el.tag + "/>"
	}
	inner := make([]string, 0, len(kids))
	for _, c := range kids {
		inner = append(inner, markupPreview(c))
	}
	return "<" + el.tag + ">" + strings.Join(inner, " ") + "</" + el.tag + ">"
}

// markupToContext gives the node its own element carrying the serialized
// document, so structured context reads the markup rather than a bare tag name.
func markupToContext(tag string) func(*item) contextXML {
	return func(it *item) contextXML {
		return contextXML{tag: tag, body: markupSerialize(it, 0)}
	}
}

// ── alt+r ──────────────────────────────────────────────────────────────────

// runHTMLOut writes the serialized document into the node's ephemeral run band,
// exactly as alt+r on a Math node writes its LaTeX.
func runHTMLOut(m *Model, it *item) tea.Cmd {
	doc := markupSerialize(it, 0)
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
	doc := svgDocument(it)
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
func svgDocument(it *item) string {
	doc := markupSerialize(it, 0)
	el := markupParse(database.TypeSVG, it.name)
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
	acts := []flashAction{{verb: "render", color: cGreen, do: runSVGRender}}
	if _, ok := m.imageLoad(it.uuid); ok {
		acts = append(acts, flashAction{verb: "view", color: cCyan, do: imageExpandDo})
	}
	return acts
}

func htmlFlashActions(m *Model, it *item) []flashAction {
	return []flashAction{{verb: "markup", color: cGreen, do: runHTMLOut}}
}
