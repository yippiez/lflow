package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The editor-side helpers shared by the language statement nodes: span
// coloring (keyword yellow, comment dim), the subtree → SrcNode projection, and
// the alt+r export action. Each language file (python.go, rust.go) registers
// its own statement node through these.

func isWordRune(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// keywordSpanColor tints keyword runes yellow and comments dim — shared by the
// python and rust statement nodes.
func keywordSpanColor(keywords map[string]bool, commentMark string) func(runes []rune) map[int]string {
	return func(runes []rune) map[int]string {
		colors := map[int]string{}
		if i := strings.Index(string(runes), commentMark); i >= 0 {
			pre := []rune(string(runes)[:i])
			for j := len(pre); j < len(runes); j++ {
				colors[j] = cDim
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
					colors[k] = cYellow
				}
			}
			i = j
		}
		return colors
	}
}

// refToSrc projects an editor subtree onto the neutral SrcNode form so alt+r
// exports through the same renderer as the file codec.
func refToSrc(it *item) *SrcNode {
	s := &SrcNode{Type: it.typ, Text: it.name}
	for _, c := range it.children {
		s.Kids = append(s.Kids, refToSrc(c))
	}
	return s
}

// runCodeExport is alt+r for a language statement node: export the subtree as
// real source to the run band.
func runCodeExport(spec LangSpec) func(m *Model, it *item) tea.Cmd {
	return func(m *Model, it *item) tea.Cmd {
		src, err := RenderCode([]*SrcNode{refToSrc(it)}, spec)
		if err != nil {
			m.flash = "export: " + err.Error()
			return nil
		}
		m.runOutLines(it.uuid, strings.Split(strings.TrimRight(src, "\n"), "\n"))
		m.flash = spec.Name + " → output"
		return nil
	}
}
