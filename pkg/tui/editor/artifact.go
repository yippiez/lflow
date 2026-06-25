package editor

import (
	"bytes"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/browser"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/utils"
)

// An artifact is a self-contained web page embedded in the DB — an .html file,
// or a .md file rendered to html. It has two shapes that share one backing
// store (the artifacts table, see database/artifact.go):
//
//   - a NODE of type "artifact" — keyed by the node's uuid; alt+e opens it in
//     the browser, ▣ glyph, the node name is the label.
//   - an inline CHIP of kind "artifact" — keyed by the chip id; alt+e on a node
//     carrying one opens it. The chip value holds the label.
//
// Both are created with "?" (the artifact picker): on an empty node "?" makes an
// artifact node; mid-text it splices an artifact chip. The picker fuzzy-finds
// the supported file types (.md / .html) under the working dir.

const glyphArtifact = "▣"

const chipKindArtifact = "artifact"

// artifactGlyph is the ▣ block, cyan — distinct from the bullet ○ so an artifact
// node reads at a glance.
func artifactGlyph(it *item) (string, string) { return glyphArtifact, cCyan }

// artifactPickedMsg carries the file the artifact picker selected. asNode picks
// the shape: an empty node becomes an artifact node, otherwise a chip is spliced
// at the captured caret.
type artifactPickedMsg struct {
	uuid   string
	caret  int
	path   string
	asNode bool
}

// openArtifactPicker fuzzy-finds a .md/.html file under the working dir (fzf,
// suspending the inline UI like the file picker) and routes the pick back as an
// artifactPickedMsg. Returns nil (with a flash) when fzf isn't installed or no
// supported files are found.
func (m *Model) openArtifactPicker(cur *item) tea.Cmd {
	if cur == nil {
		return nil
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		m.flash = "fzf not found — install it to pick an artifact"
		return nil
	}
	files := findArtifactFiles()
	if len(files) == 0 {
		m.flash = "no .md or .html files found here"
		return nil
	}
	uuid, caret := cur.uuid, m.caret
	asNode := strings.TrimSpace(cur.name) == ""
	c := exec.Command("fzf", "--prompt", "artifact> ")
	c.Stdin = strings.NewReader(strings.Join(files, "\n")) // fzf draws on /dev/tty
	var out bytes.Buffer
	c.Stdout = &out
	return tea.ExecProcess(c, func(error) tea.Msg {
		return artifactPickedMsg{uuid: uuid, caret: caret, path: strings.TrimSpace(out.String()), asNode: asNode}
	})
}

// findArtifactFiles lists .md and .html files under the working dir, skipping
// hidden dirs (incl. .git), capped so a huge tree can't stall the picker.
func findArtifactFiles() []string {
	const cap = 5000
	var out []string
	root := "."
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".md", ".html", ".htm":
			out = append(out, strings.TrimPrefix(p, "./"))
		}
		if len(out) >= cap {
			return filepath.SkipAll
		}
		return nil
	})
	return out
}

// applyArtifactPick materializes a picked file into an artifact node or chip.
func (m *Model) applyArtifactPick(msg artifactPickedMsg) {
	it := m.tree.byUUID[msg.uuid]
	if it == nil || msg.path == "" {
		return
	}
	data, err := os.ReadFile(msg.path)
	if err != nil {
		m.flash = "read failed: " + err.Error()
		return
	}
	kind := artifactKind(msg.path)
	name := filepath.Base(msg.path)

	if msg.asNode && strings.TrimSpace(it.name) == "" && it.mirrorOf == "" {
		// turn the empty node into an artifact node — keyed by its own uuid
		a := database.Artifact{ID: it.uuid, Name: name, Kind: kind, Content: string(data)}
		if m.db != nil {
			if err := database.UpsertArtifact(m.db, a); err != nil {
				m.flash = "save failed: " + err.Error()
				return
			}
		}
		it.typ = database.TypeArtifact
		it.name = name
		m.caret = len([]rune(name))
		m.unsaved = true
		m.flash = "artifact · ⌥e opens in browser"
		return
	}

	// splice an artifact chip — content keyed by the new chip id
	anchor := m.createArtifactChip(name, kind, string(data))
	if anchor == "" {
		return
	}
	runes := []rune(it.name)
	caret := msg.caret
	if caret > len(runes) {
		caret = len(runes)
	}
	if caret < 0 {
		caret = 0
	}
	it.name = string(runes[:caret]) + anchor + string(runes[caret:])
	m.caret = caret + len([]rune(anchor))
	m.unsaved = true
	m.flash = "artifact chip · ⌥e opens in browser"
}

