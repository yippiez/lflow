package editor

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

type shortcutSection struct {
	name string
	rows [][2]string
}

// shortcutSections is the one user-facing keyboard reference. Keep aliases on
// the same row so the page stays scannable while still naming every chord the
// outline accepts.
var shortcutSections = []shortcutSection{
	{"Move and select", [][2]string{
		{"↑ / ↓", "move by visual line or node"},
		{"← / →", "move the text caret"},
		{"home / end", "start / end of node text"},
		{"ctrl+← / ctrl+→", "move by word"},
		{"pgup / pgdown", "scroll half a page"},
		{"shift+↑ / shift+↓", "select nodes"},
		{"shift+← / shift+→", "select letters"},
		{"ctrl+shift+← / →", "select words (alt+shift also works)"},
		{"alt+↑ / alt+↓", "fold / unfold (ctrl also works)"},
		{"alt+← / alt+→", "zoom out / into a node"},
	}},
	{"Edit the outline", [][2]string{
		{"enter", "split node or make a sibling"},
		{"tab / shift+tab", "indent / outdent selection"},
		{"alt+shift+↑ / ↓", "move selection (ctrl+shift also works)"},
		{"backspace", "delete text or merge at the start"},
		{"ctrl+w", "delete previous word"},
		{"ctrl+d / alt+d", "delete selected nodes or subtree"},
		{"ctrl+t", "move node to Temporary Domain"},
		{"y / x", "copy / cut the active selection"},
		{"alt+y / alt+x", "copy / cut selection or cursor subtree"},
		{"ctrl+z / alt+z", "undo"},
		{"ctrl+y / ctrl+shift+z", "redo"},
		{"ctrl+s", "save"},
	}},
	{"Nodes and commands", [][2]string{
		{"/", "open the command palette"},
		{"alt+a / alt+shift+p", "open palette without typing slash"},
		{"alt+t", "set node type"},
		{"alt+c", "style node or selected text"},
		{"alt+enter", "complete / reopen node"},
		{"alt+g", "goto a node (follows links/Zotero)"},
		{"alt+r", "run node or chip (never automatic)"},
		{"alt+e", "expand/view; edit link or tag color"},
		{"alt+o", "open in host app or alternate Zotero target"},
		{"alt+k", "stop a run or clear its output"},
		{"alt+s", "label visible row actions"},
		{"alt+v", "review this node's suggestions, else the next"},
	}},
	{"Chips and sessions", [][2]string{
		{"[[", "insert a node or URL link"},
		{"((", "insert a mirror of another node"},
		{"@@", "insert a Zotero citation"},
		{"$$", "insert a command chip"},
		{"#", "pick or create a tag"},
		{":", "pick an icon; query command in query nodes"},
		{"alt+i", "edit command chip"},
		{"alt+e on agent", "edit session name and color"},
		{"alt+c on agent", "recolor agent session chip"},
		{"alt+o on agent", "copy the session's open command"},
	}},
	{"Mouse", [][2]string{
		{"click a ○", "zoom into that node"},
		{"click a #tag", "goto, already searching that tag"},
		{"click a breadcrumb", "walk the view back to that node"},
		{"click a row", "put the cursor on it"},
		{"wheel", "scroll the outline or the temp panel"},
		{"/settings · Mouse", "click, wheel-only, or hand the mouse back"},
	}},
	{"Leave", [][2]string{
		{"esc", "close a page, picker, selection or focused view"},
		{"esc esc", "save and quit from the outline"},
		{"ctrl+c / ctrl+q", "save and quit"},
	}},
}

func shortcutLines(maxLine int) []string {
	var lines []string
	for _, section := range shortcutSections {
		lines = append(lines, cYellow+cBold+section.name+cReset)
		for _, row := range section.rows {
			key := fmt.Sprintf("  %-23s", row[0])
			lines = append(lines, clip(cCyan+key+cReset+cDim+row[1]+cReset, maxLine))
		}
		lines = append(lines, "")
	}
	return lines
}

// viewShortcuts owns the main page while open; only its footer stays outside
// the scrollable reference.
func (m *Model) viewShortcuts(maxLine int) []string {
	all := shortcutLines(maxLine)
	budget := m.height - 1
	if budget < 1 {
		budget = 23
	}
	maxStart := len(all) - budget
	if maxStart < 0 {
		maxStart = 0
	}
	if m.focusScroll > maxStart {
		m.focusScroll = maxStart
	}
	if m.focusScroll < 0 {
		m.focusScroll = 0
	}
	end := m.focusScroll + budget
	if end > len(all) {
		end = len(all)
	}
	lines := append([]string(nil), all[m.focusScroll:end]...)
	for len(lines) < budget {
		lines = append(lines, "")
	}
	foot := cDim + "↑↓ scroll · pgup/pgdown page · home/end · esc close" + cReset
	lines = append(lines, clip(foot, maxLine))
	m.pageRows = budget
	return lines
}

func (m *Model) handleShortcutsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	all := shortcutLines(max(20, m.width-1))
	page := max(1, m.height-2)
	maxStart := max(0, len(all)-page)
	switch k.String() {
	case "esc", "q", "enter":
		m.mode = modeOutline
		m.focusScroll = 0
	case "up", "k":
		m.focusScroll--
	case "down", "j":
		m.focusScroll++
	case "pgup":
		m.focusScroll -= page
	case "pgdown", "space":
		m.focusScroll += page
	case "home", "g":
		m.focusScroll = 0
	case "end", "G":
		m.focusScroll = maxStart
	}
	m.focusScroll = max(0, min(m.focusScroll, maxStart))
	return m, nil
}
