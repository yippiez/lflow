// Package nodes hosts the pluggable node types — one Go file per node — each
// registered into the editor through its node plugin API (editor.NodePlugin).
// The editor owns the generic machinery (registry, pickers, bands, dep
// gating, agent transport); a file here reads standalone against
// editor.NodeHost / editor.NodeRef.
package nodes

import (
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The codereview node: a critique launcher. The node's text holds a commit
// range ("base..head"); alt+e opens the inline commit picker — `git log` of
// the editor's cwd, pick the BEGINNING commit then the END commit — and the
// pick immediately opens the critique TUI on that range. alt+r re-opens
// critique on the stored range; an empty range reviews the working tree.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeCodeReview, Label: "Code Review", Sign: "⌁ ",
		InlineEditable: true,
		CLIDeps:        []string{"critique", "git"},
		Run:            runCodeReview,
		View:           codeReviewView{},
		ToContext: func(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
			return "codereview", "", ""
		},
	})
}

// crCommit is one pickable commit.
type crCommit struct {
	sha     string
	subject string
}

// crState is the per-node ephemeral picker state (NodeStore, key "codereview").
type crState struct {
	commits []crCommit
	sel     int
	base    string // the picked beginning sha; "" = still picking it
	err     string
}

func crStateOf(h editor.NodeHost, uuid string) *crState {
	d := h.NodeStore(uuid)
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
func runCodeReview(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	c := exec.Command("critique", crRange(n.Text())...)
	return tea.ExecProcess(c, func(error) tea.Msg { return nil })
}

// codeReviewView is the alt+e commit picker.
type codeReviewView struct{}

// crShowRows is the picker window height.
const crShowRows = 14

func (codeReviewView) Enter(h editor.NodeHost, n editor.NodeRef) bool {
	st := crStateOf(h, n.UUID())
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

func (codeReviewView) Leave(h editor.NodeHost, n editor.NodeRef) {
	st := crStateOf(h, n.UUID())
	st.commits, st.base, st.err = nil, "", ""
}

func (codeReviewView) Lines(h editor.NodeHost, n editor.NodeRef, width int) int {
	st := crStateOf(h, n.UUID())
	if st.err != "" {
		return 2
	}
	return 1 + min(len(st.commits), crShowRows)
}

func (v codeReviewView) Key(h editor.NodeHost, n editor.NodeRef, k tea.KeyMsg) (tea.Cmd, bool) {
	st := crStateOf(h, n.UUID())
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
		n.SetText(st.base + ".." + sha)
		if !h.NodeDepOK("critique") {
			h.NodeFlash("Missing dependency: critique")
			return nil, true
		}
		return runCodeReview(h, n), true
	}
	return nil, false // esc & friends → central (Leave clears)
}

func (v codeReviewView) Bands(h editor.NodeHost, n editor.NodeRef, rail string, width, scroll, winH int, focused bool) []string {
	st := crStateOf(h, n.UUID())
	th := editor.NodeTheme()
	var content []string
	if st.err != "" {
		content = append(content,
			editor.NodeClip(rail+th.Reset+th.Dim+"  codereview"+th.Reset, width),
			editor.NodeClip(rail+th.Reset+"  "+th.Red+st.err+th.Reset, width))
		return editor.NodeWindowBands(content, scroll, winH)
	}
	head := "  pick the beginning commit · enter"
	if st.base != "" {
		head = "  beginning " + st.base + " · pick the end commit · enter opens critique"
	}
	content = append(content, editor.NodeClip(rail+th.Reset+th.Dim+head+th.Reset, width))

	// keep the selection visible inside the fixed window
	top := 0
	if st.sel >= crShowRows {
		top = st.sel - crShowRows + 1
	}
	for i := top; i < len(st.commits) && i < top+crShowRows; i++ {
		c := st.commits[i]
		marker, style := "  ", th.Dim
		if focused && i == st.sel {
			marker, style = th.Accent+"▸ "+th.Reset, th.FG
		}
		mark := ""
		if c.sha == st.base {
			mark = th.Green + " · beginning" + th.Reset
		}
		content = append(content, editor.NodeClip(fmt.Sprintf("%s%s  %s%s%s %s%s%s%s",
			rail+th.Reset, marker, th.Yellow, c.sha, th.Reset, style, c.subject, th.Reset, mark), width))
	}
	return editor.NodeWindowBands(content, scroll, winH)
}
