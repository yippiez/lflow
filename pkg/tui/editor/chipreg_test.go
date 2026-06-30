package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// menuModel builds a Model whose cursor sits on a node of the given type, with an
// open "@" chip menu carrying query q.
func menuModel(typ, q string) (*Model, *item) {
	it := &item{uuid: "n", typ: typ, name: "note "}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{
		tree:   tr,
		rows:   []row{{it: it}},
		cursor: 0,
		caret:  len([]rune("note ")),
	}
	m.compl = complState{kind: complChipMenu, start: m.caret, query: q}
	return m, it
}

// creatableOn counts the chip kinds the @ menu should offer on a node type.
func creatableOn(typ string) int {
	n := 0
	for _, s := range chipSpecs {
		if s.create != nil && (s.allowOn == nil || s.allowOn(typ)) {
			n++
		}
	}
	return n
}

func TestChipMenuExcludesDate(t *testing.T) {
	// date has no creator: it is recognised from a typed phrase / ctrl+t, never the
	// @ menu. It must never appear as a creatable kind.
	for _, s := range chipSpecs {
		if s.kind == chipKindDate && s.create != nil {
			t.Fatal("date must have no @ creator (it is keyword-recognised, kept outside)")
		}
	}
}

func TestChipMenuItemsBulletsListsCreatable(t *testing.T) {
	m, _ := menuModel(database.TypeBullets, "")
	items := m.complItems()
	if want := creatableOn(database.TypeBullets); len(items) != want {
		t.Fatalf("@ menu on bullets = %d items, want %d (file/link/tag/cmd)", len(items), want)
	}
	for _, it := range items {
		if it.value == chipKindDate {
			t.Fatal("@ menu must not offer date")
		}
	}
}

func TestChipMenuItemsFilter(t *testing.T) {
	m, _ := menuModel(database.TypeBullets, "fi")
	items := m.complItems()
	if len(items) != 1 || items[0].value != chipKindPath {
		t.Fatalf("@ menu filter 'fi' = %v, want [path]", items)
	}
}

func TestChipMenuItemsBashEmpty(t *testing.T) {
	m, _ := menuModel(database.TypeBash, "")
	if items := m.complItems(); len(items) != 0 {
		t.Fatalf("@ menu on bash = %v, want none (every kind gated out)", items)
	}
	if anyChipAllowed(database.TypeBash) {
		t.Fatal("anyChipAllowed(bash) = true, want false")
	}
}

func TestChipMenuItemsCodeOnlyCmd(t *testing.T) {
	// code keeps ">"/"[["/"#" out (file/link/tag gated), but a cmd chip works on
	// code — so @ offers only cmd there.
	m, _ := menuModel(database.TypeCode, "")
	items := m.complItems()
	if len(items) != 1 || items[0].value != chipKindCmd {
		t.Fatalf("@ menu on code = %v, want [cmd] only", items)
	}
}

func TestChipMenuItemsQueryNode(t *testing.T) {
	// a query node offers tag/cmd via @, but not file/link (">"/"[[") nor date.
	m, _ := menuModel(database.TypeQuery, "")
	got := map[string]bool{}
	for _, it := range m.complItems() {
		got[it.value] = true
	}
	if got[chipKindPath] || got[chipKindLink] || got[chipKindDate] {
		t.Errorf("@ menu on query offered file/link/date, want none of them: %v", got)
	}
	if !got[chipKindTag] || !got[chipKindCmd] {
		t.Errorf("@ menu on query missing tag/cmd: %v", got)
	}
}

func TestApplyChipMenuCmdOpensInput(t *testing.T) {
	m, it := menuModel(database.TypeBullets, "cm")
	it.name = "note @cm"
	m.caret = len([]rune("note @cm"))
	items := m.complItems() // [cmd]
	if len(items) != 1 || items[0].value != chipKindCmd {
		t.Fatalf("setup: items = %v, want [cmd]", items)
	}
	m.applyChipMenu(it, items)
	// "@cm" is stripped and the cmd creator opens the command input ("$" trigger)
	if it.name != "note $" {
		t.Fatalf("name = %q, want %q", it.name, "note $")
	}
	if m.mode != modeComplete || m.compl.kind != complCmd {
		t.Fatalf("after @cmd, mode=%v kind=%v, want modeComplete/complCmd", m.mode, m.compl.kind)
	}
}

func TestApplyChipMenuTagOpensCompleter(t *testing.T) {
	m, it := menuModel(database.TypeBullets, "tag")
	it.name = "note @tag"
	m.caret = len([]rune("note @tag"))
	m.applyChipMenu(it, m.complItems())
	if m.mode != modeComplete || m.compl.kind != complTag {
		t.Fatalf("after @tag, mode=%v kind=%v, want modeComplete/complTag", m.mode, m.compl.kind)
	}
}

func TestCmdComplItemsPreview(t *testing.T) {
	m := &Model{compl: complState{kind: complCmd, query: "ls -la"}}
	items := m.complItems()
	if len(items) != 1 || items[0].value != "ls -la" {
		t.Fatalf("cmd input 'ls -la' = %v, want one preview row valued 'ls -la'", items)
	}
	m.compl.query = "  "
	if items := m.complItems(); len(items) != 0 {
		t.Fatalf("empty cmd input = %v, want no row", items)
	}
}

func TestApplyCompletionCmdInsertsChip(t *testing.T) {
	it := &item{uuid: "n", typ: database.TypeBullets, name: "run $echo hi"}
	tr := &tree{byUUID: map[string]*item{"n": it}}
	m := &Model{
		tree:   tr,
		rows:   []row{{it: it}},
		cursor: 0,
		caret:  len([]rune("run $echo hi")),
		chips:  map[string]database.Chip{},
	}
	m.compl = complState{kind: complCmd, start: len([]rune("run ")), query: "echo hi"}
	m.applyCompletion(it, m.complItems())
	if !hasAnchor(it.name) {
		t.Fatalf("@cmd completion should splice a chip anchor, got %q", it.name)
	}
	for _, c := range m.chips {
		if c.Kind != chipKindCmd || c.Value != "echo hi" {
			t.Errorf("created chip = %+v, want cmd 'echo hi'", c)
		}
	}
}