// createArtifactChip records an artifact chip (value = label) plus its content
// row (keyed by the chip id) and returns the in-text anchor to splice.
func (m *Model) createArtifactChip(name, kind, content string) string {
	id, err := utils.GenerateUUID()
	if err != nil {
		return ""
	}
	c := database.Chip{ID: id, Kind: chipKindArtifact, Value: name}
	if m.chips == nil {
		m.chips = map[string]database.Chip{}
	}
	m.chips[id] = c
	if m.db != nil {
		_ = database.UpsertChip(m.db, c)
		_ = database.UpsertArtifact(m.db, database.Artifact{ID: id, Name: name, Kind: kind, Content: content})
	}
	return chipAnchor(id)
}

// artifactKind maps a file extension to the stored kind.
func artifactKind(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".md") {
		return "md"
	}
	return "html"
}

// openArtifactNode opens an artifact node (keyed by node uuid) in the browser —
// the registry's alt+e expand hook for the artifact type.
func openArtifactNode(m *Model, it *item) tea.Cmd {
	m.openArtifactID(it.uuid)
	return nil
}

// openArtifactChipCmd opens the artifact chip the caret sits on (or after), else
// the node's first artifact chip, in the browser. Returns nil when the node has
// none (mirrors openPathChipCmd).
func (m *Model) openArtifactChipCmd(cur *item) tea.Cmd {
	if cur == nil {
		return nil
	}
	spans := anchorSpans([]rune(cur.name))
	var order []*anchorSpan
	if sp := spanStartingAt(spans, m.caret); sp != nil {
		order = append(order, sp)
	}
	if sp := spanEndingAt(spans, m.caret); sp != nil {
		order = append(order, sp)
	}
	for k := range spans {
		order = append(order, &spans[k])
	}
	for _, sp := range order {
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindArtifact {
			m.openArtifactID(sp.id)
			return nil
		}
	}
	return nil
}

// openArtifactID renders an artifact's content to a cache file and points the
// browser at it. The file is regenerated from the DB each open, so it survives a
// cleared cache and always reflects the stored bytes.
func (m *Model) openArtifactID(id string) {
	if m.db == nil {
		return
	}
	a, err := database.GetArtifact(m.db, id)
	if err != nil {
		m.flash = "open failed: " + err.Error()
		return
	}
	page := a.Content
	if a.Kind == "md" {
		page = markdownToHTML(a.Name, a.Content)
	}
	dir := filepath.Join(m.ctx.Paths.Cache, "lflow", "artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.flash = "open failed: " + err.Error()
		return
	}
	path := filepath.Join(dir, id+".html")
	if err := os.WriteFile(path, []byte(page), 0o644); err != nil {
		m.flash = "open failed: " + err.Error()
		return
	}
	if err := browser.Open("file://" + path); err != nil {
		m.flash = "open failed: " + err.Error()
		return
	}
	m.flash = "opened ▣ " + clipStr(a.Name, 32)
}

// markdownToHTML wraps a minimal markdown render in a styled html document. It
// is intentionally small — headings, fenced/inline code, bold/italic, links and
// paragraphs/lists — enough to read a note in the browser without a dependency.
func markdownToHTML(title, md string) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html><head><meta charset=\"utf-8\">\n")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	b.WriteString("<style>body{max-width:46rem;margin:2rem auto;padding:0 1rem;" +
		"font:16px/1.6 -apple-system,Segoe UI,Roboto,sans-serif;color:#222}" +
		"pre{background:#f5f5f5;padding:.75rem;overflow:auto;border-radius:6px}" +
		"code{background:#f5f5f5;padding:.1rem .3rem;border-radius:4px}" +
		"pre code{background:none;padding:0}" +
		"a{color:#2563eb}h1,h2,h3{line-height:1.25}blockquote{color:#555;border-left:3px solid #ddd;margin:0;padding-left:1rem}</style>\n")
	b.WriteString("</head><body>\n")
	b.WriteString(renderMarkdownBody(md))
	b.WriteString("\n</body></html>\n")
	return b.String()
}

