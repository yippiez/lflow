package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

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

// Filesystem-path completion for the @path chip (see chip.go). Completion is
// prefix-based on the basename so Tab can advance to the longest common prefix;
// an ambiguous prefix cycles, a file commits to a chip, a directory keeps drilling.

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

// fileCandidates returns the path completions for the partial path `text`,
// preserving the display prefix the user typed (~/, relative, or absolute) and
// only completing the final path component. Directories get a trailing slash.
func fileCandidates(text string) []string {
	dir, base := filepath.Split(text) // dir keeps its trailing slash; "" for a bare name

	fsDir := expandHome(dir)
	if fsDir == "" {
		fsDir = "."
	}
	entries, err := os.ReadDir(fsDir)
	if err != nil {
		return nil
	}

	lower := strings.ToLower(base)
	var out []string
	for _, e := range entries {
		name := e.Name()
		// hidden files only when the user has started typing a dot
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), lower) {
			continue
		}
		disp := dir + name
		if e.IsDir() {
			disp += string(filepath.Separator)
		}
		out = append(out, disp)
	}
	sort.Strings(out)
	return out
}

// commonPrefix returns the longest common string prefix of the candidates.
func commonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
			if p == "" {
				return ""
			}
		}
	}
	return p
}

// fileCompList renders the match basenames for the status bar, marking the
// selected one with ›‹.
func fileCompList(cands []string, sel int) string {
	parts := make([]string, len(cands))
	for i, c := range cands {
		b := filepath.Base(strings.TrimRight(c, string(filepath.Separator)))
		if strings.HasSuffix(c, string(filepath.Separator)) {
			b += "/"
		}
		if i == sel {
			b = "›" + b + "‹"
		}
		parts[i] = b
	}
	return strings.Join(parts, "  ")
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

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// completeChipUnderCaret performs one Tab of path completion on the "@token"
// under the caret (in any node). A file match commits to a path chip (the typed
// text becomes an anchor); a directory or an ambiguous prefix stays plain text so
// Tab can keep drilling/cycling. Returns false when there's no @token to complete.
func (m *Model) completeChipUnderCaret(cur *item) bool {
	runes := []rune(cur.name)
	start, end, ok := chipTokenAt(runes, m.caret)
	if !ok {
		return false
	}
	partial := string(runes[start+1 : end]) // text after "#"
	d := m.nodeStore(cur.uuid)

	// sitting on a previously-offered candidate → cycle to the next one
	if cands, ok := d["chipCands"].([]string); ok && len(cands) > 1 {
		for i, c := range cands {
			if c == partial {
				ni := (i + 1) % len(cands)
				m.setChipPartial(cur, start, end, cands[ni], cands, ni)
				return true
			}
		}
	}

	// a completed directory path (trailing slash) commits as a folder chip — Tab
	// once lands on "dir/", Tab again selects the folder; type a subpath to drill
	if strings.HasSuffix(partial, string(filepath.Separator)) {
		if abs := absolutizePath(partial); isDir(abs) {
			m.commitPathChip(cur, start, end, abs)
			delete(d, "chipCands")
			return true
		}
	}

	matches := fileCandidates(partial)
	if len(matches) == 0 {
		if abs := absolutizePath(partial); partial != "" && pathExists(abs) {
			m.commitPathChip(cur, start, end, abs) // a fully-typed path → commit it
		} else {
			m.flash = "no path match"
			delete(d, "chipCands")
		}
		return true
	}
	if len(matches) == 1 {
		if strings.HasSuffix(matches[0], string(filepath.Separator)) {
			m.setChipPartial(cur, start, end, matches[0], nil, 0) // directory: keep drilling
		} else {
			m.commitPathChip(cur, start, end, absolutizePath(matches[0])) // file: commit chip
		}
		delete(d, "chipCands")
		return true
	}
	if lcp := commonPrefix(matches); len([]rune(lcp)) > len([]rune(partial)) {
		m.setChipPartial(cur, start, end, lcp, matches, -1) // advance to shared prefix
	} else {
		m.setChipPartial(cur, start, end, matches[0], matches, 0) // begin cycling
	}
	return true
}

// setChipPartial replaces the @token with "@"+partial — still plain, editable
// text (not yet a committed chip).
func (m *Model) setChipPartial(cur *item, start, end int, partial string, cands []string, sel int) {
	runes := []rune(cur.name)
	tok := []rune("#" + partial)
	cur.name = string(runes[:start]) + string(tok) + string(runes[end:])
	m.caret = start + len(tok)
	m.unsaved = true
	d := m.nodeStore(cur.uuid)
	if len(cands) > 1 {
		d["chipCands"] = cands
		d["chipIdx"] = sel
		m.flash = fileCompList(cands, sel)
	} else {
		delete(d, "chipCands")
		m.flash = ""
	}
}

// commitPathChip replaces the @token [start,end) with a path-chip anchor whose
// record holds the absolute path.
func (m *Model) commitPathChip(cur *item, start, end int, absPath string) {
	anchor := m.createChip(chipKindPath, absPath)
	if anchor == "" {
		return
	}
	runes := []rune(cur.name)
	cur.name = string(runes[:start]) + anchor + string(runes[end:])
	m.caret = start + len([]rune(anchor))
	m.unsaved = true
	m.flash = ""
	delete(m.nodeStore(cur.uuid), "chipCands")
}
