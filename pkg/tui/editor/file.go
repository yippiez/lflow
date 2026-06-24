package editor

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// File node: the node's name is a filesystem path. Tab completes it shell-style,
// Enter normalizes it to an absolute path (see editor.go), and ⌥e opens it in
// $EDITOR. Completion is prefix-based on the basename so Tab can advance to the
// longest common prefix; repeated Tab on an ambiguous path cycles the matches.

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

// completeFilePath performs one Tab of shell-style path completion on a file
// node: it fills the longest common prefix and lists the matches, then cycles
// through them on repeated Tab. It edits it.name and moves the caret to the end.
func (m *Model) completeFilePath(it *item) {
	text := it.name
	d := m.nodeStore(it.uuid)

	// sitting on a previously-offered candidate → cycle to the next one
	if cands, ok := d["fileCompCands"].([]string); ok && len(cands) > 1 {
		for i, c := range cands {
			if c == text {
				ni := (i + 1) % len(cands)
				m.setFileComplete(it, cands[ni], cands, ni)
				return
			}
		}
	}

	matches := fileCandidates(text)
	switch len(matches) {
	case 0:
		m.flash = "no path match"
		delete(d, "fileCompCands")
	case 1:
		m.setFileComplete(it, matches[0], nil, 0)
		delete(d, "fileCompCands")
	default:
		if lcp := commonPrefix(matches); len([]rune(lcp)) > len([]rune(text)) {
			// advance to the shared prefix and list; next Tab starts cycling
			m.setFileComplete(it, lcp, matches, -1)
		} else {
			// already at the shared prefix — begin cycling now
			m.setFileComplete(it, matches[0], matches, 0)
		}
	}
}

// setFileComplete writes a completion result: the new name, caret at the end,
// remembered candidates for cycling, and a flash listing the matches.
func (m *Model) setFileComplete(it *item, name string, cands []string, sel int) {
	it.name = name
	m.caret = len([]rune(name))
	m.unsaved = true
	d := m.nodeStore(it.uuid)
	if len(cands) > 1 {
		d["fileCompCands"] = cands
		d["fileCompIdx"] = sel
		m.flash = fileCompList(cands, sel)
	} else {
		delete(d, "fileCompCands")
		m.flash = ""
	}
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

// openFileInEditor opens the file node's path in $EDITOR (falling back to nvim),
// suspending the inline UI while the editor runs and resuming after it exits.
func openFileInEditor(m *Model, it *item) tea.Cmd {
	path := normalizeFilePath(it.name)
	if path == "" {
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
