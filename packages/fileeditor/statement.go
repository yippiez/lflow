package fileeditor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/packages/editor"
)

// The editor-side plugin helpers shared by the language statement nodes: span
// coloring (keyword yellow, comment dim), the subtree → SrcNode projection, and
// the alt+r export action. Each language file (python.go, rust.go) registers
// its own statement node plugin through these.

func isWordRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// keywordSpanColor tints keyword runes yellow and comments dim — shared by the
// python and rust statement nodes.
func keywordSpanColor(keywords map[string]bool, commentMark string) func(runes []rune) map[int]string {
	return func(runes []rune) map[int]string {
		th := editor.NodeTheme()
		colors := map[int]string{}
		if i := strings.Index(string(runes), commentMark); i >= 0 {
			pre := []rune(string(runes)[:i])
			for j := len(pre); j < len(runes); j++ {
				colors[j] = th.Dim
			}
		}
		for i := 0; i < len(runes); {
			if !isWordRune(runes[i]) {
				i++
				continue
			}
			j := i
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
			if keywords[string(runes[i:j])] && colors[i] == "" {
				for k := i; k < j; k++ {
					colors[k] = th.Yellow
				}
			}
			i = j
		}
		return colors
	}
}

// refToSrc projects an editor subtree onto the neutral SrcNode form so alt+r
// exports through the same renderer as the file codec.
func refToSrc(n editor.NodeRef) *SrcNode {
	s := &SrcNode{Type: n.Type(), Text: n.Text()}
	for _, c := range n.Children() {
		s.Kids = append(s.Kids, refToSrc(c))
	}
	return s
}

// runCodeExport is alt+r for a language statement node: export the subtree as
// real source to the run band.
func runCodeExport(spec LangSpec) func(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
	return func(h editor.NodeHost, n editor.NodeRef) tea.Cmd {
		src, err := RenderCode([]*SrcNode{refToSrc(n)}, spec)
		if err != nil {
			h.NodeFlash("export: " + err.Error())
			return nil
		}
		editor.NodeRunOut(h, n.UUID(), strings.Split(strings.TrimRight(src, "\n"), "\n"))
		h.NodeFlash(spec.Name + " → output")
		return nil
	}
}
