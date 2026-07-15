package nodes

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/editor"
)

// The mlop node: ONE ML primitive inside an mlmodel subtree. The node text is
// the primitive expression — "linear 2048", "attend heads=8 kv=encoder",
// "repeat 6" — and the tree around it is the composition: siblings pipeline
// in order, a plain op's children continue after it, a combinator's children
// are its block. The primitive vocabulary, the parser and the compiler live
// with the model root (mlmodel.go); this file is only the node surface.
// Enter continues the pipeline with another mlop (ContinueOnEnter), so a
// model types like a list. alt+r flashes THIS stage's compiled output shape
// and weight count — ephemeral, like every run output.

func init() {
	editor.RegisterNodePlugin(editor.NodePlugin{
		Key: database.TypeMLOp, Label: "ML Op",
		InlineEditable:  true,
		ContinueOnEnter: true,
		Glyph:           func() (string, string) { return "∘", editor.NodeTheme().Dim },
		Render:          moRender,
		Run:             runMLOp,
		ToContext:       moToContext,
	})
}

// moRender colors the primitive keyword cyan (red when it is not in the
// vocabulary — a live typo check), arguments plain.
func moRender(h editor.NodeHost, n editor.NodeRef) string {
	th := editor.NodeTheme()
	text := n.Text()
	f := strings.Fields(text)
	if len(f) == 0 {
		return th.Dim + "(empty op)" + th.Reset
	}
	kindColor := th.Cyan
	if !mlKinds[strings.ToLower(f[0])] {
		kindColor = th.Red
	}
	out := kindColor + f[0] + th.Reset
	if rest := strings.Join(f[1:], " "); rest != "" {
		out += " " + th.FG + rest + th.Reset
	}
	return out
}

// mlRootOf walks up to the enclosing mlmodel root, if any.
func mlRootOf(n editor.NodeRef) (editor.NodeRef, bool) {
	for p, ok := n.Parent(); ok; p, ok = p.Parent() {
		if p.Type() == database.TypeMLModel {
			return p, true
		}
	}
	return nil, false
}

// runMLOp (alt+r) compiles the whole model and flashes this stage's inferred
// output shape and weight count.
func runMLOp(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	root, ok := mlRootOf(n)
	if !ok {
		h.NodeFlash("not inside an ML Model subtree")
		return nil
	}
	r := mlCompile(root)
	for _, s := range r.stages {
		if s.uuid != n.UUID() {
			continue
		}
		if s.err != "" {
			h.NodeFlash(s.text + " · " + s.err)
			return nil
		}
		params := "no weights"
		if s.param > 0 {
			params = mlHuman(s.param) + " params"
		}
		h.NodeFlash(fmt.Sprintf("→ %s · %s", s.out, params))
		return nil
	}
	if r.err != "" {
		h.NodeFlash("model stops earlier: " + r.err)
	} else {
		h.NodeFlash("stage not compiled (comment position?)")
	}
	return nil
}

var moTagOK = regexp.MustCompile(`^[a-z]+$`)

// moToContext gives the primitive its own element in the agent context —
// <repeat args="6"> nests its block, <linear args="2048"/> reads as itself.
func moToContext(h editor.NodeHost, n editor.NodeRef) (string, string, string) {
	op := mlParse(n.Text())
	tag := "op"
	if mlKinds[op.kind] && moTagOK.MatchString(op.kind) {
		tag = op.kind
	}
	f := strings.Fields(n.Text())
	attrs := ""
	if rest := strings.Join(f[min(1, len(f)):], " "); rest != "" {
		attrs = `args="` + strings.ReplaceAll(rest, `"`, "&quot;") + `"`
	}
	return tag, attrs, ""
}
