package editor

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/browser"
	"github.com/lflow/lflow/pkg/tui/database"
)

// fzfPickedMsg carries the file the fzf picker selected, to splice a path chip
// into the node at the caret position captured when the picker opened.
type fzfPickedMsg struct {
	uuid  string
	caret int
	path  string
}

// openFilePicker runs fzf (suspending the inline UI, like the $EDITOR open) for a
// fuzzy file pick under the working dir, then splices the chosen path in as a
// chip. Returns nil (with a flash) when fzf isn't installed.
func (m *Model) openFilePicker(cur *item) tea.Cmd {
	if cur == nil {
		return nil
	}
	if _, err := exec.LookPath("fzf"); err != nil {
		m.flash = "fzf not found — install it to pick files"
		return nil
	}
	uuid, caret := cur.uuid, m.caret
	c := exec.Command("fzf", "--prompt", "file> ")
	var out bytes.Buffer
	c.Stdout = &out // fzf draws on /dev/tty and prints the selection to stdout
	return tea.ExecProcess(c, func(error) tea.Msg {
		return fzfPickedMsg{uuid: uuid, caret: caret, path: strings.TrimSpace(out.String())}
	})
}

// insertPathChip splices a path chip for absPath into cur.name at caret. For the
// embeddable file types (.html / .md) it also snapshots the file's content into
// the chip's data at pick time (see fileEmbeds).
func (m *Model) insertPathChip(cur *item, caret int, absPath string) {
	anchor, id := m.createChipID(chipKindPath, absPath)
	if anchor == "" {
		return
	}
	m.embedFileChip(id, absPath)
	runes := []rune(cur.name)
	if caret > len(runes) {
		caret = len(runes)
	}
	if caret < 0 {
		caret = 0
	}
	cur.name = string(runes[:caret]) + anchor + string(runes[caret:])
	m.caret = caret + len([]rune(anchor))
	m.unsaved = true
}

// ── per-extension file-chip behavior ───────────────────────────────────────
//
// A path chip is normally just a reference opened in $EDITOR on ⌥e. Some file
// types get special handling, declared once here: an .html/.md chip snapshots
// the file's bytes into the chip's data (the artifacts table, keyed by chip id)
// when it is picked, and an .html chip opens that saved page in the browser on
// ⌥e instead of $EDITOR. A .md chip is embedded too but still opens in $EDITOR.
type fileEmbed struct {
	kind          string                                  // stored content kind ("html"/"md")
	embedOnCreate bool                                    // snapshot bytes into chip data at pick time
	open          func(m *Model, id, path string) tea.Cmd // ⌥e override; nil → default ($EDITOR)
}

var fileEmbeds = map[string]fileEmbed{
	".html": {kind: "html", embedOnCreate: true, open: openFileChipInBrowser},
	".htm":  {kind: "html", embedOnCreate: true, open: openFileChipInBrowser},
	".md":   {kind: "md", embedOnCreate: true, open: nil}, // embedded, but ⌥e edits the file
}

// fileEmbedOf returns the embed descriptor for a path's extension, if any.
func fileEmbedOf(path string) (fileEmbed, bool) {
	e, ok := fileEmbeds[strings.ToLower(filepath.Ext(path))]
	return e, ok
}

// embedFileChip snapshots an embeddable file's bytes into the chip's data so the
// content lives in the DB independent of the on-disk file. Non-embeddable types
// are a no-op (the chip stays a plain path reference).
func (m *Model) embedFileChip(id, absPath string) {
	e, ok := fileEmbedOf(absPath)
	if !ok || !e.embedOnCreate {
		return
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		m.flash = "embed failed: " + err.Error()
		return
	}
	if m.ctx.DB != nil {
		_ = database.UpsertArtifact(m.ctx.DB, database.Artifact{
			ID: id, Name: filepath.Base(absPath), Kind: e.kind, Content: string(data),
		})
	}
}

// openFileChipInBrowser renders an .html chip's saved snapshot to a cache file
// and opens it in the browser (fire-and-forget). It falls back to the live file
// when no snapshot is stored (e.g. a chip from before the embed existed).
func openFileChipInBrowser(m *Model, id, path string) tea.Cmd {
	content := ""
	if m.ctx.DB != nil {
		if a, err := database.GetArtifact(m.ctx.DB, id); err == nil {
			content = a.Content
		}
	}
	target := path // live file fallback
	if content != "" {
		dir := filepath.Join(m.ctx.Paths.Cache, "lflow", "embeds")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			m.flash = "open failed: " + err.Error()
			return nil
		}
		cacheFile := filepath.Join(dir, id+".html")
		if err := os.WriteFile(cacheFile, []byte(content), 0o644); err != nil {
			m.flash = "open failed: " + err.Error()
			return nil
		}
		target = cacheFile
	}
	if err := browser.Open("file://" + target); err != nil {
		m.flash = "open failed: " + err.Error()
		return nil
	}
	m.flash = "opened ▣ " + clipStr(filepath.Base(path), 32)
	return nil
}

// openPathChipCmd opens the cursor node's path chip on ⌥e. The path chip the
// caret sits on (or just after) wins; otherwise the node's first path chip. The
// file type decides the action: an .html chip opens its saved page in the
// browser, everything else opens the file in $EDITOR (nvim fallback). Returns
// nil when the node has no path chip (or the action is fire-and-forget).
func (m *Model) openPathChipCmd(cur *item) tea.Cmd {
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
		if c, ok := m.chips[sp.id]; ok && c.Kind == chipKindPath {
			if e, ok := fileEmbedOf(c.Value); ok && e.open != nil {
				return e.open(m, c.ID, c.Value)
			}
			return openPathInEditor(m, c.Value)
		}
	}
	return nil
}

// openPathInEditor launches $EDITOR (or nvim) on path, suspending the inline UI
// while it runs and resuming after it exits.
func openPathInEditor(m *Model, path string) tea.Cmd {
	if strings.TrimSpace(path) == "" {
		m.flash = "empty path"
		return nil
	}
	ed := os.Getenv("EDITOR")
	if ed == "" {
		ed = "nvim"
	}
	parts := strings.Fields(ed)
	c := exec.Command(parts[0], append(parts[1:], path)...)
	return tea.ExecProcess(c, func(error) tea.Msg { return nil })
}

// Path helpers for the path chip (created by the /file fzf picker above; opened in
// $EDITOR by ⌥e). expandHome/normalizeFilePath/absolutizePath resolve the picked
// path to an absolute value before it is stored in the chip record.

// expandHome turns a leading ~ into the user's home directory. Other paths are
// returned unchanged.
func expandHome(p string) string {
	if p == "~" {
		if h, err := os.UserHomeDir(); err == nil {
			return h
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// normalizeFilePath expands ~ and resolves a relative path against the working
// directory, yielding a cleaned absolute path. Empty input is left as-is.
func normalizeFilePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	p = expandHome(p)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// absolutizePath expands ~ and resolves a path to absolute, preserving a
// trailing slash (so a completed directory keeps drilling).
func absolutizePath(p string) string {
	trailing := strings.HasSuffix(p, "/")
	abs := normalizeFilePath(p)
	if trailing && !strings.HasSuffix(abs, string(filepath.Separator)) {
		abs += string(filepath.Separator)
	}
	return abs
}
