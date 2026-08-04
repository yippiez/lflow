package editor

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Reading a WRITE tool call as a diff.
//
// The agentic CLIs all edit files through a tool, and each records the call in
// its transcript with the text it put in — and, for a replacement, the text it
// took out. That is a diff already; nothing here computes one. A CLI whose write
// tool records only "I wrote something" yields no diff and the trace row stays
// an ordinary tool line, which is the honest reading of what was recorded.
//
// WARNING (invariant): never synthesize a hunk. A diff shown here is what the
// tool call said it did, verbatim — the file on disk has moved on since, and a
// diff lflow reconstructed by reading it would be a different claim entirely.

// agentWriteTools names the write tools by the argument shape they record,
// keyed by the lowercased tool name. Adding a CLI's tool is one line.
//
//	replace  old_string → new_string on a path      (Edit, str_replace_editor…)
//	create   the whole file's content on a path     (Write, create_file…)
//	multi    a list of replacements on a path       (MultiEdit…)
var agentWriteTools = map[string]string{
	"edit":               "replace",
	"edit_file":          "replace",
	"editfile":           "replace",
	"str_replace":        "replace",
	"str_replace_based":  "replace",
	"str_replace_editor": "replace",
	"replace_in_file":    "replace",
	"apply_patch":        "replace",
	"patch":              "replace",

	"write":       "create",
	"write_file":  "create",
	"writefile":   "create",
	"create_file": "create",
	"createfile":  "create",
	"new_file":    "create",

	"multiedit":  "multi",
	"multi_edit": "multi",
	"edits":      "multi",
}

// agentToolDiff reads a tool call's arguments as a file change, or nil when the
// call is not a write or did not record what it wrote.
func agentToolDiff(name string, args map[string]any) *agentDiff {
	if args == nil {
		return nil
	}
	shape, ok := agentWriteTools[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil
	}
	path := agentString(args, "file_path", "filePath", "path", "file", "filename", "target_file")
	d := &agentDiff{path: path}
	switch shape {
	case "replace":
		old := agentString(args, "old_string", "oldString", "old_str", "old", "search")
		neu := agentString(args, "new_string", "newString", "new_str", "new", "replace")
		if old == "" && neu == "" {
			return nil
		}
		d.hunks = []agentHunk{{old: old, new: neu}}
	case "create":
		content := agentString(args, "content", "contents", "text", "body", "file_text")
		if content == "" {
			return nil
		}
		d.hunks = []agentHunk{{new: content}}
	case "multi":
		raw, _ := args["edits"].([]any)
		for _, e := range raw {
			edit, ok := e.(map[string]any)
			if !ok {
				continue
			}
			old := agentString(edit, "old_string", "oldString", "old_str", "old")
			neu := agentString(edit, "new_string", "newString", "new_str", "new")
			if old == "" && neu == "" {
				continue
			}
			d.hunks = append(d.hunks, agentHunk{old: old, new: neu})
		}
		if len(d.hunks) == 0 {
			return nil
		}
	}
	return d
}

// ── the Diff node's body ───────────────────────────────────────────────────

// diffBody is the text a Diff node carries: a `--- path` header followed by the
// hunks in ordinary unified-diff notation, so the node's content is readable
// wherever it lands — an export, the CLI, a paste into a review — and not only
// inside the viewer that colors it.
func diffBody(d *agentDiff) string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	if d.path != "" {
		b.WriteString("--- " + d.path + "\n")
	}
	for i, h := range d.hunks {
		if i > 0 {
			b.WriteString("@@\n")
		}
		for _, line := range splitDiffLines(h.old) {
			b.WriteString("-" + line + "\n")
		}
		for _, line := range splitDiffLines(h.new) {
			b.WriteString("+" + line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// diffStat counts the lines a diff adds and removes — the "+12 −3" a Diff row
// wears, so a folded change still says how big it is.
func diffStat(body string) (added, removed int) {
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			removed++
		}
	}
	return added, removed
}

// diffPath is the file a Diff node's body names, for the row's own label.
func diffPath(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(line, "--- "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// ── rendering ──────────────────────────────────────────────────────────────

// diffRender is the Diff node's row: the file it changed and the size of the
// change. The body is multi-line and belongs under ⌥e, not squeezed onto a row.
func diffRender(it *item, _ string) string {
	body := it.name
	path := diffPath(body)
	if path == "" {
		path = "diff"
	}
	added, removed := diffStat(body)
	out := cDim + "⬚ " + cReset + cFG + path + cReset
	if added > 0 || removed > 0 {
		out += "  " + cGreen + "+" + strconv.Itoa(added) + cReset + " " + cRed + "−" + strconv.Itoa(removed) + cReset
	}
	return out + cDim + " · ⌥e read" + cReset
}

// diffLineColor paints one diff line: added green, removed red, the file header
// and hunk marks dim. Nothing else is colored — a diff is read by its margin.
func diffLineColor(line string) string {
	switch {
	case strings.HasPrefix(line, "---") || strings.HasPrefix(line, "@@"):
		return cDim + line + cReset
	case strings.HasPrefix(line, "+"):
		return cGreen + line + cReset
	case strings.HasPrefix(line, "-"):
		return cRed + line + cReset
	}
	return cFG + line + cReset
}

// diffToContext ships the diff as its element's multi-line body — a Diff node's
// name IS the patch, and flattening it to one line would mangle it.
func diffToContext(it *item) contextXML {
	return contextXML{tag: "diff", attrs: attrIfSet("path", diffPath(it.name)), body: it.name}
}

func attrIfSet(name, value string) string {
	if value == "" {
		return ""
	}
	return name + `="` + value + `"`
}

// ── the ⌥e view ────────────────────────────────────────────────────────────

// diffView is the read-only band under a Diff row: the patch, colored, scrolling
// with ↑↓. There is no edit path at all — a diff is a record of something that
// already happened, and typing into it would be writing history.
type diffView struct{}

func (diffView) Enter(m *Model, it *item) bool { return it != nil && strings.TrimSpace(it.name) != "" }

func (diffView) Leave(m *Model, it *item) {}

func (diffView) Lines(m *Model, it *item, width int) int {
	return len(diffViewContent(it, "", width))
}

// Key swallows every key but the ones that scroll or leave: the band is a
// reading surface. Returning false for the rest lets esc/⌥e defocus as usual.
func (diffView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "up", "down", "pgup", "pgdown":
		return nil, false // the generic focus scroller handles these
	}
	if k.Type == tea.KeyRunes {
		return nil, true // a keystroke never edits a diff
	}
	return nil, false
}

func (diffView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	return NodeWindowBands(diffViewContent(it, rail, width), scroll, winH)
}

func diffViewContent(it *item, rail string, width int) []string {
	if it == nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(it.name, "\n") {
		out = append(out, clip(rail+"  "+diffLineColor(line), width))
	}
	return append(out, clip(rail+"  "+cDim+"↑↓ read · esc close"+cReset, width))
}

// diffFlashActions: a diff is read, never run.
func diffFlashActions(m *Model, it *item) []flashAction {
	return []flashAction{{verb: "read", color: cCyan, do: diffExpandDo}}
}

func diffExpandDo(m *Model, it *item) tea.Cmd {
	if (diffView{}).Enter(m, it) {
		m.focused = true
		m.focusScroll = 0
	}
	return nil
}

// diffGlyph marks a Diff row with the same hollow square its chip wears.
func diffGlyph(it *item) (string, string) { return "⬚", cDim }
