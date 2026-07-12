package editor

import (
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// The codereview node: a critique launcher. The node's text holds a commit
// range ("base..head"); alt+e opens the inline commit picker — `git log` of
// the editor's cwd, pick the BEGINNING commit then the END commit — and the
// pick immediately opens the critique TUI on that range (suspending the
// inline UI, the way a path chip opens $EDITOR). alt+r re-opens critique on
// the stored range; an empty range reviews the working tree (critique's own
// default). Dep-gated on the critique binary (NodeCLIDeps).

// crCommit is one pickable commit.
type crCommit struct {
	sha     string
	subject string
}

// crState is the per-node ephemeral picker state (nodeStore, key "codereview").
type crState struct {
	commits []crCommit
	sel     int
	base    string // the picked beginning sha; "" = still picking it
	err     string
}

func crStateOf(m *Model, it *item) *crState {
	d := m.nodeStore(it.uuid)
	st, _ := d["codereview"].(*crState)
	if st == nil {
		st = &crState{}
		d["codereview"] = st
	}
	return st
}

// crRange parses the node text into critique args: "base..head", "base head"
// or a single ref; empty text means the working tree.
func crRange(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if i := strings.Index(text, ".."); i >= 0 {
		base, head := strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+2:])
		out := []string{}
		if base != "" {
			out = append(out, base)
		}
		if head != "" {
			out = append(out, head)
		}
		return out
	}
	f := strings.Fields(text)
	if len(f) > 2 {
		f = f[:2]
	}
	return f
}

// runCodeReview opens the critique TUI on the node's range (alt+r).
func runCodeReview(m *Model, it *item) tea.Cmd {
	args := crRange(expandAnchors(it.name, m.chips))
	c := exec.Command("critique", args...)
	return tea.ExecProcess(c, func(error) tea.Msg { return nil })
}

// codeReviewView is the alt+e commit picker.
type codeReviewView struct{}

// crShowRows is the picker window height.
const crShowRows = 14

func (codeReviewView) Enter(m *Model, it *item) bool {
	st := crStateOf(m, it)
	st.sel, st.base, st.err = 0, "", ""
	out, err := exec.Command("git", "log", "--oneline", "--no-decorate", "-60").Output()
	if err != nil {
		st.commits = nil
		st.err = "git log failed · is the editor cwd a repo?"
		return true
	}
	st.commits = st.commits[:0]
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		sha, subject, _ := strings.Cut(l, " ")
		if sha != "" {
			st.commits = append(st.commits, crCommit{sha: sha, subject: subject})
		}
	}
	return true
}

func (codeReviewView) Leave(m *Model, it *item) {
	st := crStateOf(m, it)
	st.commits, st.base, st.err = nil, "", ""
}

func (codeReviewView) Lines(m *Model, it *item, width int) int {
	st := crStateOf(m, it)
	if st.err != "" {
		return 2
	}
	return 1 + min(len(st.commits), crShowRows)
}

func (v codeReviewView) Key(m *Model, it *item, k tea.KeyMsg) (tea.Cmd, bool) {
	st := crStateOf(m, it)
	switch k.String() {
	case "up", "k":
		if st.sel > 0 {
			st.sel--
		}
		return nil, true
	case "down", "j":
		if st.sel < len(st.commits)-1 {
			st.sel++
		}
		return nil, true
	case "enter", " ", "space":
		if st.sel < 0 || st.sel >= len(st.commits) {
			return nil, true
		}
		sha := st.commits[st.sel].sha
		if st.base == "" {
			st.base = sha // the beginning commit; now pick the end
			return nil, true
		}
		// beginning + end picked: store the range and open critique
		it.name = st.base + ".." + sha
		m.unsaved = true
		if bin, missing := m.typeDepMissing(database.TypeCodeReview); missing {
			m.flash = "Missing dependency: " + bin
			return nil, true
		}
		return runCodeReview(m, it), true
	}
	return nil, false // esc & friends → central (Leave clears)
}

func (v codeReviewView) Bands(m *Model, it *item, rail string, width, scroll, winH int, focused bool) []string {
	st := crStateOf(m, it)
	var content []string
	if st.err != "" {
		content = append(content,
			clip(rail+cReset+cDim+"  codereview"+cReset, width),
			clip(rail+cReset+"  "+cRed+st.err+cReset, width))
		return windowBands(content, scroll, winH)
	}
	head := "  pick the beginning commit · enter"
	if st.base != "" {
		head = "  beginning " + st.base + " · pick the end commit · enter opens critique"
	}
	content = append(content, clip(rail+cReset+cDim+head+cReset, width))

	// keep the selection visible inside the fixed window
	top := 0
	if st.sel >= crShowRows {
		top = st.sel - crShowRows + 1
	}
	for i := top; i < len(st.commits) && i < top+crShowRows; i++ {
		c := st.commits[i]
		marker, style := "  ", cDim
		if focused && i == st.sel {
			marker, style = cAccent+"▸ "+cReset, cFG
		}
		mark := ""
		if c.sha == st.base {
			mark = cGreen + " · beginning" + cReset
		}
		content = append(content, clip(fmt.Sprintf("%s%s  %s%s%s %s%s%s%s",
			rail+cReset, marker, cYellow, c.sha, cReset, style, c.subject, cReset, mark), width))
	}
	return windowBands(content, scroll, winH)
}

// windowBands clamps a band list to the caller's [scroll, scroll+winH) window.
func windowBands(content []string, scroll, winH int) []string {
	if scroll > len(content) {
		scroll = len(content)
	}
	end := scroll + winH
	if end > len(content) {
		end = len(content)
	}
	return content[scroll:end]
}