// renderMarkdownBody converts markdown block structure to html. Line-based and
// tolerant; unknown syntax degrades to escaped text.
func renderMarkdownBody(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var b strings.Builder
	inCode, inList := false, false
	var para []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		b.WriteString("<p>" + mdInline(strings.Join(para, " ")) + "</p>\n")
		para = nil
	}
	closeList := func() {
		if inList {
			b.WriteString("</ul>\n")
			inList = false
		}
	}

	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			if inCode {
				b.WriteString("</code></pre>\n")
				inCode = false
			} else {
				flushPara()
				closeList()
				b.WriteString("<pre><code>")
				inCode = true
			}
			continue
		}
		if inCode {
			b.WriteString(html.EscapeString(ln) + "\n")
			continue
		}
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" {
			flushPara()
			closeList()
			continue
		}
		if h := headingLevel(trimmed); h > 0 {
			flushPara()
			closeList()
			text := mdInline(strings.TrimSpace(trimmed[h:]))
			b.WriteString("<h" + itoa(h) + ">" + text + "</h" + itoa(h) + ">\n")
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			flushPara()
			if !inList {
				b.WriteString("<ul>\n")
				inList = true
			}
			b.WriteString("<li>" + mdInline(strings.TrimSpace(trimmed[2:])) + "</li>\n")
			continue
		}
		if strings.HasPrefix(trimmed, "> ") {
			flushPara()
			closeList()
			b.WriteString("<blockquote>" + mdInline(strings.TrimSpace(trimmed[2:])) + "</blockquote>\n")
			continue
		}
		para = append(para, trimmed)
	}
	if inCode {
		b.WriteString("</code></pre>\n")
	}
	flushPara()
	closeList()
	return b.String()
}

// headingLevel returns the markdown heading level (1-6) for a "# " prefix, else 0.
func headingLevel(s string) int {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n >= 1 && n <= 6 && n < len(s) && s[n] == ' ' {
		return n
	}
	return 0
}

// mdInline renders inline markdown (escaped first): `code`, **bold**, *italic*,
// and [text](url) links.
func mdInline(s string) string {
	s = html.EscapeString(s)
	s = wrapDelim(s, "`", "<code>", "</code>")
	s = wrapDelim(s, "**", "<strong>", "</strong>")
	s = wrapDelim(s, "*", "<em>", "</em>")
	s = mdLinks(s)
	return s
}

// wrapDelim replaces matched pairs of delim with open/close tags, left to right.
func wrapDelim(s, delim, open, close string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, delim)
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.Index(s[i+len(delim):], delim)
		if j < 0 {
			b.WriteString(s)
			break
		}
		j += i + len(delim)
		b.WriteString(s[:i])
		b.WriteString(open + s[i+len(delim):j] + close)
		s = s[j+len(delim):]
	}
	return b.String()
}

// mdLinks rewrites [text](url) into anchors. url is already html-escaped.
func mdLinks(s string) string {
	var b strings.Builder
	for {
		open := strings.Index(s, "[")
		if open < 0 {
			b.WriteString(s)
			break
		}
		close := strings.Index(s[open:], "](")
		if close < 0 {
			b.WriteString(s)
			break
		}
		close += open
		end := strings.Index(s[close:], ")")
		if end < 0 {
			b.WriteString(s)
			break
		}
		end += close
		text := s[open+1 : close]
		url := s[close+2 : end]
		b.WriteString(s[:open])
		b.WriteString("<a href=\"" + url + "\">" + text + "</a>")
		s = s[end+1:]
	}
	return b.String()
}

// itoa is a tiny single-digit int→string for heading levels (1-6).
func itoa(n int) string { return string(rune('0' + n)) }
