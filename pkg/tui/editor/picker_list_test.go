package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// longSource is a picker whose header and rows are both far wider than the pane
// — the shape a Zotero title or a coding-session prompt actually has.
type longSource struct{ n int }

func (s longSource) items(*Model, string) []pickerItem {
	out := make([]pickerItem, 0, s.n)
	for i := 0; i < s.n; i++ {
		out = append(out, pickerItem{
			label: "an option whose label runs well past the width of any pane it is drawn in",
			desc:  "and a description that keeps going for a good while after that one too",
		})
	}
	return out
}

func (longSource) header(*Model, *listPicker) string {
	return " " + cDim + "cite: " + cReset + "a header itself much wider than the pane it has to fit inside"
}
func (longSource) initialSel(*Model) int                                { return 0 }
func (longSource) onSelect(m *Model, _ pickerItem) (tea.Model, tea.Cmd) { return m, nil }

// TestPickerRowsTruncateAndKeepTheirShape: one option is one line. A row that
// wrapped pushed the ones below it down, so eight options could occupy fifteen
// lines and the drawn height no longer matched what counts() promised View.
func TestPickerRowsTruncateAndKeepTheirShape(t *testing.T) {
	m := &Model{}
	src := longSource{n: 20}
	p := &listPicker{}
	p.open(m, src, true)

	const maxLine = 40
	lines := p.render(m, src, maxLine)

	// header + exactly one line per visible option, never more than the window
	want := 1 + pickerMaxRows
	if len(lines) != want {
		t.Fatalf("render drew %d lines, want %d (a header plus one per option)", len(lines), want)
	}
	for i, l := range lines {
		if w := visibleWidth(l); w > maxLine {
			t.Errorf("line %d is %d columns wide, want <= %d: %q", i, w, maxLine, stripSGR(l))
		}
		if strings.ContainsAny(l, "\n\r") {
			t.Errorf("line %d carries a newline — a row must be one line", i)
		}
	}

	// what render draws and what counts promises View must agree
	items, header := p.counts(m, src)
	win := items
	if win > pickerMaxRows {
		win = pickerMaxRows
	}
	if got := win + header; got != len(lines) {
		t.Errorf("counts promised %d lines, render drew %d", got, len(lines))
	}

	// a truncated row says so
	if !strings.Contains(stripSGR(lines[1]), "…") {
		t.Errorf("a clipped row has no ellipsis: %q", stripSGR(lines[1]))
	}
}

// TestPickerShortRowsAreLeftAlone: truncation must not touch a row that fits.
func TestPickerShortRowsAreLeftAlone(t *testing.T) {
	m := &Model{}
	src := staticSource{}
	p := &listPicker{}
	p.open(m, src, false)

	lines := p.render(m, src, 80)
	if len(lines) != 2 {
		t.Fatalf("render drew %d lines, want one per option", len(lines))
	}
	for _, l := range lines {
		if strings.Contains(l, "…") {
			t.Errorf("a row that fits was clipped anyway: %q", stripSGR(l))
		}
	}
}

// staticSource is two short rows and no header.
type staticSource struct{}

func (staticSource) items(*Model, string) []pickerItem {
	return []pickerItem{{label: "Bold"}, {label: "Italic"}}
}
func (staticSource) header(*Model, *listPicker) string                    { return "" }
func (staticSource) initialSel(*Model) int                                { return 0 }
func (staticSource) onSelect(m *Model, _ pickerItem) (tea.Model, tea.Cmd) { return m, nil }
