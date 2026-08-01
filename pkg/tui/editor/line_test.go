package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestLineCharacterAssignAndRender drives the Line node end to end: /type
// lands a fresh Line node in the outline (no forced character — one is assigned
// whenever, via alt+e), typing an unseen name offers to create it, picking it
// persists and renders the bracket prefix, a second Line node picks that same
// character back off the existing list, "no character" sheds it, and alt+c
// recolors it everywhere it speaks.
func TestLineCharacterAssignAndRender(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	defer func() {
		lineCharacters = map[string]string{}
		characterColors = map[string]string{}
	}()

	root := &item{uuid: "root"}
	n1 := &item{uuid: "n1", name: "be right there", typ: database.TypeBullets, parent: root}
	n2 := &item{uuid: "n2", name: "took you long enough", typ: database.TypeLine, parent: root}
	root.children = []*item{n1, n2}
	tr := &tree{db: db, root: root, byUUID: map[string]*item{"root": root, "n1": n1, "n2": n2},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{db: db, tree: tr, viewStack: []*item{root}, width: 100, height: 30,
		chips: map[string]database.Chip{}}
	m.refreshRows()
	m.cursor = 0
	if m.cursorItem() != n1 {
		t.Fatalf("expected the cursor on n1, got %+v", m.cursorItem())
	}

	// /type picking Line on the cursor node must convert the type and land in the
	// outline — a Line is created WITHOUT a character; one is set later.
	m.mode = modeType
	mm, _ := typeSource{}.onSelect(m, pickerItem{value: database.TypeLine})
	m = mm.(*Model)
	if n1.typ != database.TypeLine {
		t.Fatalf("n1.typ = %q, want line", n1.typ)
	}
	if m.mode != modeOutline {
		t.Fatalf("/type -> line must land in the outline (character is optional), mode=%v", m.mode)
	}

	// an unassigned Line prompts for a character, and still renders its body fine
	if !strings.Contains(linePrefix(n1), "character") {
		t.Fatalf("unassigned Line must prompt for a character, prefix = %q", linePrefix(n1))
	}
	if body := renderBody(n1, n1.name, -1, false, m.chips, false); !strings.Contains(body, "right there") {
		t.Fatalf("Line body must still render its dialogue text, got %q", body)
	}

	// typing a name nothing matches offers to create it
	m.characterPickUUID = "n1"
	m.list.query = "alice"
	items := characterSource{}.items(m, "alice")
	if len(items) != 1 || items[0].value != "ALICE" {
		t.Fatalf("typing a new name must offer to create it, items = %+v", items)
	}
	mm, _ = characterSource{}.onSelect(m, items[0])
	m = mm.(*Model)
	if lineCharacters["n1"] != "ALICE" {
		t.Fatalf("lineCharacters[n1] = %q, want ALICE", lineCharacters["n1"])
	}
	if saved, err := database.AllLineCharacters(db); err != nil || saved["n1"] != "ALICE" {
		t.Fatalf("persisted characters = %v (%v)", saved, err)
	}
	if !strings.Contains(linePrefix(n1), "[ALICE]") {
		t.Fatalf("prefix must show the bracketed character, got %q", linePrefix(n1))
	}

	// a second Line node picks ALICE back off the existing list — "no character"
	// (row 0) plus ALICE (row 1), and no new-row offer
	m.openCharacterPicker(n2)
	items = characterSource{}.items(m, "")
	if len(items) != 2 || items[0].value != "" || items[1].value != "ALICE" {
		t.Fatalf("existing character must be offered after the no-character row, items = %+v", items)
	}
	mm, _ = characterSource{}.onSelect(m, items[1])
	m = mm.(*Model)
	if lineCharacters["n2"] != "ALICE" {
		t.Fatalf("lineCharacters[n2] = %q, want ALICE", lineCharacters["n2"])
	}

	// "no character" sheds the assignment — the row falls back to the prompt
	mm, _ = characterSource{}.onSelect(m, items[0])
	m = mm.(*Model)
	if lineCharacters["n2"] != "" {
		t.Fatalf("no-character must clear lineCharacters[n2], got %q", lineCharacters["n2"])
	}
	if saved, err := database.AllLineCharacters(db); err != nil || saved["n2"] != "" {
		t.Fatalf("clearing must persist too, got %v (%v)", saved, err)
	}
	// re-name it so the recolor test below can check both lines together
	setLineCharacter(m, "n2", "ALICE")

	// alt+c on the highlighted row opens the nested recolor picker
	m.mode = modeCharacterPick
	m.list.open(m, characterSource{}, true)
	handled := characterSource{}.onKey(m, &m.list, "alt+c", characterSource{}.items(m, ""))
	if !handled || m.mode != modeCharacterColor || m.characterColorName != "ALICE" {
		t.Fatalf("alt+c must open the recolor picker for ALICE, handled=%v mode=%v name=%q", handled, m.mode, m.characterColorName)
	}
	mm, _ = characterColorSource{}.onSelect(m, pickerItem{value: "cyan"})
	m = mm.(*Model)
	if characterColors["ALICE"] != "cyan" {
		t.Fatalf("characterColors[ALICE] = %q, want cyan", characterColors["ALICE"])
	}
	if saved, err := database.AllCharacterColors(db); err != nil || saved["ALICE"] != "cyan" {
		t.Fatalf("persisted colors = %v (%v)", saved, err)
	}
	if !strings.Contains(characterColorSGR("alice"), styleColorCode["cyan"]) {
		t.Fatal("ALICE must now render in cyan, matched case-insensitively")
	}
	// both lines naming ALICE pick up the new color, not just the one it was set on
	if !strings.Contains(linePrefix(n2), styleColorCode["cyan"]) {
		t.Fatalf("every Line naming ALICE must recolor, n2 prefix = %q", linePrefix(n2))
	}

	// a real alt+c keystroke round-trips through the shared listPicker skeleton
	m.mode = modeCharacterPick
	m.list.open(m, characterSource{}, true)
	_, mm2, _ := m.list.handleKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c"), Alt: true}, characterSource{})
	if mm2.(*Model).mode != modeCharacterColor {
		t.Fatalf("alt+c through handleKey must switch to the recolor mode, got %v", mm2.(*Model).mode)
	}
}
