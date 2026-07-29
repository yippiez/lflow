package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/database"
)

func pasteMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}
}

// TestServiceLinkDisplay: a link chip pointing at a service carries that
// service's mark beside its name — same arrow, everything else untouched; an
// ordinary URL link is unchanged.
func TestServiceLinkDisplay(t *testing.T) {
	sheet := database.Chip{Kind: chipKindLink, Value: "https://docs.google.com/spreadsheets/d/1abc/edit", Label: "Q3 budget"}
	if got := chipDisplay(sheet); got != "→▦ Q3 budget" {
		t.Errorf("display = %q, want →▦ Q3 budget", got)
	}
	// the machine-readable form is unchanged — a service chip IS a link chip
	if got := chipExpand(sheet); got != "[Q3 budget](https://docs.google.com/spreadsheets/d/1abc/edit)" {
		t.Errorf("expand = %q", got)
	}
	plain := database.Chip{Kind: chipKindLink, Value: "https://example.com", Label: "X"}
	if got := chipDisplay(plain); got != "→X" {
		t.Errorf("plain link display = %q, want →X", got)
	}
	node := database.Chip{Kind: chipKindLink, Value: nodeLinkURI("abc"), Label: "Elsewhere"}
	if _, ok := linkService(node); ok {
		t.Error("a node link must never resolve to a service")
	}
}

// TestServiceLinkKeepsLinkStyling: a service link stays a link — same underline,
// same OSC 8 target — and only trades the neutral link color for its service's
// muted hue. An unrecognized link is untouched.
func TestServiceLinkKeepsLinkStyling(t *testing.T) {
	chips := map[string]database.Chip{
		"s": {ID: "s", Kind: chipKindLink, Value: "https://docs.google.com/spreadsheets/d/1abc", Label: "Budget"},
		"p": {ID: "p", Kind: chipKindLink, Value: "https://example.com", Label: "Plain"},
	}
	it := &item{typ: database.TypeBullets}

	sheets := renderBody(it, chipAnchor("s"), -1, false, chips, false)
	plain := renderBody(it, chipAnchor("p"), -1, false, chips, false)
	if !strings.Contains(sheets, serviceColors["sheets"]+cUnderline) {
		t.Errorf("sheets link is not in its muted green: %q", sheets)
	}
	if !strings.Contains(plain, linkChipColorCode()) {
		t.Errorf("an unrecognized link must keep the ordinary link color: %q", plain)
	}
	for key, col := range serviceColors {
		if strings.Contains(plain, col) {
			t.Errorf("a plain link took the %s hue: %q", key, plain)
		}
	}
	// no filled cell: the whole chip is one colored run (stripSGR would not help
	// here — it removes SGR but not the OSC 8 hyperlink wrapper)
	if !strings.Contains(sheets, "→▦ Budget") {
		t.Errorf("sheets chip text missing: %q", sheets)
	}
	if !strings.Contains(sheets, "\x1b]8;;https://docs.google.com/spreadsheets/d/1abc") {
		t.Errorf("service link lost its OSC 8 target: %q", sheets)
	}
}

// TestPasteServiceLinkChips: pasting a Google link lands the dressed chip with
// the service as its default title; pasting any other URL stays plain text.
func TestPasteServiceLinkChips(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "here", Name: "see "})
	cursorOn(m, "here")
	m.caret = len([]rune("see "))

	m.feed(pasteMsg("https://docs.google.com/spreadsheets/d/1abc/edit"))

	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("pasted sheets URL did not become a link chip")
	}
	if c.Label != "Sheets" || c.Value != "https://docs.google.com/spreadsheets/d/1abc/edit" {
		t.Errorf("chip = %+v", c)
	}
	here := m.tree.byUUID["here"]
	if got := displayAnchors(here.name, m.chips); got != "see →▦ Sheets" {
		t.Errorf("rendered = %q, want \"see →▦ Sheets\"", got)
	}
	// persisted at once, like every chip
	if got, err := database.GetChip(m.db, c.ID); err != nil || got.Label != "Sheets" {
		t.Errorf("chip not persisted: %+v err=%v", got, err)
	}

	// a non-service URL keeps the old behavior: plain pasted text
	m2, _ := dbModel(t, database.Node{UUID: "here", Name: ""})
	cursorOn(m2, "here")
	m2.caret = 0
	m2.feed(pasteMsg("https://example.com/x"))
	if _, ok := linkChipOf(m2); ok {
		t.Error("a plain URL must paste as text, not a chip")
	}
	if got := m2.tree.byUUID["here"].name; got != "https://example.com/x" {
		t.Errorf("plain paste = %q", got)
	}
}

// TestPasteServiceLinkSchemeless: a URL pasted without a scheme is recognized
// and stored canonicalized, so the browser can open it as-is.
func TestPasteServiceLinkSchemeless(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "here", Name: ""})
	cursorOn(m, "here")
	m.caret = 0
	m.feed(pasteMsg("drive.google.com/drive/folders/1abc"))

	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("pasted drive URL did not become a link chip")
	}
	if c.Value != "https://drive.google.com/drive/folders/1abc" {
		t.Errorf("target = %q, want the https:// form", c.Value)
	}
	if c.Label != "Drive" {
		t.Errorf("title = %q, want Drive", c.Label)
	}
}

// TestServiceLinkRenamedLikeAHyperlink: ⌥e edits the title, and the chip keeps
// its service dress — the target, not the title, decides the look.
func TestServiceLinkRenamedLikeAHyperlink(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "here", Name: ""})
	cursorOn(m, "here")
	m.caret = 0
	m.insertLinkChip("https://docs.google.com/document/d/1abc/edit", "Docs")

	m.feed(altRune('e'))
	if m.mode != modeLinkEdit {
		t.Fatalf("⌥e did not open the link editor: mode=%v", m.mode)
	}
	for range []rune(m.linkEditName) {
		m.feed(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Design spec")})
	m.feed(tea.KeyMsg{Type: tea.KeyEnter})

	c, _ := linkChipOf(m)
	if c.Label != "Design spec" {
		t.Fatalf("title = %q, want Design spec", c.Label)
	}
	if got := chipDisplay(c); got != "→▤ Design spec" {
		t.Errorf("renamed display = %q, want →▤ Design spec", got)
	}
}

// TestLinkChipRunsOpen: ⌥r on a link chip acts like ⌥g — a node link jumps
// (the browser path is not exercised in a test).
func TestLinkChipRunsOpen(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "here", Name: "from", Rank: 0},
		database.Node{UUID: "dest", Name: "Destination", Rank: 1},
	)
	cursorOn(m, "here")
	m.caret = 0
	m.insertLinkChip(nodeLinkURI("dest"), "Destination")

	m.feed(altRune('r'))
	if m.tree.root.uuid != "dest" {
		t.Fatalf("⌥r on a link chip did not follow it: root = %q", m.tree.root.uuid)
	}
}

// TestServiceLinkCLIDisplay guards the CLI-side copy of the display (lflow list
// / grep), which must show the same glyph and title as the editor.
func TestServiceLinkCLIDisplay(t *testing.T) {
	c := database.Chip{Kind: chipKindLink, Value: "https://drive.google.com/drive/folders/1abc", Label: "Team drive"}
	if got := database.ChipDisplay(c); got != "▲ Team drive" {
		t.Errorf("CLI display = %q, want ▲ Team drive", got)
	}
}
