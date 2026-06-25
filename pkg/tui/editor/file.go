package editor

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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

// insertPathChip splices a path chip for absPath into cur.name at caret.
func (m *Model) insertPathChip(cur *item, caret int, absPath string) {
	anchor := m.createChip(chipKindPath, absPath)
	if anchor == "" {
		return
	}
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

// openPathChipCmd opens the cursor node's path chip in $EDITOR (nvim fallback),
// restoring the old file-node ⌥e behavior for the chip model. The path chip the
// caret sits on (or just after) wins; otherwise the node's first path chip.
// Returns nil when the node has no path chip.
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

